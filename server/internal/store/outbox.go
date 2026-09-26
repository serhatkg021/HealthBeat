package store

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"healthbeat-server/internal/model"
	"healthbeat-server/internal/secretbox"
)

// Bildirim kuyruğundaki satır türleri (notification_outbox.kind).
const (
	OutboxKindAlert           = "alert"
	OutboxKindPasswordReset   = "password_reset"
	OutboxKindPasswordChanged = "password_changed"
)

// Alert bildirimini doğuran olaylar (notification_outbox.alert_event).
const (
	AlertEventOpened       = "opened"
	AlertEventLevelChanged = "level_changed"
	AlertEventResolved     = "resolved"
)

// OutboxMessage, kuyruğa yazılacak tek bir kanaldaki bildirimdir.
type OutboxMessage struct {
	Kind       string
	Channel    string
	Recipients []string
	Subject    string
	Body       string
	// Seal, gövdenin şifreli saklanmasını ister (ör. şifre sıfırlama bağlantısı): veritabanında düz hâli bulunmaz ve
	// satır bitince (gönderildi ya da vazgeçildi) şifreli hâli de silinir.
	Seal    bool
	AlertID *uuid.UUID
	// AlertEvent ve AlertLevel, alert bildiriminde (Kind = OutboxKindAlert) zorunludur: hangi olay için, hangi seviyede
	// gittiği (alert detayındaki bildirim geçmişi için).
	AlertEvent string
	AlertLevel string
	RequestID  string
	// ExpiresAt verilirse bu andan sonra gönderilmez.
	ExpiresAt *time.Time
}

// OutboxItem, işçinin teslim için aldığı satırdır; Body çözülmüş hâldedir.
type OutboxItem struct {
	ID         uuid.UUID
	Kind       string
	Channel    string
	Recipients []string
	Subject    string
	Body       string
	AlertID    *uuid.UUID
	RequestID  string
	Attempts   int // bu deneme dahil
	// OpenErr, şifreli gövde çözülemediyse doludur (yanlış anahtar ya da bozuk veri): satır gönderilemez.
	OpenErr error
}

// Outbox, bildirim kuyruğudur (notification_outbox; bkz. internal/outbox işçisi).
type Outbox struct {
	db  DB
	box *secretbox.Box // yalnızca Seal'li satırlar için; nil ise böyle satır yazılamaz/okunamaz
}

func NewOutbox(pool *pgxpool.Pool, box *secretbox.Box) *Outbox {
	return &Outbox{db: pool, box: box}
}

// WithTx, kuyruğa tx içinde yazan bir Outbox döndürür: bildirim, onu doğuran değişiklikle birlikte commit edilir.
func (s *Outbox) WithTx(tx pgx.Tx) *Outbox { return &Outbox{db: tx, box: s.box} }

var errNoSecretBox = errors.New("outbox: sealed message without a secret box")

// Enqueue, m'yi hemen teslim edilecek şekilde kuyruğa yazar.
func (s *Outbox) Enqueue(ctx context.Context, m OutboxMessage) (uuid.UUID, error) {
	id := uuid.New()
	var body, sealed *string
	if m.Seal {
		if s.box == nil {
			return uuid.Nil, errNoSecretBox
		}
		enc, err := s.box.Seal(m.Body, outboxAAD(id))
		if err != nil {
			return uuid.Nil, err
		}
		sealed = &enc
	} else {
		body = &m.Body
	}
	_, err := s.db.Exec(ctx,
		`INSERT INTO notification_outbox (id, kind, channel, recipients, subject, body, body_sealed, alert_id, alert_event,
		                                  alert_level, request_id, expires_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)`,
		id, m.Kind, m.Channel, m.Recipients, m.Subject, body, sealed, m.AlertID, nullIfEmpty(m.AlertEvent),
		nullIfEmpty(m.AlertLevel), nullIfEmpty(m.RequestID), m.ExpiresAt)
	if err != nil {
		return uuid.Nil, err
	}
	return id, nil
}

