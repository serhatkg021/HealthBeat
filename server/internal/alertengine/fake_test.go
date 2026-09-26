package alertengine

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"healthbeat-server/internal/model"
	"healthbeat-server/internal/notify"
	"healthbeat-server/internal/store"
)

// Veritabanı ve SMTP olmadan motor: bellek içi depolar ve gönderileni kaydeden kanallar.

type fakeAlerts struct {
	mu     sync.Mutex
	alerts []*model.Alert
}

func (f *fakeAlerts) active(hostID uuid.UUID, alertType, subject string, anySubject bool) *model.Alert {
	for _, a := range f.alerts {
		if a.HostID == hostID && a.AlertType == alertType && (anySubject || a.Subject == subject) &&
			(a.Status == model.AlertStatusOpen || a.Status == model.AlertStatusAcknowledged) {
			return a
		}
	}
	return nil
}

func (f *fakeAlerts) GetActive(_ context.Context, hostID uuid.UUID, alertType string) (model.Alert, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if a := f.active(hostID, alertType, "", true); a != nil {
		return *a, nil
	}
	return model.Alert{}, store.ErrNotFound
}

func (f *fakeAlerts) GetActiveSubject(_ context.Context, hostID uuid.UUID, alertType, subject string) (model.Alert, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if a := f.active(hostID, alertType, subject, false); a != nil {
		return *a, nil
	}
	return model.Alert{}, store.ErrNotFound
}

func (f *fakeAlerts) ListActive(_ context.Context, hostID uuid.UUID, alertType string) ([]model.Alert, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []model.Alert
	for _, a := range f.alerts {
		if a.HostID == hostID && a.AlertType == alertType && a.Status != model.AlertStatusResolved {
			out = append(out, *a)
		}
	}
	return out, nil
}

func (f *fakeAlerts) CreateIfNoneActive(_ context.Context, hostID uuid.UUID, alertType, subject, level string, value, threshold *float64) (model.Alert, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if a := f.active(hostID, alertType, subject, false); a != nil {
		return *a, false, nil
	}
	a := &model.Alert{ID: uuid.New(), HostID: hostID, AlertType: alertType, Subject: subject, Level: level,
		Status: model.AlertStatusOpen, Value: value, Threshold: threshold, CreatedAt: time.Now()}
	f.alerts = append(f.alerts, a)
	return *a, true, nil
}

func (f *fakeAlerts) byID(id uuid.UUID) *model.Alert {
	for _, a := range f.alerts {
		if a.ID == id {
			return a
		}
	}
	return nil
}

func (f *fakeAlerts) UpdateLevel(_ context.Context, id uuid.UUID, level string, value, threshold *float64, reopen bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	a := f.byID(id)
	a.Level, a.Value, a.Threshold = level, value, threshold
	if reopen {
		a.Status, a.AcknowledgedAt, a.AcknowledgedBy = model.AlertStatusOpen, nil, nil
	}
	return nil
}

func (f *fakeAlerts) Resolve(_ context.Context, id uuid.UUID, value, threshold *float64) (model.Alert, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	a := f.byID(id)
	if a == nil || a.Status == model.AlertStatusResolved {
		return model.Alert{}, store.ErrNotFound
	}
	now := time.Now()
	a.Status, a.ResolvedAt = model.AlertStatusResolved, &now
	if value != nil {
		a.Value, a.Threshold = value, threshold
	}
	return *a, nil
}

func (f *fakeAlerts) ResolveActiveByHostAndMetric(ctx context.Context, hostID uuid.UUID, alertType string) (model.Alert, error) {
	a, err := f.GetActive(ctx, hostID, alertType)
	if err != nil {
		return a, err
	}
	return f.Resolve(ctx, a.ID, nil, nil)
}

// fakeThresholds yalnızca host geneli eşikleri bilir (mount/container başına eşik yok).
type fakeThresholds map[string]model.ThresholdConfig

func (f fakeThresholds) Resolve(_ context.Context, _, _ uuid.UUID, metricType string) (model.ThresholdConfig, bool, error) {
	t, ok := f[metricType]
	return t, ok, nil
}

func (f fakeThresholds) ResolveSubjects(_ context.Context, _, _ uuid.UUID, metricType string) (store.SubjectThresholds, error) {
	if t, ok := f[metricType]; ok {
		return store.SubjectThresholds{Base: &t}, nil
	}
	return store.SubjectThresholds{}, nil
}

func (f fakeThresholds) HostSubjectOverrides(context.Context, uuid.UUID, string) (map[string]model.ThresholdLevels, error) {
	return nil, nil
}

type fakeHosts struct {
	host    model.Host
	lookups int
}

func (f *fakeHosts) DiskAlertMounts(context.Context, uuid.UUID) (bool, []string, error) {
	return true, nil, nil
}

func (f *fakeHosts) GetByID(context.Context, uuid.UUID) (model.Host, error) {
	f.lookups++
	return f.host, nil
}

type fakeMetrics struct{}

func (fakeMetrics) RecentReportedMounts(context.Context, uuid.UUID, int) ([]map[string]struct{}, error) {
	return nil, nil
}

type fakeOrgs struct{ org model.Organization }

func (f fakeOrgs) GetByID(context.Context, uuid.UUID) (model.Organization, error) { return f.org, nil }

type fakeRecipients []store.Recipient

func (f fakeRecipients) ResolveRecipients(context.Context, uuid.UUID, uuid.UUID, string) ([]store.Recipient, error) {
	return f, nil
}

