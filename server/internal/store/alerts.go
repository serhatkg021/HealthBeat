package store

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"healthbeat-server/internal/model"
)

type Alerts struct {
	pool *pgxpool.Pool
}

func NewAlerts(pool *pgxpool.Pool) *Alerts {
	return &Alerts{pool: pool}
}

const alertColumns = `id, host_id, alert_type, COALESCE(subject, ''), level, status, value, threshold, created_at, acknowledged_at, acknowledged_by, resolved_at`

func scanAlert(row interface{ Scan(...any) error }) (model.Alert, error) {
	var a model.Alert
	err := row.Scan(&a.ID, &a.HostID, &a.AlertType, &a.Subject, &a.Level, &a.Status, &a.Value, &a.Threshold, &a.CreatedAt, &a.AcknowledgedAt, &a.AcknowledgedBy, &a.ResolvedAt)
	return a, err
}

func (s *Alerts) Create(ctx context.Context, hostID uuid.UUID, alertType, level string) (model.Alert, error) {
	row := s.pool.QueryRow(ctx,
		`INSERT INTO alerts (host_id, alert_type, level) VALUES ($1, $2, $3) RETURNING `+alertColumns,
		hostID, alertType, level,
	)
	return scanAlert(row)
}

// nullable, boş subject'i NULL'a çevirir (subject yok = sunucu geneli).
func nullable(subject string) *string {
	if subject == "" {
		return nil
	}
	return &subject
}

// Aktif alert: çözülmemiş (açık ya da onaylanmış) alert. Onay "gördüm, sustur ama izle" demektir: onaylanan alert
// çözülene kadar aktif kalır, aynı olay için yeni alert açılmasını engeller ve eşik altına inince çözülür
// (bkz. docs/MIMARI.md bölüm 8). Bir host + tür + subject için en fazla bir aktif alert vardır (alerts_one_active_uidx).

// CreateIfNoneActive, bu host+tür+subject için aktif bir alert yoksa atomik olarak bir
// alert açar (alerts_one_active_uidx unique index'i ile sağlanır) ve ekleyip eklemediğini bildirir. subject,
// host'ın bütünüyle ilgili alert'ler için "" (NULL saklanır) ve docker_restart için container adıdır. value ve
// threshold tetiklendiği andaki ölçülen değer ve eşiktir (olay alert'lerinde nil). Eşzamanlı çağıranlar bu yüzden ikisi birden
// oluşturamaz — tam biri created=true alır ve yalnızca o kişi kimseye bildirim yapmalıdır.
func (s *Alerts) CreateIfNoneActive(ctx context.Context, hostID uuid.UUID, alertType, subject, level string, value, threshold *float64) (alert model.Alert, created bool, err error) {
	row := s.pool.QueryRow(ctx,
		`INSERT INTO alerts (host_id, alert_type, subject, level, value, threshold) VALUES ($1, $2, $3, $4, $5, $6)
		 ON CONFLICT DO NOTHING
		 RETURNING `+alertColumns,
		hostID, alertType, nullable(subject), level, value, threshold,
	)
	alert, err = scanAlert(row)
	if err != nil {
		if isNoRows(err) {
			return model.Alert{}, false, nil // aktif bir alert zaten var
		}
		return model.Alert{}, false, err
	}
	return alert, true, nil
}

// GetActive, bu host+metrik için aktif (açık ya da onaylanmış) alert yoksa store.ErrNotFound döndürür — tekrar
// bildirimi önleme/bekleme denetimi (bkz. docs/MIMARI.md bölüm 8).
func (s *Alerts) GetActive(ctx context.Context, hostID uuid.UUID, alertType string) (model.Alert, error) {
	return s.GetActiveSubject(ctx, hostID, alertType, "")
}

// GetActiveSubject, belirli bir subject (mount ya da container) hakkındaki alert için GetActive'dir.
func (s *Alerts) GetActiveSubject(ctx context.Context, hostID uuid.UUID, alertType, subject string) (model.Alert, error) {
	row := s.pool.QueryRow(ctx,
		`SELECT `+alertColumns+` FROM alerts WHERE host_id = $1 AND alert_type = $2 AND subject IS NOT DISTINCT FROM $3 AND status <> 'resolved'`,
		hostID, alertType, nullable(subject),
	)
	a, err := scanAlert(row)
	if err != nil {
		if isNoRows(err) {
			return model.Alert{}, ErrNotFound
		}
		return model.Alert{}, err
	}
	return a, nil
}

func (s *Alerts) GetByID(ctx context.Context, id uuid.UUID) (model.Alert, error) {
	row := s.pool.QueryRow(ctx, `SELECT `+alertColumns+` FROM alerts WHERE id = $1`, id)
	a, err := scanAlert(row)
	if err != nil {
		if isNoRows(err) {
			return model.Alert{}, ErrNotFound
		}
		return model.Alert{}, err
	}
	return a, nil
}