// outboxAAD, şifreli gövdeyi satırına bağlar: başka bir satıra kopyalanan şifreli gövde çözülmez.
func outboxAAD(id uuid.UUID) string { return "notification_outbox.body:" + id.String() }

// Claim, kinds türündeki teslim zamanı gelmiş en çok limit satırı alır: deneme sayısını artırır ve next_attempt_at'i
// lease kadar ileri atar (kira), sonra hemen commit eder. Gönderim transaction dışında yapılır; süreç gönderim
// ortasında düşerse satır kira bitince yeniden alınır (en az bir kez teslim). FOR UPDATE SKIP LOCKED sayesinde birden
// çok işçi (ya da server kopyası) aynı satırı almaz. Süresi dolmuş satırlar alınmaz (bkz. Expire).
func (s *Outbox) Claim(ctx context.Context, kinds []string, limit int, lease time.Duration) ([]OutboxItem, error) {
	rows, err := s.db.Query(ctx,
		`UPDATE notification_outbox o
		 SET attempts = o.attempts + 1, next_attempt_at = now() + make_interval(secs => $3)
		 WHERE o.id IN (
		     SELECT id FROM notification_outbox
		     WHERE sent_at IS NULL AND failed_at IS NULL AND next_attempt_at <= now()
		       AND kind = ANY($1) AND (expires_at IS NULL OR expires_at > now())
		     ORDER BY next_attempt_at
		     LIMIT $2
		     FOR UPDATE SKIP LOCKED)
		 RETURNING o.id, o.kind, o.channel, o.recipients, o.subject, o.body, o.body_sealed, o.alert_id,
		           COALESCE(o.request_id, ''), o.attempts`,
		kinds, limit, lease.Seconds())
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []OutboxItem
	for rows.Next() {
		var it OutboxItem
		var body, sealed *string
		if err := rows.Scan(&it.ID, &it.Kind, &it.Channel, &it.Recipients, &it.Subject, &body, &sealed, &it.AlertID,
			&it.RequestID, &it.Attempts); err != nil {
			return nil, err
		}
		switch {
		case sealed != nil && s.box == nil:
			it.OpenErr = errNoSecretBox
		case sealed != nil:
			it.Body, it.OpenErr = s.box.Open(*sealed, outboxAAD(it.ID))
		case body != nil:
			it.Body = *body
		}
		items = append(items, it)
	}
	return items, rows.Err()
}

// MarkSent, teslim edilen satırı kapatır; şifreli gövde silinir.
func (s *Outbox) MarkSent(ctx context.Context, id uuid.UUID) error {
	_, err := s.db.Exec(ctx,
		`UPDATE notification_outbox SET sent_at = now(), body_sealed = NULL, last_error = NULL WHERE id = $1`, id)
	return err
}

// MarkRetry, başarısız denemeyi kaydeder ve satırı next anında yeniden denenmek üzere bırakır.
func (s *Outbox) MarkRetry(ctx context.Context, id uuid.UUID, next time.Time, errMsg string) error {
	_, err := s.db.Exec(ctx,
		`UPDATE notification_outbox SET next_attempt_at = $2, last_error = $3 WHERE id = $1`, id, next, errMsg)
	return err
}

// MarkFailed, satırdan vazgeçer (deneme sınırı ya da kalıcı hata); şifreli gövde silinir.
func (s *Outbox) MarkFailed(ctx context.Context, id uuid.UUID, errMsg string) error {
	_, err := s.db.Exec(ctx,
		`UPDATE notification_outbox SET failed_at = now(), body_sealed = NULL, last_error = $2 WHERE id = $1`, id, errMsg)
	return err
}

