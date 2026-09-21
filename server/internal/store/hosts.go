package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"healthbeat-server/internal/model"
	"healthbeat-server/internal/secretbox"
)

// ErrNoSecretBox, store bir anahtar olmadan kurulduğunda pull secret şifrelemesi ya da
// çözmesi gereken işlemlerce döndürülür.
var ErrNoSecretBox = errors.New("hosts store has no secret encryption key configured")

// Hosts, izlenen makineleri yönetir. Bir host üç tabloya yayılır: hosts (kimlik ve bağlantı ayarı, nadiren değişir),
// host_status (durum ve her raporda değişen bilgiler) ve host_inventory (yavaş değişen envanter); model.Host bunların
// birleşimidir.
type Hosts struct {
	pool *pgxpool.Pool
	// box, hosts.pull_secret_enc'i at-rest şifreler. pull secret'lara hiç dokunmayan çağıranlar
	// (alert engine, offline monitor) için nil olabilir; dokunan metotlar o zaman düz metin
	// saklamak yerine ErrNoSecretBox ile başarısız olur.
	box *secretbox.Box
}

func NewHosts(pool *pgxpool.Pool, box *secretbox.Box) *Hosts {
	return &Hosts{pool: pool, box: box}
}

// hostSelect, model.Host'un tüm alanlarını üç tablodan (ve özel mount listesinden) tek sorguda okur.
const hostSelect = `
SELECT h.id, h.organization_id, h.title, host(h.ip), h.mode, h.interval_seconds,
       s.status, s.last_seen, h.pull_port, h.pull_endpoint, h.all_mounts_alert,
       COALESCE((SELECT array_agg(m.mount ORDER BY m.mount) FROM host_custom_mounts_alerts m WHERE m.host_id = h.id), '{}'::text[]),
       i.cpu_cores, i.ram_total_mb, i.physical_disks,
       s.agent_version, s.agent_protocol, s.unsupported_fields,
       i.hostname, i.machine_id_hash, i.info, s.runtime,
       h.created_at, h.updated_at
FROM hosts h
JOIN host_status s ON s.host_id = h.id
JOIN host_inventory i ON i.host_id = h.id`

func scanHost(row interface{ Scan(...any) error }) (model.Host, error) {
	var h model.Host
	var hostname, machineID *string
	var info, runtime []byte
	err := row.Scan(&h.ID, &h.OrganizationID, &h.Title, &h.IP, &h.Mode, &h.IntervalSeconds,
		&h.Status, &h.LastSeen, &h.PullPort, &h.PullEndpoint, &h.AllMountsAlert, &h.CustomAlertMounts,
		&h.CPUCores, &h.RAMTotalMB, &h.PhysicalDisks,
		&h.AgentVersion, &h.AgentProtocol, &h.UnsupportedFields,
		&hostname, &machineID, &info, &runtime,
		&h.CreatedAt, &h.UpdatedAt)
	if err != nil {
		return h, err
	}
	h.HostInfo = mergeHostInfo(hostname, machineID, info, runtime)
	return h, nil
}

// mergeHostInfo, envanterin yavaş değişen kısmını (info), her raporda değişen kısmını (runtime) ve ayrı kolonlara
// alınmış hostname / machine_id_hash'i tek model.HostInfo'da birleştirir. Hiçbiri yoksa nil (henüz bildirilmedi).
func mergeHostInfo(hostname, machineID *string, info, runtime []byte) *model.HostInfo {
	if hostname == nil && machineID == nil && len(info) == 0 && len(runtime) == 0 {
		return nil
	}
	var h model.HostInfo
	if len(info) > 0 {
		_ = json.Unmarshal(info, &h)
	}
	if len(runtime) > 0 {
		_ = json.Unmarshal(runtime, &h) // alanlar info ile ayrıktır; yalnızca gelenler üzerine yazılır
	}
	if hostname != nil {
		h.Hostname = *hostname
	}
	if machineID != nil {
		h.MachineIDHash = *machineID
	}
	return &h
}