// UpdateLevel, aktif bir alert'in seviyesini (ve o andaki değer/eşiğini) günceller: uyarı → kritik gibi. reopen,
// onaylanmış bir alert'i yeniden açar (onayı siler): seviye yükselince durum ciddileşmiştir, biri yeniden sahiplenmeli.
func (s *Alerts) UpdateLevel(ctx context.Context, id uuid.UUID, level string, value, threshold *float64, reopen bool) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE alerts SET level = $2, value = $3, threshold = $4,
		        status = CASE WHEN $5 THEN 'open' ELSE status END,
		        acknowledged_at = CASE WHEN $5 THEN NULL ELSE acknowledged_at END,
		        acknowledged_by = CASE WHEN $5 THEN NULL ELSE acknowledged_by END
		 WHERE id = $1 AND status <> 'resolved'`,
		id, level, value, threshold, reopen)
	return err
}

// Resolve bir alert'i çözer ve güncel hâlini döndürür (bildirim e-postası bunun üzerinden kurulur).
// Alert zaten çözülmüşse (eşzamanlı bir çağrı önce davranmış) ErrNotFound döner — bu bir hata değil,
// çağıranın ikinci kez bildirim göndermemesi için bir işarettir.
//
// value/threshold verilirse (eşik-tabanlı çözülmede: cpu/ram/disk/docker_restart eşiğin altına
// dönünce), kaydı da bu ÇÖZÜLME anındaki okumaya günceller — yoksa alert'in son yükseltildiği
// andaki (eşiğin hâlâ üstündeki) eski değer kalır ve "Değer: %52 (eşik: %40)" gibi, gerçekte artık
// eşiğin altına inmiş bir okumayı yanlışlıkla üstündeymiş gibi gösteren bir e-postaya yol açar.
// Diğer çözülme yolları (mount/container kaybolması, host online olması) sayısal bir okuma
// taşımaz; onlar nil geçer ve son bilinen değer/eşik olduğu gibi kalır.
func (s *Alerts) Resolve(ctx context.Context, id uuid.UUID, value, threshold *float64) (model.Alert, error) {
	row := s.pool.QueryRow(ctx,
		`UPDATE alerts SET status = 'resolved', resolved_at = now(),
		        value = COALESCE($2, value), threshold = COALESCE($3, threshold)
		 WHERE id = $1 AND status <> 'resolved' RETURNING `+alertColumns,
		id, value, threshold,
	)
	a, err := scanAlert(row)
	if err != nil {
		if isNoRows(err) {
			return model.Alert{}, ErrNotFound
		}
		return model.Alert{}, err
	}
	return a, nil
}

// ResolveActiveByHostAndMetric, host yeniden rapor verdiğinde host_offline alert'ini (onaylanmış olsa da)
// kendiliğinden çözmek için kullanılır; çözülen alert'i döndürür (ya da aktif bir şey yoksa ErrNotFound —
// bildirim gerekmediğinin işareti).
func (s *Alerts) ResolveActiveByHostAndMetric(ctx context.Context, hostID uuid.UUID, alertType string) (model.Alert, error) {
	row := s.pool.QueryRow(ctx,
		`UPDATE alerts SET status = 'resolved', resolved_at = now() WHERE host_id = $1 AND alert_type = $2 AND status <> 'resolved' RETURNING `+alertColumns,
		hostID, alertType,
	)
	a, err := scanAlert(row)
	if err != nil {
		if isNoRows(err) {
			return model.Alert{}, ErrNotFound
		}
		return model.Alert{}, err
	}
	return a, nil
}

// ListActive, bir host'ın tek bir metrik türündeki tüm aktif (açık ya da onaylanmış) alert'lerini subject'inden
// bağımsız döndürür — subject'i (mount ya da container) kaybolmuş alert'leri bulmak için kullanılır.
func (s *Alerts) ListActive(ctx context.Context, hostID uuid.UUID, alertType string) ([]model.Alert, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT `+alertColumns+` FROM alerts WHERE host_id = $1 AND alert_type = $2 AND status <> 'resolved' ORDER BY subject`,
		hostID, alertType,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanAlerts(rows)
}

// Acknowledge yalnızca şu an açık bir alert'te başarılı olur; onaylayan kullanıcı by ile kaydedilir — ErrNotFound hem "yok" hem
// "açık değil" (zaten onaylanmış/çözülmüş) durumunu kapsar; çünkü çağıranın yanıtı iki durumda
// da aynı görünmeli.
func (s *Alerts) Acknowledge(ctx context.Context, id, by uuid.UUID) (model.Alert, error) {
	row := s.pool.QueryRow(ctx,
		`UPDATE alerts SET status = 'acknowledged', acknowledged_at = now(), acknowledged_by = $2 WHERE id = $1 AND status = 'open' RETURNING `+alertColumns,
		id, by,
	)
	a, err := scanAlert(row)
	if err != nil {
		if isNoRows(err) {
			return model.Alert{}, ErrNotFound
		}
		return model.Alert{}, err
	}
	return a, nil
}

