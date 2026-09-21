// Package model, store ve httpapi katmanları arasında paylaşılan alan tiplerini tutar.
package model

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/mail"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"
)

type User struct {
	ID           uuid.UUID `json:"id"`
	Email        string    `json:"email"`
	FullName     *string   `json:"full_name,omitempty"` // görünen ad; giriş e-postayla yapılır
	PasswordHash string    `json:"-"`
	Role         string    `json:"role"`
	// Phone, ileride SMS bildirimi için; TwoFactor*, ileride iki faktörlü doğrulama için hesap bazlı anahtar
	// (henüz uygulanmadı: yalnızca saklanır).
	Phone            *string    `json:"phone,omitempty"`
	TwoFactorEnabled bool       `json:"two_factor_enabled"`
	TwoFactorChannel *string    `json:"two_factor_channel,omitempty"`
	CreatedAt        time.Time  `json:"created_at"`
	LastLoginAt      *time.Time `json:"last_login_at,omitempty"`
	// MustChangePassword: kullanıcı, API başka bir şey yapmasına izin vermeden önce yeni bir şifre
	// seçmek zorundadır (bkz. httpapi.requireAuth).
	MustChangePassword bool `json:"must_change_password"`
}

const (
	MinPasswordLength = 12
	// bcrypt yalnızca ilk 72 baytı kullanır ve kütüphanenin yeni sürümleri daha uzun girdiyi
	// reddeder; bu eskiden kullanıcı oluşturmada 500 olarak ortaya çıkıyordu.
	MaxPasswordBytes = 72
)

// ValidatePassword, bir şifrenin ayarlandığı her yer için şifre politikasını uygular.
func ValidatePassword(password string) error {
	if utf8.RuneCountInString(password) < MinPasswordLength {
		return fmt.Errorf("şifre en az %d karakter olmalı", MinPasswordLength)
	}
	if len(password) > MaxPasswordBytes {
		return fmt.Errorf("şifre en fazla %d bayt olabilir", MaxPasswordBytes)
	}
	return nil
}

// NormalizeEmail bir kullanıcı e-postasının kanonik biçimini döndürür — kırpılmış ve küçük
// harfli; böylece "Alice@X.com" ve "alice@x.com" tek hesaptır — ya da sade bir adres
// değilse (görünen ad, boşluk ya da kontrol karakteri yok) hata verir.
func NormalizeEmail(s string) (string, error) {
	s = strings.ToLower(strings.TrimSpace(s))
	addr, err := mail.ParseAddress(s)
	if err != nil || addr.Address != s || strings.ContainsAny(s, " \t\r\n<>,;") {
		return "", errors.New("e-posta ad@ornek.com gibi sade bir adres olmalı")
	}
	return s, nil
}

type Organization struct {
	ID uuid.UUID `json:"id"`
	// ParentOrganizationID, organizasyon ağacındaki üst şirkettir (nil = kök).
	ParentOrganizationID *uuid.UUID `json:"parent_organization_id,omitempty"`
	Name                 string     `json:"name"`
	Address              *string    `json:"address,omitempty"`
	CreatedAt            time.Time  `json:"created_at"`
	// Access, listeyi isteyen kullanıcının bu organizasyondaki erişimidir: "full" (atandığı dal) ya da
	// "context" (yalnızca üst zincir: adı bilgi olarak görünür, içeriği ve kardeş dalları görünmez).
	// Yalnızca listelerde doludur.
	Access string `json:"access,omitempty"`
}

const (
	OrgAccessFull    = "full"
	OrgAccessContext = "context"
)

// OrganizationContact, bir organizasyonun sunucu işleri için başvurulacak kişisidir (paneli olmayan
// müşteri yetkilileri dahil).
type OrganizationContact struct {
	ID               uuid.UUID  `json:"id"`
	OrganizationID   uuid.UUID  `json:"organization_id"`
	Department       *string    `json:"department,omitempty"`
	Title            *string    `json:"title,omitempty"`
	Name             string     `json:"name"`
	ManagerContactID *uuid.UUID `json:"manager_contact_id,omitempty"`
	Phone            *string    `json:"phone,omitempty"`
	Email            *string    `json:"email,omitempty"`
	CreatedAt        time.Time  `json:"created_at"`
	UpdatedAt        time.Time  `json:"updated_at"`
}