// splitHostInfo, birleşik envanteri saklandığı parçalara ayırır: makinenin bildirdiği hostname ve machine_id_hash
// (kendi kolonları), yavaş değişen bilgiler (info) ve uçucu bilgiler (runtime). h nil ise (rapor host_info taşımıyor)
// hepsi nil olur; h varsa info ve runtime boş olsa bile "{}" döner: bildirilen host_info eskisinin YERİNE geçer.
func splitHostInfo(h *model.HostInfo) (hostname, machineID, info, runtime *string, err error) {
	if h == nil {
		return nil, nil, nil, nil, nil
	}
	if h.Hostname != "" {
		v := h.Hostname
		hostname = &v
	}
	if h.MachineIDHash != "" {
		v := h.MachineIDHash
		machineID = &v
	}
	volatile := model.HostInfo{
		UptimeSeconds: h.UptimeSeconds, BootTime: h.BootTime, LoadAvg: h.LoadAvg, Swap: h.Swap,
		RebootRequired: h.RebootRequired, TimeSynced: h.TimeSynced, FailedUnits: h.FailedUnits,
	}
	stable := *h
	stable.Hostname, stable.MachineIDHash = "", ""
	stable.UptimeSeconds, stable.BootTime, stable.LoadAvg, stable.Swap = 0, "", nil, nil
	stable.RebootRequired, stable.TimeSynced, stable.FailedUnits = nil, nil, nil
	marshal := func(v model.HostInfo) (*string, error) {
		b, err := json.Marshal(v)
		if err != nil {
			return nil, err
		}
		str := string(b)
		return &str, nil
	}
	if info, err = marshal(stable); err != nil {
		return
	}
	runtime, err = marshal(volatile)
	return
}

type CreateHostParams struct {
	OrganizationID  uuid.UUID
	Title           string
	IP              string
	Mode            string
	IntervalSeconds int
	APITokenHash    *string
	// AllMountsAlert false ise yalnızca CustomAlertMounts'taki mount'lar disk alert'i üretir.
	AllMountsAlert    bool
	CustomAlertMounts []string
	PullPort          *int
	PullEndpoint      *string
	// PullSecret düz metin paylaşılan secret'tır. APITokenHash'ten farklı olarak tek yönlü hash
	// olamaz (server onu her poll'da sunar); bu yüzden store onu at-rest AES-GCM ile şifreler.
	PullSecret *string
	// Thresholds, host'ın özel eşikleridir ve host'ın kendisiyle aynı transaction'da yazılır
	// (nil değerler yok sayılır: yeni bir host'ın kaldıracak eşiği yoktur).
	Thresholds model.ThresholdOverrides
	// MountThresholds, host'ın mount başına disk eşikleridir ve onunla birlikte yazılır.
	MountThresholds model.MountThresholds
	// ContainerThresholds, host'ın container başına docker_restart eşikleridir; onunla birlikte yazılır.
	ContainerThresholds model.ContainerThresholds
}

type rowQuerier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

