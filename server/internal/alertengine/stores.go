package alertengine

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"healthbeat-server/internal/model"
	"healthbeat-server/internal/store"
)

// Motorun veritabanından kullandığı yöntemler; store tipleri bunları karşılar, DB'siz testler sahtelerini verir.

// AlertStore, alert yaşam döngüsüdür (store.Alerts). "Aktif" açık ya da onaylanmış demektir.
type AlertStore interface {
	GetActive(ctx context.Context, hostID uuid.UUID, alertType string) (model.Alert, error)
	GetActiveSubject(ctx context.Context, hostID uuid.UUID, alertType, subject string) (model.Alert, error)
	ListActiveForHost(ctx context.Context, hostID uuid.UUID) ([]model.Alert, error)
	CreateIfNoneActive(ctx context.Context, hostID uuid.UUID, alertType, subject, level string, value, threshold *float64) (model.Alert, bool, error)
	UpdateLevel(ctx context.Context, id uuid.UUID, level string, value, threshold *float64, reopen bool) error
	Resolve(ctx context.Context, id uuid.UUID, value, threshold *float64) (model.Alert, error)
	ResolveActiveByHostAndMetric(ctx context.Context, hostID uuid.UUID, alertType string) (model.Alert, error)
}

// ThresholdStore, eşik ve durum kuralı çözümlemesidir (store.Thresholds; en özel olan kazanır, bkz. docs/MIMARI.md
// bölüm 8).
type ThresholdStore interface {
	ResolveHost(ctx context.Context, hostID, orgID uuid.UUID) (store.HostThresholds, error)
	ResolveStatusRules(ctx context.Context, hostID, orgID uuid.UUID) (model.StatusRuleSet, error)
}

// PendingStore, süre koşulu dolmamış alert koşullarıdır (store.Alerts, alert_pending tablosu). Bildirim doğurmadığı
// için transaction dışıdır.
type PendingStore interface {
	ListPending(ctx context.Context, hostID uuid.UUID) ([]store.PendingCondition, error)
	MarkPending(ctx context.Context, hostID uuid.UUID, alertType, subject, level string, at time.Time) (time.Time, error)
	ClearPending(ctx context.Context, hostID uuid.UUID, alertType, subject string) error
}

// HostStore, sunucunun disk alert seçimi ve bildirim metnindeki bilgileridir (store.Hosts).
type HostStore interface {
	DiskAlertMounts(ctx context.Context, id uuid.UUID) (allMounts bool, mounts []string, err error)
	GetByID(ctx context.Context, id uuid.UUID) (model.Host, error)
}

// MetricStore, kaybolan mount denetiminin rapor geçmişidir (store.Metrics).
type MetricStore interface {
	RecentReportedMounts(ctx context.Context, hostID uuid.UUID, n int) ([]map[string]struct{}, error)
}

// OrgStore, bildirim metnindeki organizasyon adıdır (store.Organizations).
type OrgStore interface {
	GetByID(ctx context.Context, id uuid.UUID) (model.Organization, error)
}

// RecipientStore, bir alert'in alıcılarını bildirim kurallarından çözer (store.Notifications).
type RecipientStore interface {
	ResolveRecipients(ctx context.Context, hostID, orgID uuid.UUID, level string) ([]store.Recipient, error)
}

// OutboxWriter, bildirimi kuyruğa yazar (store.Outbox).
type OutboxWriter interface {
	Enqueue(ctx context.Context, m store.OutboxMessage) (uuid.UUID, error)
}

// Tx, tek bir transaction'daki depolardır: alert değişikliği ve bildirim satırları birlikte commit edilir; transaction
// yarıda kalırsa (ör. süreç düşerse) ikisi de geri alınır.
type Tx interface {
	Alerts() AlertStore
	Outbox() OutboxWriter
	// Savepoint, fn'i transaction içinde bir kayıt noktasında çalıştırır: fn hata döndürürse yalnızca fn'in yazdıkları
	// geri alınır, transaction sürer. Bildirim yazılamasa da alert değişikliğinin kaydedilmesi için.
	Savepoint(ctx context.Context, fn func(Tx) error) error
}

// TxRunner, fn'i bir transaction'da çalıştırır; fn hata döndürürse geri alır.
type TxRunner interface {
	InTx(ctx context.Context, fn func(Tx) error) error
}

// Stores, motorun bütün depolarıdır. Alerts transaction dışı okumalar ve bildirim doğurmayan güncellemeler içindir;
// bildirim doğuran her değişiklik Tx üzerinden yapılır.
type Stores struct {
	Alerts        AlertStore
	Pending       PendingStore
	Thresholds    ThresholdStore
	Hosts         HostStore
	Metrics       MetricStore
	Organizations OrgStore
	Recipients    RecipientStore
	Tx            TxRunner
}

// pgTx, TxRunner'ın PostgreSQL uygulamasıdır.
type pgTx struct {
	pool   *pgxpool.Pool
	alerts *store.Alerts
	outbox *store.Outbox
}

type pgTxStores struct {
	tx     pgx.Tx
	alerts *store.Alerts
	outbox *store.Outbox
}

func (t pgTxStores) Alerts() AlertStore   { return t.alerts }
func (t pgTxStores) Outbox() OutboxWriter { return t.outbox }

// Savepoint, pgx'in iç içe transaction'ını (SAVEPOINT / ROLLBACK TO SAVEPOINT) kullanır.
func (t pgTxStores) Savepoint(ctx context.Context, fn func(Tx) error) error {
	sp, err := t.tx.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = sp.Rollback(ctx) }() // commit'ten sonra etkisizdir
	if err := fn(pgTxStores{tx: sp, alerts: t.alerts.WithTx(sp), outbox: t.outbox.WithTx(sp)}); err != nil {
		return err
	}
	return sp.Commit(ctx)
}

func (p pgTx) InTx(ctx context.Context, fn func(Tx) error) error {
	return store.InTx(ctx, p.pool, func(tx pgx.Tx) error {
		return fn(pgTxStores{tx: tx, alerts: p.alerts.WithTx(tx), outbox: p.outbox.WithTx(tx)})
	})
}