// Expire, süresi dolmuş bekleyen satırları gönderilmeden kapatır (şifreli gövdeleri silinir).
func (s *Outbox) Expire(ctx context.Context, kinds []string) (int64, error) {
	tag, err := s.db.Exec(ctx,
		`UPDATE notification_outbox SET failed_at = now(), body_sealed = NULL, last_error = 'expired before delivery'
		 WHERE sent_at IS NULL AND failed_at IS NULL AND expires_at <= now() AND kind = ANY($1)`, kinds)
	return tag.RowsAffected(), err
}

// Supersede, recipient'a giden bekleyen kind satırlarını gönderilmeden kapatır: ör. yeni bir sıfırlama bağlantısı
// istenince (ya da şifre sıfırlanınca) eski bağlantıyı taşıyan e-posta artık gönderilmez.
func (s *Outbox) Supersede(ctx context.Context, kind, recipient string) (int64, error) {
	tag, err := s.db.Exec(ctx,
		`UPDATE notification_outbox SET failed_at = now(), body_sealed = NULL, last_error = 'superseded'
		 WHERE sent_at IS NULL AND failed_at IS NULL AND kind = $1 AND $2 = ANY(recipients)`, kind, recipient)
	return tag.RowsAffected(), err
}

// PurgeFinished, before'dan önce oluşturulmuş bitmiş (gönderilmiş ya da vazgeçilmiş) kinds satırlarını siler. Alert
// bildirimleri alert'leri durdukça silinmez (alert detayındaki bildirim geçmişi, gövdesiyle); yalnızca alert'i silinmiş
// (ör. sunucusu silinen) sahipsiz alert bildirimleri silinir.
func (s *Outbox) PurgeFinished(ctx context.Context, kinds []string, before time.Time) (int64, error) {
	tag, err := s.db.Exec(ctx,
		`DELETE FROM notification_outbox
		 WHERE (sent_at IS NOT NULL OR failed_at IS NOT NULL) AND created_at < $2 AND kind = ANY($1)
		   AND (kind <> 'alert' OR alert_id IS NULL)`, kinds, before)
	return tag.RowsAffected(), err
}

// AlertNotification, bir alert bildiriminin teslim kaydıdır (alert detayındaki bildirim geçmişi).
type AlertNotification struct {
	ID            uuid.UUID
	Event         string // AlertEvent*
	Level         string
	Channel       string
	Recipients    []string
	Subject       string
	Body          string
	Status        string // model.Notification*
	Attempts      int
	LastError     string
	CreatedAt     time.Time
	SentAt        *time.Time
	FailedAt      *time.Time
	NextAttemptAt *time.Time // yalnızca bekleyen satırda
}

// ListForAlert, alertID'nin bildirimlerini oluşturulma sırasıyla döndürür.
func (s *Outbox) ListForAlert(ctx context.Context, alertID uuid.UUID) ([]AlertNotification, error) {
	rows, err := s.db.Query(ctx,
		`SELECT id, alert_event, alert_level, channel, recipients, subject, COALESCE(body, ''), attempts,
		        COALESCE(last_error, ''), created_at, sent_at, failed_at, next_attempt_at
		 FROM notification_outbox WHERE alert_id = $1 AND kind = 'alert'
		 ORDER BY created_at, id`, alertID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AlertNotification{}
	for rows.Next() {
		var n AlertNotification
		var next time.Time
		if err := rows.Scan(&n.ID, &n.Event, &n.Level, &n.Channel, &n.Recipients, &n.Subject, &n.Body, &n.Attempts,
			&n.LastError, &n.CreatedAt, &n.SentAt, &n.FailedAt, &next); err != nil {
			return nil, err
		}
		switch {
		case n.SentAt != nil:
			n.Status = model.NotificationSent
		case n.FailedAt != nil:
			n.Status = model.NotificationFailed
		default:
			n.Status, n.NextAttemptAt = model.NotificationPending, &next
		}
		out = append(out, n)
	}
	return out, rows.Err()
}