func (s *Hosts) Create(ctx context.Context, p CreateHostParams) (model.Host, error) {
	// Kimlik burada seçilir (kolon varsayılanıyla değil) çünkü şifrelenmiş pull secret ona ilişkili
	// veri olarak bağlıdır.
	id := uuid.New()

	var sealedSecret *string
	if p.PullSecret != nil {
		sealed, err := s.seal(*p.PullSecret, id)
		if err != nil {
			return model.Host{}, err
		}
		sealedSecret = &sealed
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return model.Host{}, err
	}
	defer tx.Rollback(ctx) // commit edildikten sonra etkisiz

	_, err = tx.Exec(ctx,
		`INSERT INTO hosts (id, organization_id, title, ip, mode, interval_seconds, api_token_hash, pull_port, pull_endpoint, pull_secret_enc, all_mounts_alert)
		 VALUES ($1, $2, $3, $4::inet, $5, $6, $7, $8, $9, $10, $11)`,
		id, p.OrganizationID, p.Title, p.IP, p.Mode, p.IntervalSeconds, p.APITokenHash, p.PullPort, p.PullEndpoint, sealedSecret, p.AllMountsAlert,
	)
	if err != nil {
		switch pgErrorCode(err) {
		case pgForeignKeyViolation:
			return model.Host{}, fmt.Errorf("%w: organizasyon yok", ErrNotFound)
		case pgCheckViolation:
			return model.Host{}, fmt.Errorf("%w: alanlar seçilen moda uymuyor", ErrConflict)
		case pgUniqueViolation:
			return model.Host{}, fmt.Errorf("%w: bu organizasyonda aynı adlı bir sunucu zaten var", ErrConflict)
		}
		return model.Host{}, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO host_status (host_id) VALUES ($1)`, id); err != nil {
		return model.Host{}, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO host_inventory (host_id) VALUES ($1)`, id); err != nil {
		return model.Host{}, err
	}
	if err := setCustomMounts(ctx, tx, id, p.CustomAlertMounts); err != nil {
		return model.Host{}, err
	}
	if err := applyHostOverrides(ctx, tx, id, p.Thresholds, p.MountThresholds, p.ContainerThresholds); err != nil {
		return model.Host{}, err
	}
	h, err := getHost(ctx, tx, id)
	if err != nil {
		return model.Host{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return model.Host{}, err
	}
	return h, nil
}

func getHost(ctx context.Context, q rowQuerier, id uuid.UUID) (model.Host, error) {
	h, err := scanHost(q.QueryRow(ctx, hostSelect+` WHERE h.id = $1`, id))
	if err != nil {
		if isNoRows(err) {
			return model.Host{}, ErrNotFound
		}
		return model.Host{}, err
	}
	return h, nil
}

func (s *Hosts) GetByID(ctx context.Context, id uuid.UUID) (model.Host, error) {
	return getHost(ctx, s.pool, id)
}

// ListByOrganization, title/IP'de bir alt dize araması (ListParams.Search) ve isteğe bağlı
// sayfalama ile bir organizasyonun sunucularını döndürür. total, filtreye uyan toplam satır
// sayısıdır; Limit==0 ise tüm satırlar döner ve total = len(sonuç).
func (s *Hosts) ListByOrganization(ctx context.Context, orgID uuid.UUID, p ListParams) ([]model.Host, int, error) {
	where := "WHERE h.organization_id = $1"
	args := []any{orgID}
	if p.Search != "" {
		args = append(args, "%"+p.Search+"%")
		where += fmt.Sprintf(" AND (h.title ILIKE $%d OR host(h.ip) ILIKE $%d)", len(args), len(args))
	}
	return s.listFiltered(ctx, where, args, p)
}

// ListByIDs, tam olarak ids içindeki host'ları döndürür — "bana atanan host'lar"
// self-servis endpoint'i tarafından kullanılır (bir operatörün aksi halde hangi host'lara
// atandığını keşfetmenin yolu yoktur: organization.view'ları olmadığı için GET
// /organizations/:id/hosts'a ulaşmak üzere organizasyonlara göz atamazlar).
func (s *Hosts) ListByIDs(ctx context.Context, ids []uuid.UUID) ([]model.Host, error) {
	if len(ids) == 0 {
		return []model.Host{}, nil
	}
	rows, err := s.pool.Query(ctx, hostSelect+` WHERE h.id = ANY($1) ORDER BY h.title`, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanHosts(rows)
}

// ListAll her organizasyondaki tüm host'ları döndürür — yalnızca kapsamsız (super_admin) görünümler içindir.
func (s *Hosts) ListAll(ctx context.Context) ([]model.Host, error) {
	rows, err := s.pool.Query(ctx, hostSelect+` ORDER BY h.title`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanHosts(rows)
}

// ListByOrganizationFiltered, ListByOrganization'ın operator kapsamlı halidir: yalnızca
// allowedIDs içindeki sunucuları döndürür (arama ve sayfalama aynı şekilde çalışır).
func (s *Hosts) ListByOrganizationFiltered(ctx context.Context, orgID uuid.UUID, allowedIDs []uuid.UUID, p ListParams) ([]model.Host, int, error) {
	if len(allowedIDs) == 0 {
		return []model.Host{}, 0, nil
	}
	where := "WHERE h.organization_id = $1 AND h.id = ANY($2)"
	args := []any{orgID, allowedIDs}
	if p.Search != "" {
		args = append(args, "%"+p.Search+"%")
		where += fmt.Sprintf(" AND (h.title ILIKE $%d OR host(h.ip) ILIKE $%d)", len(args), len(args))
	}
	return s.listFiltered(ctx, where, args, p)
}

// listFiltered, ListByOrganization ve ListByOrganizationFiltered'ın paylaştığı sayım + sorgu +
// isteğe bağlı LIMIT/OFFSET mantığıdır.
func (s *Hosts) listFiltered(ctx context.Context, where string, args []any, p ListParams) ([]model.Host, int, error) {
	var total int
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM hosts h `+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}

	query := hostSelect + ` ` + where + ` ORDER BY h.title`
	if p.Limit > 0 {
		args = append(args, p.Limit, p.Offset)
		query += fmt.Sprintf(" LIMIT $%d OFFSET $%d", len(args)-1, len(args))
	}
	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	hosts, err := scanHosts(rows)
	return hosts, total, err
}

func scanHosts(rows interface {
	Next() bool
	Scan(...any) error
	Err() error
}) ([]model.Host, error) {
	hosts := []model.Host{}
	for rows.Next() {
		h, err := scanHost(rows)
		if err != nil {
			return nil, err
		}
		hosts = append(hosts, h)
	}
	return hosts, rows.Err()
}

// Update yalnızca moddan bağımsız alanlarda kısmi güncelleme uygular. Mod ve kimlik bilgileri
// burada asla değiştirilmez — bkz. UpdateAPITokenHash / UpdatePullSecret.
func (s *Hosts) Update(ctx context.Context, id uuid.UUID, title, ip *string, intervalSeconds *int) (model.Host, error) {
	tag, err := s.pool.Exec(ctx,
		`UPDATE hosts
		 SET title = COALESCE($2, title),
		     ip = COALESCE($3::inet, ip),
		     interval_seconds = COALESCE($4, interval_seconds),
		     updated_at = now()
		 WHERE id = $1`,
		id, title, ip, intervalSeconds,
	)
	if err != nil {
		if pgErrorCode(err) == pgUniqueViolation {
			return model.Host{}, fmt.Errorf("%w: bu organizasyonda aynı adlı bir sunucu zaten var", ErrConflict)
		}
		return model.Host{}, err
	}
	if tag.RowsAffected() == 0 {
		return model.Host{}, ErrNotFound
	}
	return s.GetByID(ctx, id)
}

func (s *Hosts) UpdateAPITokenHash(ctx context.Context, id uuid.UUID, hash string) error {
	tag, err := s.pool.Exec(ctx, `UPDATE hosts SET api_token_hash = $2, updated_at = now() WHERE id = $1`, id, hash)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Hosts) UpdatePullSecret(ctx context.Context, id uuid.UUID, secret string) error {
	sealed, err := s.seal(secret, id)
	if err != nil {
		return err
	}
	tag, err := s.pool.Exec(ctx, `UPDATE hosts SET pull_secret_enc = $2, updated_at = now() WHERE id = $1`, id, sealed)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// GetAuthByID, bir push modu host'ın api_token'ını doğrulamak için gereken alanları
// döndürür — tam model.Host'ı asla (o hash'i hiç taşımaz). organization_id, requireHostAuth
// onu ikinci bir arama olmadan alert engine'e verebilsin diye dahildir.
func (s *Hosts) GetAuthByID(ctx context.Context, id uuid.UUID) (mode string, organizationID uuid.UUID, apiTokenHash *string, err error) {
	err = s.pool.QueryRow(ctx, `SELECT mode, organization_id, api_token_hash FROM hosts WHERE id = $1`, id).
		Scan(&mode, &organizationID, &apiTokenHash)
	if err != nil {
		if isNoRows(err) {
			return "", uuid.Nil, nil, ErrNotFound
		}
		return "", uuid.Nil, nil, err
	}
	return mode, organizationID, apiTokenHash, nil
}

func (s *Hosts) seal(secret string, hostID uuid.UUID) (string, error) {
	if s.box == nil {
		return "", ErrNoSecretBox
	}
	return s.box.Seal(secret, hostID.String())
}

// PullHostInfo, pull scheduler'ın bir host'ı poll etmek için ihtiyaç duyduğu şeyi tam
// olarak taşır — model.Host üzerinden asla açığa çıkmayan çözülmüş pull secret dahil.
type PullHostInfo struct {
	ID              uuid.UUID
	OrganizationID  uuid.UUID
	IP              string
	PullPort        int
	PullEndpoint    string
	PullSecret      string
	IntervalSeconds int
}

// ListPullHosts, her pull modu host'ı secret'ı çözülmüş olarak döndürür. Secret'ı
// çözülemeyen bir host (anahtar değişti, bozuk satır) tüm listeyi başarısız kılmak yerine
// loglanır ve atlanır; böylece tek kötü satır sağlıklı olanların poll edilmesini durdurmaz;
// host_offline olarak yüzeye çıkar.
func (s *Hosts) ListPullHosts(ctx context.Context) ([]PullHostInfo, error) {
	if s.box == nil {
		return nil, ErrNoSecretBox
	}
	rows, err := s.pool.Query(ctx,
		`SELECT id, organization_id, host(ip), pull_port, pull_endpoint, pull_secret_enc, interval_seconds
		 FROM hosts WHERE mode = 'pull'`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	infos := []PullHostInfo{}
	for rows.Next() {
		var i PullHostInfo
		var stored string
		if err := rows.Scan(&i.ID, &i.OrganizationID, &i.IP, &i.PullPort, &i.PullEndpoint, &stored, &i.IntervalSeconds); err != nil {
			return nil, err
		}
		secret, err := s.box.Open(stored, i.ID.String())
		if err != nil {
			log.Printf("hosts: skipping pull host %s: pull secret cannot be decrypted: %v", i.ID, err)
			continue
		}
		i.PullSecret = secret
		infos = append(infos, i)
	}
	return infos, rows.Err()
}

// MarkOnline, bir host'ın az önce metrik raporladığını kaydeder; push ingest ve pull scheduler
// tarafından her başarılı alımdan sonra çağrılır. hw'nin sıfır/boş alanları ("bilinmiyor" — eski
// bir agent sürümü ya da tek seferlik bir okuma hatası) mevcut değeri korur; sıfırlamaz.
//
// Her rapor yalnızca küçük host_status satırını günceller (durum, agent sürümü, uçucu bilgiler). Yavaş değişen
// envanter (host_inventory) yalnızca içeriği gerçekten değiştiyse yazılır.
//
// agent, istek başlıklarından gelen sürüm bilgisidir ve donanımın aksine her seferinde yazılır
// ("son bilinen" değil, "son isteğin bildirdiği"): sürüm bildirmeyen bir agent'a geri dönülürse
// panel eski sürümü göstermeye devam etmemeli.
func (s *Hosts) MarkOnline(ctx context.Context, id uuid.UUID, hw model.Hardware, agent model.AgentInfo) error {
	var disksJSON *string // nil -> NULL -> COALESCE eski değeri korur
	if len(hw.PhysicalDisks) > 0 {
		b, err := json.Marshal(hw.PhysicalDisks)
		if err != nil {
			return err
		}
		str := string(b)
		disksJSON = &str
	}
	hostname, machineID, infoJSON, runtimeJSON, err := splitHostInfo(hw.HostInfo)
	if err != nil {
		return err
	}
	var unsupportedJSON *string // nil -> NULL: bilinmeyen alan yok (eski kaydı da temizler)
	if len(agent.UnsupportedFields) > 0 {
		b, err := json.Marshal(agent.UnsupportedFields)
		if err != nil {
			return err
		}
		str := string(b)
		unsupportedJSON = &str
	}

	if _, err := s.pool.Exec(ctx,
		`UPDATE host_status SET status = 'online', last_seen = now(),
		        agent_version = NULLIF($2, ''),
		        agent_protocol = $3,
		        unsupported_fields = $4::jsonb,
		        runtime = CASE WHEN $6::boolean THEN $5::jsonb ELSE runtime END,
		        updated_at = now()
		 WHERE host_id = $1`,
		id, agent.Version, agent.Protocol, unsupportedJSON, runtimeJSON, infoJSON != nil,
	); err != nil {
		return err
	}

	// Envanter: yalnızca değişiklik varsa yazılır (her 30 sn'lik raporda aynı JSONB'yi yeniden yazmamak için).
	// host_info bildirilmişse hostname, machine_id_hash ve info eskisinin yerine geçer (eksik parça temizlenir);
	// bildirilmemişse son bilinen değerler korunur. Donanım toplamları ve fiziksel diskler ise yalnızca dolu gelirse yazılır.
	const (
		newHostname  = `CASE WHEN $8::boolean THEN $2::text ELSE hostname END`
		newMachineID = `CASE WHEN $8::boolean THEN $3::text ELSE machine_id_hash END`
		newInfo      = `CASE WHEN $8::boolean THEN $7::jsonb ELSE info END`
		newCores     = `COALESCE(NULLIF($4::int, 0), cpu_cores)`
		newRAM       = `COALESCE(NULLIF($5::bigint, 0), ram_total_mb)`
		newDisks     = `COALESCE($6::jsonb, physical_disks)`
	)
	_, err = s.pool.Exec(ctx,
		`UPDATE host_inventory SET
		        hostname = `+newHostname+`,
		        machine_id_hash = `+newMachineID+`,
		        cpu_cores = `+newCores+`,
		        ram_total_mb = `+newRAM+`,
		        physical_disks = `+newDisks+`,
		        info = `+newInfo+`,
		        updated_at = now()
		 WHERE host_id = $1 AND (
		        hostname IS DISTINCT FROM `+newHostname+`
		     OR machine_id_hash IS DISTINCT FROM `+newMachineID+`
		     OR cpu_cores IS DISTINCT FROM `+newCores+`
		     OR ram_total_mb IS DISTINCT FROM `+newRAM+`
		     OR physical_disks IS DISTINCT FROM `+newDisks+`
		     OR info IS DISTINCT FROM `+newInfo+`)`,
		id, hostname, machineID, hw.CPUCores, hw.RAMTotalMB, disksJSON, infoJSON, infoJSON != nil,
	)
	return err
}

// ListStale, graceMultiplier * kendi interval_seconds süresinden uzun süre sessiz kalmış
// çevrimiçi host'ları döndürür — offline monitor'ün girdisi. Hiç rapor vermemiş host'lar
// (last_seen IS NULL, varsayılan olarak hâlâ 'offline') dahil değildir: yalnızca çevrimiçi
// olup sessizleşmiş bir host için host_offline üretilir, hiç kurulmamış olan için değil.
func (s *Hosts) ListStale(ctx context.Context, graceMultiplier int) ([]model.Host, error) {
	rows, err := s.pool.Query(ctx,
		hostSelect+`
		 WHERE s.status = 'online'
		   AND s.last_seen < now() - (h.interval_seconds * $1 || ' seconds')::interval`,
		graceMultiplier,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanHosts(rows)
}

func (s *Hosts) MarkOffline(ctx context.Context, id uuid.UUID) error {
	_, err := s.pool.Exec(ctx, `UPDATE host_status SET status = 'offline', updated_at = now() WHERE host_id = $1`, id)
	return err
}

// CountByStatus dashboard özetini besler. ids == nil kapsamsız demektir (tüm host'lar —
// super_admin); nil olmayan (belki boş) bir dilim tam o kimliklerle sınırlar
// (org_admin/operator).
func (s *Hosts) CountByStatus(ctx context.Context, ids []uuid.UUID) (online, offline int, err error) {
	query := `SELECT status, count(*) FROM host_status GROUP BY status`
	args := []any{}
	if ids != nil {
		query = `SELECT status, count(*) FROM host_status WHERE host_id = ANY($1) GROUP BY status`
		args = append(args, ids)
	}

	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return 0, 0, err
	}
	defer rows.Close()

	for rows.Next() {
		var status string
		var count int
		if err := rows.Scan(&status, &count); err != nil {
			return 0, 0, err
		}
		switch status {
		case "online":
			online = count
		case "offline":
			offline = count
		}
	}
	return online, offline, rows.Err()
}

// ListIDsByOrganizations, orgIDs'den herhangi birine ait her host'ın kimliğini döndürür —
// bir org_admin'in görebildiği tam host kümesi; alert listelerini kapsamlamak için kullanılır.
func (s *Hosts) ListIDsByOrganizations(ctx context.Context, orgIDs []uuid.UUID) ([]uuid.UUID, error) {
	if len(orgIDs) == 0 {
		return []uuid.UUID{}, nil
	}
	rows, err := s.pool.Query(ctx, `SELECT id FROM hosts WHERE organization_id = ANY($1)`, orgIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	ids := []uuid.UUID{}
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func (s *Hosts) Delete(ctx context.Context, id uuid.UUID) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM hosts WHERE id = $1`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// setCustomMounts, host'ın özel disk alert mount listesini tümden değiştirir.
func setCustomMounts(ctx context.Context, tx pgx.Tx, id uuid.UUID, mounts []string) error {
	if _, err := tx.Exec(ctx, `DELETE FROM host_custom_mounts_alerts WHERE host_id = $1`, id); err != nil {
		return err
	}
	if len(mounts) == 0 {
		return nil
	}
	_, err := tx.Exec(ctx,
		`INSERT INTO host_custom_mounts_alerts (host_id, mount) SELECT $1, unnest($2::text[]) ON CONFLICT DO NOTHING`, id, mounts)
	return err
}

// SetDiskAlertMounts, disk alert'leri için mount seçimini değiştirir: allMounts true ise agent'ın raporladığı her
// mount alert üretebilir (özel liste saklanır ama kullanılmaz); false ise yalnızca mounts'taki mount'lar.
func (s *Hosts) SetDiskAlertMounts(ctx context.Context, id uuid.UUID, allMounts bool, mounts []string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	tag, err := tx.Exec(ctx, `UPDATE hosts SET all_mounts_alert = $2, updated_at = now() WHERE id = $1`, id, allMounts)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	if err := setCustomMounts(ctx, tx, id, mounts); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// DiskAlertMounts, host'ın seçimini döndürür: allMounts true ise tüm raporlanan mount'lar, aksi halde mounts.
func (s *Hosts) DiskAlertMounts(ctx context.Context, id uuid.UUID) (allMounts bool, mounts []string, err error) {
	err = s.pool.QueryRow(ctx,
		`SELECT h.all_mounts_alert, COALESCE((SELECT array_agg(m.mount ORDER BY m.mount) FROM host_custom_mounts_alerts m WHERE m.host_id = h.id), '{}'::text[])
		 FROM hosts h WHERE h.id = $1`, id).Scan(&allMounts, &mounts)
	if err != nil {
		if isNoRows(err) {
			return false, nil, ErrNotFound
		}
		return false, nil, err
	}
	return allMounts, mounts, nil
}

// SameMachineHosts, aynı /etc/machine-id özetini bildiren diğer host'ları döndürür (çift kayıt uyarısı için).
// Benzersizlik zorlanmaz: klonlanmış sanal makineler aynı kimliği taşıyabilir; panel yalnızca uyarır.
func (s *Hosts) SameMachineHosts(ctx context.Context, id uuid.UUID) ([]HostRef, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT o.host_id, h.title, h.organization_id
		 FROM host_inventory me
		 JOIN host_inventory o ON o.machine_id_hash = me.machine_id_hash AND o.host_id <> me.host_id
		 JOIN hosts h ON h.id = o.host_id
		 WHERE me.host_id = $1 AND me.machine_id_hash IS NOT NULL
		 ORDER BY h.title`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []HostRef{}
	for rows.Next() {
		var r HostRef
		if err := rows.Scan(&r.ID, &r.Title, &r.OrganizationID); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// HostRef, bir host'a kısa başvurudur.
type HostRef struct {
	ID             uuid.UUID `json:"id"`
	Title          string    `json:"title"`
	OrganizationID uuid.UUID `json:"organization_id"`
}
