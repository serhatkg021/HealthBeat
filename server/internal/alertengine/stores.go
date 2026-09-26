package alertengine

import (
	"context"

	"github.com/google/uuid"

	"healthbeat-server/internal/model"
	"healthbeat-server/internal/store"
)

// Motorun veritabanından kullandığı yöntemler; store tipleri bunları karşılar, DB'siz testler sahtelerini verir.

// AlertStore, alert yaşam döngüsüdür (store.Alerts). "Aktif" açık ya da onaylanmış demektir.
type AlertStore interface {
	GetActive(ctx context.Context, hostID uuid.UUID, alertType string) (model.Alert, error)
	GetActiveSubject(ctx context.Context, hostID uuid.UUID, alertType, subject string) (model.Alert, error)
	ListActive(ctx context.Context, hostID uuid.UUID, alertType string) ([]model.Alert, error)
	CreateIfNoneActive(ctx context.Context, hostID uuid.UUID, alertType, subject, level string, value, threshold *float64) (model.Alert, bool, error)
	UpdateLevel(ctx context.Context, id uuid.UUID, level string, value, threshold *float64, reopen bool) error
	Resolve(ctx context.Context, id uuid.UUID, value, threshold *float64) (model.Alert, error)
	ResolveActiveByHostAndMetric(ctx context.Context, hostID uuid.UUID, alertType string) (model.Alert, error)
}

// ThresholdStore, eşik çözümlemesidir (store.Thresholds; en özel olan kazanır, bkz. docs/MIMARI.md bölüm 8).
type ThresholdStore interface {
	Resolve(ctx context.Context, hostID, orgID uuid.UUID, metricType string) (model.ThresholdConfig, bool, error)
	ResolveSubjects(ctx context.Context, hostID, orgID uuid.UUID, metricType string) (store.SubjectThresholds, error)
	HostSubjectOverrides(ctx context.Context, hostID uuid.UUID, metricType string) (map[string]model.ThresholdLevels, error)
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

// Stores, motorun bütün depolarıdır.
type Stores struct {
	Alerts        AlertStore
	Thresholds    ThresholdStore
	Hosts         HostStore
	Metrics       MetricStore
	Organizations OrgStore
	Recipients    RecipientStore
}