// alertColumnsJoined, List/ListForHosts'ın hosts'a JOIN yaptığı sorgularda kullanılır
// (arama title'ı da kapsar); alertColumns tablo öneki olmadan alias'sız sorgularda kalır.
const alertColumnsJoined = `a.id, a.host_id, a.alert_type, COALESCE(a.subject, ''), a.level, a.status, a.value, a.threshold, a.created_at, a.acknowledged_at, a.acknowledged_by, a.resolved_at`

// List, isteğe bağlı bir durum, bir arama (ListParams.Search — subject/alert_type/title'da
// alt dize) ve sayfalama ile alert'leri döndürür. total, filtreye uyan toplam satır sayısıdır;
// Limit==0 ise tüm satırlar döner ve total = len(sonuç).
func (s *Alerts) List(ctx context.Context, status string, p ListParams) ([]model.Alert, int, error) {
	return s.listWhere(ctx, "", nil, status, p)
}

// ListForHosts ayrıca hostIDs ile kısıtlar — org_admin/operator kapsamı.
func (s *Alerts) ListForHosts(ctx context.Context, status string, hostIDs []uuid.UUID, p ListParams) ([]model.Alert, int, error) {
	if len(hostIDs) == 0 {
		return []model.Alert{}, 0, nil
	}
	return s.listWhere(ctx, "a.host_id = ANY($%d)", []any{hostIDs}, status, p)
}

// listWhere, List ve ListForHosts'ın paylaştığı sorgu kurucusudur. scopeClause verilirse (bir
// tane %d yer tutucusuyla) scopeArgs[0] ile birlikte WHERE'e eklenir.
func (s *Alerts) listWhere(ctx context.Context, scopeClause string, scopeArgs []any, status string, p ListParams) ([]model.Alert, int, error) {
	var conds []string
	var args []any
	if scopeClause != "" {
		args = append(args, scopeArgs...)
		conds = append(conds, fmt.Sprintf(scopeClause, len(args)))
	}
	if status != "" {
		args = append(args, status)
		conds = append(conds, fmt.Sprintf("a.status = $%d", len(args)))
	}
	if p.Search != "" {
		args = append(args, "%"+p.Search+"%")
		n := len(args)
		conds = append(conds, fmt.Sprintf("(a.subject ILIKE $%d OR a.alert_type ILIKE $%d OR c.title ILIKE $%d)", n, n, n))
	}
	where := ""
	if len(conds) > 0 {
		where = "WHERE " + strings.Join(conds, " AND ")
	}
	from := `FROM alerts a JOIN hosts c ON c.id = a.host_id ` + where

	var total int
	if err := s.pool.QueryRow(ctx, `SELECT count(*) `+from, args...).Scan(&total); err != nil {
		return nil, 0, err
	}

	query := `SELECT ` + alertColumnsJoined + ` ` + from + ` ORDER BY a.created_at DESC`
	if p.Limit > 0 {
		args = append(args, p.Limit, p.Offset)
		query += fmt.Sprintf(" LIMIT $%d OFFSET $%d", len(args)-1, len(args))
	}
	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	alerts, err := scanAlerts(rows)
	return alerts, total, err
}

// CountOpenByLevel dashboard özetini besler — Hosts.CountByStatus ile aynı
// ids==nil-kapsamsız-demektir kuralı.
func (s *Alerts) CountOpenByLevel(ctx context.Context, ids []uuid.UUID) (critical, warning int, err error) {
	query := `SELECT level, count(*) FROM alerts WHERE status = 'open' GROUP BY level`
	args := []any{}
	if ids != nil {
		query = `SELECT level, count(*) FROM alerts WHERE status = 'open' AND host_id = ANY($1) GROUP BY level`
		args = append(args, ids)
	}

	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return 0, 0, err
	}
	defer rows.Close()

	for rows.Next() {
		var level string
		var count int
		if err := rows.Scan(&level, &count); err != nil {
			return 0, 0, err
		}
		switch level {
		case "critical":
			critical = count
		case "warning":
			warning = count
		}
	}
	return critical, warning, rows.Err()
}

func scanAlerts(rows interface {
	Next() bool
	Scan(...any) error
	Err() error
}) ([]model.Alert, error) {
	alerts := []model.Alert{}
	for rows.Next() {
		a, err := scanAlert(rows)
		if err != nil {
			return nil, err
		}
		alerts = append(alerts, a)
	}
	return alerts, rows.Err()
}