// fakeNotifier, kendisine verilen bildirimleri kaydeder.
type fakeNotifier struct {
	channel string
	mu      sync.Mutex
	sent    []sentMessage
}

type sentMessage struct {
	to  []string
	msg notify.Message
}

func (f *fakeNotifier) Channel() string { return f.channel }

func (f *fakeNotifier) Send(_ context.Context, to []string, msg notify.Message) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sent = append(f.sent, sentMessage{to: to, msg: msg})
	return nil
}

func (f *fakeNotifier) subjects() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var s []string
	for _, m := range f.sent {
		s = append(s, m.msg.Subject)
	}
	return s
}

type fakeEnv struct {
	engine *Engine
	alerts *fakeAlerts
	hosts  *fakeHosts
	email  *fakeNotifier
	sms    *fakeNotifier
	host   uuid.UUID
	org    uuid.UUID
}

func newFakeEnv(t *testing.T, recipients fakeRecipients) *fakeEnv {
	t.Helper()
	f := &fakeEnv{
		alerts: &fakeAlerts{},
		host:   uuid.New(),
		org:    uuid.New(),
		email:  &fakeNotifier{channel: model.ChannelEmail},
		sms:    &fakeNotifier{channel: model.ChannelSMS},
	}
	f.hosts = &fakeHosts{host: model.Host{ID: f.host, OrganizationID: f.org, Title: "web-1", IP: "10.0.0.5"}}
	f.engine = newEngineWith(Stores{
		Alerts:        f.alerts,
		Thresholds:    fakeThresholds{model.MetricTypeCPU: {MetricType: model.MetricTypeCPU, WarningLevel: 70, CriticalLevel: 90}},
		Hosts:         f.hosts,
		Metrics:       fakeMetrics{},
		Organizations: fakeOrgs{org: model.Organization{ID: f.org, Name: "Acme"}},
		Recipients:    recipients,
	}, []notify.Notifier{f.email, f.sms}, "https://panel.example.com/", 16, 1)
	t.Cleanup(func() { _ = f.engine.Close(context.Background()) })
	return f
}

func (f *fakeEnv) report(cpu float64) {
	f.engine.EvaluateMetrics(context.Background(), f.host, f.org, cpu, 10, nil)
	f.engine.Flush()
}

// Açılma, yükselme, düşme ve çözülme her biri bir bildirimdir; aynı seviyede kalmak bildirim üretmez.
func TestFakeEngineNotifiesOnLifecycleChanges(t *testing.T) {
	f := newFakeEnv(t, fakeRecipients{{Channel: model.ChannelEmail, Address: "ops@acme.test", Name: "Ops"}})

	for _, cpu := range []float64{50, 75, 80, 95, 96, 72, 40, 30} {
		f.report(cpu)
	}
	got := f.email.subjects()
	wantLevels := []string{"UYARI", "KRİTİK", "UYARI", "ÇÖZÜLDÜ"}
	if len(got) != len(wantLevels) {
		t.Fatalf("subjects = %q, want %d notifications (%v)", got, len(wantLevels), wantLevels)
	}
	for i, level := range wantLevels {
		if want := "[HealthBeat] -- " + level + " / Acme / web-1(10.0.0.5) - "; !strings.HasPrefix(got[i], want) {
			t.Errorf("subject %d = %q, want prefix %q", i, got[i], want)
		}
	}
}

// Alıcılar kanala göre gruplanır ve her grup kendi kanalına gider; kanalı olmayan alıcı atlanır.
func TestFakeEngineFansOutByChannel(t *testing.T) {
	f := newFakeEnv(t, fakeRecipients{
		{Channel: model.ChannelEmail, Address: "a@acme.test", Name: "A"},
		{Channel: model.ChannelSlack, Address: "#ops", Name: "Slack"},
		{Channel: model.ChannelSMS, Address: "+905550000000", Name: "Nöbetçi"},
		{Channel: model.ChannelEmail, Address: "b@acme.test", Name: "B"},
	})
	f.report(95)

	if len(f.email.sent) != 1 || strings.Join(f.email.sent[0].to, ",") != "a@acme.test,b@acme.test" {
		t.Fatalf("email = %+v, want one message to both addresses", f.email.sent)
	}
	if len(f.sms.sent) != 1 || strings.Join(f.sms.sent[0].to, ",") != "+905550000000" {
		t.Fatalf("sms = %+v, want one message to the phone number", f.sms.sent)
	}
	if f.email.sent[0].msg != f.sms.sent[0].msg {
		t.Fatal("channels received different messages for the same alert")
	}
}

// Teslim edilebilir alıcı yoksa bildirim metni hiç kurulmaz (sunucu/organizasyon okunmaz).
func TestFakeEngineSkipsLookupsWithoutRecipients(t *testing.T) {
	f := newFakeEnv(t, fakeRecipients{{Channel: model.ChannelSlack, Address: "#ops", Name: "Slack"}})
	f.report(95)
	if len(f.email.sent)+len(f.sms.sent) != 0 || f.hosts.lookups != 0 {
		t.Fatalf("sent email=%d sms=%d, host lookups=%d; want nothing", len(f.email.sent), len(f.sms.sent), f.hosts.lookups)
	}
	if len(f.alerts.alerts) != 1 {
		t.Fatalf("alerts = %d, want the alert itself to be recorded", len(f.alerts.alerts))
	}
}
