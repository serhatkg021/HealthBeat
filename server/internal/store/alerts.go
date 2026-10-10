package store

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"healthbeat-server/internal/model"
)

type Alerts struct {
	pool DB
}

func NewAlerts(pool *pgxpool.Pool) *Alerts {
	return &Alerts{pool: pool}
}

// WithTx, aynı sorguları tx içinde çalıştıran bir Alerts döndürür: alert değişikliği ve bildirimi (Outbox) birlikte
// commit edilsin diye.
func (s *Alerts) WithTx(tx pgx.Tx) *Alerts { return &Alerts{pool: tx} }

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
	return collect(rows, scanAlert)
}

// ListActiveForHost, host'un bütün aktif (açık ya da onaylanmış) alert'leridir, türüne bakılmaksızın; alert motoru
// rapor başına bir kez okur.
func (s *Alerts) ListActiveForHost(ctx context.Context, hostID uuid.UUID) ([]model.Alert, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT `+alertColumns+` FROM alerts WHERE host_id = $1 AND status <> 'resolved' ORDER BY alert_type, subject`, hostID)
	if err != nil {
		return nil, err
	}
	return collect(rows, scanAlert)
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

// AlertFilter, alert listesinin isteğe bağlı süzgeçleridir; boş alan "hepsi" demektir.
type AlertFilter struct {
	Status string
	Level  string
}

// List, isteğe bağlı bir durum ve seviye, bir arama (ListParams.Search — subject/alert_type/title'da
// alt dize) ve sayfalama ile alert'leri döndürür. total, filtreye uyan toplam satır sayısıdır;
// Limit==0 ise tüm satırlar döner ve total = len(sonuç).
func (s *Alerts) List(ctx context.Context, f AlertFilter, p ListParams) ([]model.Alert, int, error) {
	return s.listWhere(ctx, "", nil, f, p)
}

// ListForHosts ayrıca hostIDs ile kısıtlar — org_admin/operator kapsamı.
func (s *Alerts) ListForHosts(ctx context.Context, f AlertFilter, hostIDs []uuid.UUID, p ListParams) ([]model.Alert, int, error) {
	if len(hostIDs) == 0 {
		return []model.Alert{}, 0, nil
	}
	return s.listWhere(ctx, "a.host_id = ANY($%d)", []any{hostIDs}, f, p)
}

// listWhere, List ve ListForHosts'ın paylaştığı sorgu kurucusudur. scopeClause verilirse (bir
// tane %d yer tutucusuyla) scopeArgs[0] ile birlikte WHERE'e eklenir.
func (s *Alerts) listWhere(ctx context.Context, scopeClause string, scopeArgs []any, f AlertFilter, p ListParams) ([]model.Alert, int, error) {
	var conds []string
	var args []any
	if scopeClause != "" {
		args = append(args, scopeArgs...)
		conds = append(conds, fmt.Sprintf(scopeClause, len(args)))
	}
	if f.Status != "" {
		args = append(args, f.Status)
		conds = append(conds, fmt.Sprintf("a.status = $%d", len(args)))
	}
	if f.Level != "" {
		args = append(args, f.Level)
		conds = append(conds, fmt.Sprintf("a.level = $%d", len(args)))
	}
	if p.Search != "" {
		args = append(args, p.SearchPattern())
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

	query := `SELECT ` + alertColumnsJoined + `, ` + alertNotificationStatus + ` ` + from + ` ORDER BY a.created_at DESC`
	if p.Limit > 0 {
		args = append(args, p.Limit, p.Offset)
		query += fmt.Sprintf(" LIMIT $%d OFFSET $%d", len(args)-1, len(args))
	}
	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	alerts := []model.Alert{}
	for rows.Next() {
		var a model.Alert
		var notification *string
		if err := rows.Scan(&a.ID, &a.HostID, &a.AlertType, &a.Subject, &a.Level, &a.Status, &a.Value, &a.Threshold,
			&a.CreatedAt, &a.AcknowledgedAt, &a.AcknowledgedBy, &a.ResolvedAt, &notification); err != nil {
			return nil, 0, err
		}
		if notification != nil {
			a.NotificationStatus = *notification
		}
		alerts = append(alerts, a)
	}
	return alerts, total, rows.Err()
}

// alertNotificationStatus, alert'in bildirimlerinin toplu durumudur (bkz. model.Alert.NotificationStatus); bildirimi
// yoksa NULL.
const alertNotificationStatus = `(SELECT CASE
	    WHEN bool_or(o.failed_at IS NOT NULL) THEN 'failed'
	    WHEN bool_or(o.sent_at IS NULL) THEN 'pending'
	    WHEN count(*) > 0 THEN 'sent'
	END FROM notification_outbox o WHERE o.alert_id = a.id)`

// OpenAlertCounts, açık (onaylanmamış, çözülmemiş) alert'lerin seviye başına sayısıdır.
type OpenAlertCounts struct {
	Critical, Warning, Info int
}

// CountOpenByLevel dashboard özetini besler — Hosts.CountByStatus ile aynı
// ids==nil-kapsamsız-demektir kuralı.
func (s *Alerts) CountOpenByLevel(ctx context.Context, ids []uuid.UUID) (OpenAlertCounts, error) {
	query := `SELECT level, count(*) FROM alerts WHERE status = 'open' GROUP BY level`
	args := []any{}
	if ids != nil {
		query = `SELECT level, count(*) FROM alerts WHERE status = 'open' AND host_id = ANY($1) GROUP BY level`
		args = append(args, ids)
	}

	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return OpenAlertCounts{}, err
	}
	defer rows.Close()

	var c OpenAlertCounts
	for rows.Next() {
		var level string
		var count int
		if err := rows.Scan(&level, &count); err != nil {
			return OpenAlertCounts{}, err
		}
		switch level {
		case model.AlertLevelCritical:
			c.Critical = count
		case model.AlertLevelWarning:
			c.Warning = count
		case model.AlertLevelInfo:
			c.Info = count
		}
	}
	return c, rows.Err()
}

// PurgeResolvedBefore, cutoff'tan önce çözülmüş alert'leri parti parti siler ve kaç tane sildiğini döndürür (bkz.
// app_settings.resolved_alert_retention_days). Açık ve onaylanmış alert'lere dokunmaz. Silinen alert'lerin bildirim geçmişi sahipsiz
// kalır ve outbox temizliğiyle silinir.
func (s *Alerts) PurgeResolvedBefore(ctx context.Context, cutoff time.Time, batchSize int) (int64, error) {
	return purgeInBatches(ctx, s.pool,
		`DELETE FROM alerts WHERE id IN (
		     SELECT id FROM alerts WHERE status = 'resolved' AND resolved_at < $1 ORDER BY resolved_at LIMIT $2)`,
		cutoff, batchSize)
}

// PendingCondition, süre koşulu henüz dolmamış bir alert koşuludur (alert_pending): koşul Since'ten beri sürüyor.
type PendingCondition struct {
	AlertType string
	Subject   string // "" = sunucu geneli
	Since     time.Time
}

// ListPending, sunucunun bekleyen koşullarıdır; alert motoru rapor başına bir kez okur.
func (s *Alerts) ListPending(ctx context.Context, hostID uuid.UUID) ([]PendingCondition, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT alert_type, COALESCE(subject, ''), since FROM alert_pending WHERE host_id = $1`, hostID)
	if err != nil {
		return nil, err
	}
	return collect(rows, func(row interface{ Scan(...any) error }) (PendingCondition, error) {
		var p PendingCondition
		err := row.Scan(&p.AlertType, &p.Subject, &p.Since)
		return p, err
	})
}

// MarkPending, koşulun başladığını kaydeder ve başlangıcını döndürür: koşul zaten bekliyorsa ilk başlangıç korunur
// (yalnızca seviyesi güncellenir).
func (s *Alerts) MarkPending(ctx context.Context, hostID uuid.UUID, alertType, subject, level string, at time.Time) (time.Time, error) {
	var since time.Time
	err := s.pool.QueryRow(ctx,
		`INSERT INTO alert_pending (host_id, alert_type, subject, level, since) VALUES ($1, $2, NULLIF($3, ''), $4, $5)
		 ON CONFLICT ON CONSTRAINT alert_pending_key DO UPDATE SET level = EXCLUDED.level
		 RETURNING since`, hostID, alertType, subject, level, at).Scan(&since)
	return since, err
}

// ClearPending, koşulun bekleme kaydını siler (koşul kalktı ya da alert açıldı).
func (s *Alerts) ClearPending(ctx context.Context, hostID uuid.UUID, alertType, subject string) error {
	_, err := s.pool.Exec(ctx,
		`DELETE FROM alert_pending WHERE host_id = $1 AND alert_type = $2 AND subject IS NOT DISTINCT FROM NULLIF($3, '')`,
		hostID, alertType, subject)
	return err
}