type Host struct {
	ID             uuid.UUID `json:"id"`
	OrganizationID uuid.UUID `json:"organization_id"`
	// Title, panelde görünen addır; makinenin kendi bildirdiği hostname HostInfo.Hostname'dedir.
	Title           string     `json:"title"`
	IP              string     `json:"ip"`
	Mode            string     `json:"mode"`
	IntervalSeconds int        `json:"interval_seconds"`
	Status          string     `json:"status"`
	LastSeen        *time.Time `json:"last_seen,omitempty"`
	PullPort        *int       `json:"pull_port,omitempty"`
	PullEndpoint    *string    `json:"pull_endpoint,omitempty"`
	// AllMountsAlert true ise agent'ın raporladığı her mount disk alert'i üretebilir; false ise yalnızca
	// CustomAlertMounts'taki mount yolları (boş liste = hiçbiri).
	AllMountsAlert    bool     `json:"all_mounts_alert"`
	CustomAlertMounts []string `json:"custom_alert_mounts"`
	// CPUCores/RAMTotalMB, agent'ın en son raporunda bildirdiği donanım toplamlarıdır (bkz.
	// MetricsIngestRequest). nil = henüz bunları gönderen bir agent sürümüyle rapor alınmadı.
	CPUCores   *int   `json:"cpu_cores,omitempty"`
	RAMTotalMB *int64 `json:"ram_total_mb,omitempty"`
	// PhysicalDisks, agent'ın son raporundaki fiziksel diskler ve üzerlerindeki mount'lardır. Bir
	// mount birden çok diske düşebilir (LVM/mdraid); hiçbir diske düşmeyen mount'lar (NFS, tmpfs)
	// hiçbir listede yer almaz. nil = bilinmiyor.
	PhysicalDisks []PhysicalDisk `json:"physical_disks,omitempty"`
	// AgentVersion/AgentProtocol, agent'ın son isteğinde başlıktan bildirdiği sürümdür (bkz.
	// AgentInfo). AgentProtocol 1 ve AgentVersion nil = sürüm bildirmeyen eski agent; ikisi de nil =
	// henüz hiç rapor alınmadı.
	AgentVersion  *string `json:"agent_version,omitempty"`
	AgentProtocol *int    `json:"agent_protocol,omitempty"`
	// UnsupportedFields, agent'ın son raporunda gönderdiği ama bu server sürümünün tanımadığı
	// alan adlarıdır: agent server'dan yeni, server'ın güncellenmesi gerekir.
	UnsupportedFields []string `json:"unsupported_fields,omitempty"`
	// HostInfo, agent'ın son raporundaki makine envanteri ve anlık durumudur (protokol 3); nil =
	// henüz bildirmedi (eski agent). Yalnızca bilgi içindir.
	HostInfo  *HostInfo `json:"host_info,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// PhysicalDisk, bir fiziksel (ya da sanal) blok aygıtıdır. SizeBytes aygıtın ham boyutudur,
// üzerindeki dosya sisteminin değil (o DiskUsage.Total'dir).
type PhysicalDisk struct {
	Name      string   `json:"name"`
	Model     string   `json:"model,omitempty"`
	SizeBytes int64    `json:"size_bytes,omitempty"`
	Kind      string   `json:"kind,omitempty"` // nvme | ssd | hdd; boş = bilinmiyor
	Mounts    []string `json:"mounts"`
}

// Hardware, agent'ın bildirdiği ve hosts'ta "son bilinen değer" olarak saklanan donanım
// bilgisidir. Sıfır değer / boş liste "bilinmiyor" demektir ve mevcut kaydı silmez.
type Hardware struct {
	CPUCores      int
	RAMTotalMB    int64
	PhysicalDisks []PhysicalDisk
	HostInfo      *HostInfo // sanitize edilmiş; nil = bilinmiyor
}

// Fiziksel disk listesi için kabul sınırları. Liste JSONB olarak saklanır ve her panel isteğinde
// döner; hatalı ya da düşmanca bir agent'ın şişirebilmesi engellenir.
const (
	maxPhysicalDisks  = 64
	maxDiskNameBytes  = 64
	maxDiskModelBytes = 128
	maxDiskKindBytes  = 16
	maxDiskMountBytes = 4096
	maxMountsPerDisk  = 64
)

// cleanText, denetim karakterlerini atar (JSONB "\u0000"ı reddeder) ve metni rune sınırında
// en çok max bayta kırpar.
func cleanText(s string, max int) string {
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, strings.ToValidUTF8(s, ""))
	s = strings.TrimSpace(s)
	if len(s) <= max {
		return s
	}
	cut := max
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut]
}

// sanitizePhysicalDisks, agent'ın bildirdiği listeyi saklanabilir hâle getirir: adı olmayan
// ya da tekrar eden diskler atılır, metinler kırpılır, negatif boyut 0 olur, sayılar sınırlanır.
// Bozuk bir disk listesi tüm metrik alımını (CPU/RAM/disk/Docker) reddettirmemeli; bu yüzden
// hata döndürmek yerine temizler. Sonuç boşsa nil döner ("bilinmiyor").
func sanitizePhysicalDisks(in []PhysicalDisk) []PhysicalDisk {
	var out []PhysicalDisk
	seen := map[string]struct{}{}
	for _, d := range in {
		if len(out) == maxPhysicalDisks {
			break
		}
		name := cleanText(d.Name, maxDiskNameBytes)
		if _, dup := seen[name]; name == "" || dup {
			continue
		}
		seen[name] = struct{}{}

		clean := PhysicalDisk{
			Name:   name,
			Model:  cleanText(d.Model, maxDiskModelBytes),
			Kind:   cleanText(d.Kind, maxDiskKindBytes),
			Mounts: []string{},
		}
		if d.SizeBytes > 0 {
			clean.SizeBytes = d.SizeBytes
		}
		for _, m := range d.Mounts {
			if len(clean.Mounts) == maxMountsPerDisk {
				break
			}
			if m = cleanText(m, maxDiskMountBytes+1); m != "" && len(m) <= maxDiskMountBytes {
				clean.Mounts = append(clean.Mounts, m)
			}
		}
		out = append(out, clean)
	}
	return out
}

const (
	maxMountEntries = 64
	maxMountBytes   = 255
)

// ValidateMountList bir disk_alert_mounts seçimini denetler (nil = "tümü", her zaman geçerli).
// Girdiler agent'ın raporladığı gibi birebir karşılaştırılır; bu yüzden normalleştirme yapılmaz.
func ValidateMountList(mounts []string) error {
	if len(mounts) > maxMountEntries {
		return fmt.Errorf("en fazla %d mount seçilebilir", maxMountEntries)
	}
	seen := make(map[string]struct{}, len(mounts))
	for _, m := range mounts {
		switch {
		case !strings.HasPrefix(m, "/"):
			return fmt.Errorf("%q mutlak bir yol olmalı ve / ile başlamalı", m)
		case len(m) > maxMountBytes:
			return fmt.Errorf("mount yolu %d bayttan uzun olamaz", maxMountBytes)
		case strings.IndexFunc(m, unicode.IsControl) >= 0:
			return errors.New("mount yolları kontrol karakteri içeremez")
		}
		if _, dup := seen[m]; dup {
			return fmt.Errorf("%q iki kez listelenmiş", m)
		}
		seen[m] = struct{}{}
	}
	return nil
}

const (
	RoleSuperAdmin = "super_admin"
	RoleOrgAdmin   = "org_admin"
	RoleOperator   = "operator"
)

func ValidRole(role string) bool {
	switch role {
	case RoleSuperAdmin, RoleOrgAdmin, RoleOperator:
		return true
	default:
		return false
	}
}

const (
	HostModePush = "push"
	HostModePull = "pull"
)

func ValidHostMode(mode string) bool {
	return mode == HostModePush || mode == HostModePull
}

// DiskUsage ve DockerContainerReport, agent'ın push payload'ını (docs/MIMARI.md
// bölüm 7: POST /api/v1/metrics gövdesi) birebir yansıtır; böylece httpapi katmanı doğrudan
// onlara çözebilir.
type DiskUsage struct {
	Mount   string  `json:"mount"`
	UsedPct float64 `json:"used_pct"`
	Total   int64   `json:"total"`
	Free    int64   `json:"free"`
	// InodesUsedPct, dosya sisteminin inode doluluğudur (protokol 3); nil = bilinmiyor / dosya sistemi
	// inode sayısı bildirmiyor (ör. btrfs). Disk yüzdesinden önce dolabilir; yalnızca bilgi içindir.
	InodesUsedPct *float64 `json:"inodes_used_pct,omitempty"`
}

type DockerContainerReport struct {
	Name          string  `json:"name"`
	Image         string  `json:"image"`
	Status        string  `json:"status"`
	CPUPct        float64 `json:"cpu_pct"`
	RAMMB         float64 `json:"ram_mb"`
	RestartCount  int     `json:"restart_count"`
	UptimeSeconds int64   `json:"uptime_seconds"`
}

// ValidDockerStatus, docker_containers.status üzerindeki CHECK kısıtını yansıtır.
func ValidDockerStatus(status string) bool {
	switch status {
	case "created", "running", "paused", "restarting", "exited", "dead", "removing":
		return true
	default:
		return false
	}
}

type MetricsIngestRequest struct {
	CPUUsagePct float64     `json:"cpu_usage_pct"`
	RAMUsagePct float64     `json:"ram_usage_pct"`
	Disk        []DiskUsage `json:"disk"`
	// CPUCores/RAMTotalMB isteğe bağlıdır (eski agent'lar göndermez): 0 = "bilinmiyor", panel bu
	// durumda yalnızca yüzdeyi gösterir. hosts tablosunda "son bilinen değer" olarak saklanır,
	// her metrik satırında tekrarlanmaz (bkz. store.Hosts.MarkOnline).
	CPUCores   int   `json:"cpu_cores,omitempty"`
	RAMTotalMB int64 `json:"ram_total_mb,omitempty"`
	// PhysicalDisks isteğe bağlıdır ve aynı "son bilinen değer" kuralına uyar; boş/eksik =
	// "bilinmiyor". Hatalı girdiler reddedilmez, Hardware() içinde temizlenir.
	PhysicalDisks []PhysicalDisk `json:"physical_disks,omitempty"`
	// HostInfo (protokol 3) isteğe bağlıdır ve aynı "son bilinen değer" kuralına uyar.
	HostInfo         *HostInfo               `json:"host_info,omitempty"`
	DockerContainers []DockerContainerReport `json:"docker_containers"`
}

// Hardware, isteğin donanım bölümünü saklanacak (temizlenmiş) hâliyle döndürür.
func (r MetricsIngestRequest) Hardware() Hardware {
	return Hardware{CPUCores: r.CPUCores, RAMTotalMB: r.RAMTotalMB, PhysicalDisks: sanitizePhysicalDisks(r.PhysicalDisks), HostInfo: sanitizeHostInfo(r.HostInfo)}
}

// Validate, hiçbir sağlıklı agent'ın göndermeyeceği okumaları reddeder. İki alım yolu da
// (push handler'ı ve pull scheduler) onu kullanır; böylece çekilen bir okuma, itilen bir
// okumayla aynı ölçüte tabi tutulur.
func (r MetricsIngestRequest) Validate() error {
	if r.CPUUsagePct < 0 || r.CPUUsagePct > 100 || r.RAMUsagePct < 0 || r.RAMUsagePct > 100 {
		return errors.New("cpu_usage_pct ve ram_usage_pct 0 ile 100 arasında olmalı")
	}
	if r.CPUCores < 0 {
		return errors.New("cpu_cores negatif olamaz")
	}
	if r.RAMTotalMB < 0 {
		return errors.New("ram_total_mb negatif olamaz")
	}
	return nil
}

// MetricPoint, panelin grafikleri için GET /hosts/:id/metrics'in döndürdüğü tek bir geçmiş örnektir.
type MetricPoint struct {
	Timestamp   time.Time   `json:"timestamp"`
	CPUUsagePct float64     `json:"cpu_usage_pct"`
	RAMUsagePct float64     `json:"ram_usage_pct"`
	Disk        []DiskUsage `json:"disk"`
}

// AuditLog kaydedilmiş tek bir kritik eylemdir (docs/MIMARI.md bölüm 5). ActorEmail bir
// anlık görüntüdür; böylece kullanıcı silindikten sonra da satırlar atfedilebilir kalır
// (UserID o zaman null olur).
type AuditLog struct {
	ID         uuid.UUID       `json:"id"`
	UserID     *uuid.UUID      `json:"user_id,omitempty"`
	ActorEmail string          `json:"actor_email"`
	Action     string          `json:"action"`
	TargetType string          `json:"target_type"`
	TargetID   *string         `json:"target_id,omitempty"`
	Details    json.RawMessage `json:"details,omitempty"`
	IP         *string         `json:"ip,omitempty"`
	CreatedAt  time.Time       `json:"created_at"`
}

const (
	MetricTypeCPU           = "cpu"
	MetricTypeRAM           = "ram"
	MetricTypeDisk          = "disk"
	MetricTypeDockerRestart = "docker_restart"
	// AlertTypeHostOffline yalnızca alert'lerde kullanılır, eşik tablolarında asla (onun için anlamlı
	// bir eşik değeri yok).
	AlertTypeHostOffline = "host_offline"
	// AlertTypeDiskMissing de yalnızca alert içindir: beklenen bir mount (Alert.Subject) art arda
	// birkaç raporda raporlanmadı.
	AlertTypeDiskMissing = "disk_missing"
)

// ValidThresholdMetricType bilerek AlertTypeHostOffline'ı dışarıda bırakır — docker_restart'ın
// burada geçerli bir eşik türü olarak kabul edilmesine rağmen neden henüz gerçek alert'e
// bağlanmadığı için bkz. PROGRESS.md kararları.
func ValidThresholdMetricType(metricType string) bool {
	switch metricType {
	case MetricTypeCPU, MetricTypeRAM, MetricTypeDisk, MetricTypeDockerRestart:
		return true
	default:
		return false
	}
}

// maxDockerRestartLevel, makul her restart sayısının çok üstündedir ve threshold_configs'in
// NUMERIC(10,2)'si içinde güvenle kalır.
const maxDockerRestartLevel = 1_000_000

// ValidateThresholdLevels, hiçbir zaman istenen gibi davranamayacak seviyeleri reddeder:
// negatif bir seviye her şeyde tetiklenir ve 100'ün üstündeki yüzdeye asla ulaşılamaz; yani
// "alert" sessizce hiç var olmazdı.
func ValidateThresholdLevels(metricType string, warning, critical float64) error {
	if warning < 0 || critical < 0 {
		return errors.New("warning_level ve critical_level negatif olamaz")
	}
	if warning > critical {
		return errors.New("warning_level, critical_level'dan büyük olamaz")
	}
	switch metricType {
	case MetricTypeCPU, MetricTypeRAM, MetricTypeDisk:
		if critical > 100 {
			return errors.New("cpu, ram ve disk için seviyeler yüzdedir ve en fazla 100 olabilir")
		}
	case MetricTypeDockerRestart:
		if critical > maxDockerRestartLevel {
			return errors.New("docker_restart seviyeleri restart sayısıdır ve en fazla 1000000 olabilir")
		}
	}
	return nil
}

// ThresholdMetricTypes, eşik taşıyan metrik türleridir, gösterim sırasıyla.
var ThresholdMetricTypes = []string{MetricTypeCPU, MetricTypeRAM, MetricTypeDisk, MetricTypeDockerRestart}

// ThresholdLevels tek bir uyarı/kritik çiftidir.
type ThresholdLevels struct {
	WarningLevel  float64 `json:"warning_level"`
	CriticalLevel float64 `json:"critical_level"`
}

// ThresholdOverrides bir host'ın kendi eşikleridir, metrik türüne göre. Nil değer
// "bu metrik için özel eşik yok: varsayılanı kullan" demektir.
type ThresholdOverrides map[string]*ThresholdLevels

// Validate, her anahtarın bir eşik metriği türü ve her özel çiftin makul olduğunu denetler.
func (o ThresholdOverrides) Validate() error {
	for metricType, levels := range o {
		if !ValidThresholdMetricType(metricType) {
			return fmt.Errorf("%q bir eşik metriği değil (cpu, ram, disk veya docker_restart kullanın)", metricType)
		}
		if levels == nil {
			continue
		}
		if err := ValidateThresholdLevels(metricType, levels.WarningLevel, levels.CriticalLevel); err != nil {
			return fmt.Errorf("%s: %w", metricType, err)
		}
	}
	return nil
}

// MountThresholds bir host'ın mount başına disk eşikleridir, mount yoluna göre. Nil değer
// "bu mount'un kendi eşiği yok" demektir (host'ın disk eşiğini izler).
type MountThresholds map[string]*ThresholdLevels

// ContainerThresholds bir host'ın container başına docker_restart eşikleridir, container
// adına göre. Nil değer "bu container'ın kendi eşiği yok" demektir.
type ContainerThresholds map[string]*ThresholdLevels

const (
	maxSubjectThresholds = 64
	maxContainerNameLen  = 255
)

// validateSubjectLevels bir subject->seviye haritasını denetler: en fazla maxSubjectThresholds
// girdi, her subject checkName tarafından kabul edilmiş ve her özel çift metricType için makul
// bir eşik.
func validateSubjectLevels(m map[string]*ThresholdLevels, metricType string, what string, checkName func(string) error) error {
	if len(m) > maxSubjectThresholds {
		return fmt.Errorf("en fazla %d %s için özel eşik girilebilir", maxSubjectThresholds, what)
	}
	for subject, levels := range m {
		if err := checkName(subject); err != nil {
			return err
		}
		if levels == nil {
			continue
		}
		if err := ValidateThresholdLevels(metricType, levels.WarningLevel, levels.CriticalLevel); err != nil {
			return fmt.Errorf("%s: %w", subject, err)
		}
	}
	return nil
}

func hasCustom(m map[string]*ThresholdLevels) bool {
	for _, levels := range m {
		if levels != nil {
			return true
		}
	}
	return false
}

// Validate, her anahtarın bir mount yolu ve her özel çiftin makul bir disk eşiği olduğunu denetler.
func (m MountThresholds) Validate() error {
	return validateSubjectLevels(m, MetricTypeDisk, "mount", func(mount string) error { return ValidateMountList([]string{mount}) })
}

// HasCustom, en az bir mount'un kendi eşiği olup olmadığını bildirir.
func (m MountThresholds) HasCustom() bool { return hasCustom(m) }

// Validate, her anahtarın bir container adı ve her özel çiftin makul bir restart eşiği olduğunu denetler.
func (c ContainerThresholds) Validate() error {
	return validateSubjectLevels(c, MetricTypeDockerRestart, "container", func(name string) error {
		switch {
		case strings.TrimSpace(name) == "":
			return errors.New("container adı boş olamaz")
		case len(name) > maxContainerNameLen:
			return fmt.Errorf("container adı %d bayttan uzun olamaz", maxContainerNameLen)
		case strings.IndexFunc(name, unicode.IsControl) >= 0:
			return errors.New("container adı kontrol karakteri içeremez")
		}
		return nil
	})
}

// HasCustom, en az bir container'ın kendi eşiği olup olmadığını bildirir.
func (c ContainerThresholds) HasCustom() bool { return hasCustom(c) }

// HasCustom, en az bir metriğin özel (nil olmayan) eşiği olup olmadığını bildirir.
func (o ThresholdOverrides) HasCustom() bool {
	for _, levels := range o {
		if levels != nil {
			return true
		}
	}
	return false
}

type ThresholdConfig struct {
	ID             uuid.UUID  `json:"id"`
	OrganizationID *uuid.UUID `json:"organization_id,omitempty"`
	HostID         *uuid.UUID `json:"host_id,omitempty"`
	MetricType     string     `json:"metric_type"`
	// Subject, host'ın mount başına disk eşiği için mount yoludur; aksi halde boştur.
	Subject       string    `json:"subject,omitempty"`
	WarningLevel  float64   `json:"warning_level"`
	CriticalLevel float64   `json:"critical_level"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

const (
	// AlertLevelInfo yalnızca bilgi amaçlı, zararsız alert'lerdir (varsayılan olarak bildirim gönderilmez).
	AlertLevelInfo     = "info"
	AlertLevelWarning  = "warning"
	AlertLevelCritical = "critical"

	AlertStatusOpen         = "open"
	AlertStatusAcknowledged = "acknowledged"
	AlertStatusResolved     = "resolved"
)

type Alert struct {
	ID     uuid.UUID `json:"id"`
	HostID uuid.UUID `json:"host_id"`
	// AlertType, alert'in türüdür: metrik alert'lerinde metriğin adı (cpu, ram, disk, docker_restart), ayrıca
	// olay türleri (host_offline, disk_missing).
	AlertType string `json:"alert_type"`
	// Subject, alert'in host içinde neyle ilgili olduğunu söyler — docker_restart alert'leri için
	// container adı; host'ın bütününü ilgilendiren alert'ler için boş.
	Subject string `json:"subject,omitempty"`
	Level   string `json:"level"`
	Status  string `json:"status"`
	// Value ve Threshold, alert tetiklendiği andaki ölçülen değer ve tetikleyen eşiktir (olay alert'lerinde nil).
	Value          *float64   `json:"value,omitempty"`
	Threshold      *float64   `json:"threshold,omitempty"`
	CreatedAt      time.Time  `json:"created_at"`
	AcknowledgedAt *time.Time `json:"acknowledged_at,omitempty"`
	AcknowledgedBy *uuid.UUID `json:"acknowledged_by,omitempty"`
	ResolvedAt     *time.Time `json:"resolved_at,omitempty"`
}

// Bildirim kanalları. Veritabanı ileride eklenecek kanalları da kabul eder; API yalnızca burada
// "uygulanmış" olanları kabul eder (aksi halde biri hiç gitmeyecek bir kanal seçebilirdi).
const (
	ChannelEmail    = "email"
	ChannelSMS      = "sms"
	ChannelSlack    = "slack"
	ChannelDiscord  = "discord"
	ChannelTelegram = "telegram"
)

// ImplementedChannels, gerçekten mesaj gönderebilen kanallardır.
var ImplementedChannels = []string{ChannelEmail}

func ValidChannel(c string) bool {
	switch c {
	case ChannelEmail, ChannelSMS, ChannelSlack, ChannelDiscord, ChannelTelegram:
		return true
	}
	return false
}

func ImplementedChannel(c string) bool {
	for _, x := range ImplementedChannels {
		if x == c {
			return true
		}
	}
	return false
}

// ValidAlertLevel, alert seviyeleri ve bildirim kuralının en düşük seviyesi için.
func ValidAlertLevel(l string) bool {
	return l == AlertLevelInfo || l == AlertLevelWarning || l == AlertLevelCritical
}

// LevelRank, seviyeleri sıralar (info < warning < critical).
func LevelRank(l string) int {
	switch l {
	case AlertLevelInfo:
		return 0
	case AlertLevelWarning:
		return 1
	case AlertLevelCritical:
		return 2
	}
	return -1
}

// NotificationRoute, bir alert'in kime, hangi kanaldan ve en az hangi seviyeden gideceğini söyler. Kapsam bir
// organizasyon (altındaki dal için de geçerli) ya da tek bir sunucudur; alıcı bir panel kullanıcısı ya da
// organizasyonun bir iletişim kişisidir. Bir alert için en özel kapsamdaki kurallar geçerlidir; hiç kural yoksa
// varsayılan alıcılar kullanılır.
type NotificationRoute struct {
	ID             uuid.UUID  `json:"id"`
	OrganizationID *uuid.UUID `json:"organization_id,omitempty"`
	HostID         *uuid.UUID `json:"host_id,omitempty"`
	UserID         *uuid.UUID `json:"user_id,omitempty"`
	ContactID      *uuid.UUID `json:"contact_id,omitempty"`
	Channel        string     `json:"channel"`
	MinLevel       string     `json:"min_level"`
	CreatedAt      time.Time  `json:"created_at"`
	// RecipientName ve RecipientTarget yalnızca listelerde doludur (gösterim için): alıcının adı ve kanalın adresi.
	RecipientName   string `json:"recipient_name,omitempty"`
	RecipientTarget string `json:"recipient_target,omitempty"`
}
