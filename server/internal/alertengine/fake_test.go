package alertengine

import (
	"context"
	"errors"
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

func (f *fakeAlerts) ListActiveForHost(_ context.Context, hostID uuid.UUID) ([]model.Alert, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []model.Alert
	for _, a := range f.alerts {
		if a.HostID == hostID && a.Status != model.AlertStatusResolved {
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

func (f *fakeAlerts) GetByID(_ context.Context, id uuid.UUID) (model.Alert, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if a := f.byID(id); a != nil {
		return *a, nil
	}
	return model.Alert{}, store.ErrNotFound
}

func (f *fakeAlerts) SetNotifyPending(_ context.Context, id uuid.UUID, pending bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if a := f.byID(id); a != nil {
		a.NotifyPending = pending
	}
	return nil
}

func (f *fakeAlerts) ListNotifyPending(context.Context) ([]model.Alert, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []model.Alert
	for _, a := range f.alerts {
		if a.NotifyPending && a.Status != model.AlertStatusResolved {
			out = append(out, *a)
		}
	}
	return out, nil
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

func (f fakeThresholds) ResolveHost(context.Context, uuid.UUID, uuid.UUID) (store.HostThresholds, error) {
	out := store.HostThresholds{}
	for metricType, t := range f {
		t := t
		out[metricType] = store.SubjectThresholds{Base: &t}
	}
	return out, nil
}

func (f fakeThresholds) ResolveStatusRules(context.Context, uuid.UUID, uuid.UUID) (model.StatusRuleSet, error) {
	return model.StatusRuleSet{}, nil
}

// fakePending, alert_pending'in bellek içi karşılığıdır.
type fakePending struct {
	mu    sync.Mutex
	since map[[2]string]time.Time
	marks int
}

func (f *fakePending) ListPending(context.Context, uuid.UUID) ([]store.PendingCondition, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []store.PendingCondition
	for k, t := range f.since {
		out = append(out, store.PendingCondition{AlertType: k[0], Subject: k[1], Since: t})
	}
	return out, nil
}

func (f *fakePending) MarkPending(_ context.Context, _ uuid.UUID, alertType, subject, _ string, at time.Time) (time.Time, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.marks++
	if f.since == nil {
		f.since = map[[2]string]time.Time{}
	}
	k := [2]string{alertType, subject}
	if t, ok := f.since[k]; ok {
		return t, nil
	}
	f.since[k] = at
	return at, nil
}

func (f *fakePending) ClearPending(_ context.Context, _ uuid.UUID, alertType, subject string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.since, [2]string{alertType, subject})
	return nil
}

// failingPending, bekleme kaydı yazılamayan bir veritabanıdır.
type failingPending struct{ fakePending }

func (*failingPending) MarkPending(context.Context, uuid.UUID, string, string, string, time.Time) (time.Time, error) {
	return time.Time{}, errors.New("db down")
}

type fakeHosts struct {
	host     model.Host
	lookups  int
	services []model.HostService
}

func (f *fakeHosts) Services(context.Context, uuid.UUID) ([]model.HostService, error) {
	return f.services, nil
}

func (f *fakeHosts) DiskAlertMounts(context.Context, uuid.UUID) (bool, []string, error) {
	return true, nil, nil
}

func (f *fakeHosts) GetByID(context.Context, uuid.UUID) (model.Host, error) {
	f.lookups++
	return f.host, nil
}

type fakeMetrics struct{ previous *[]model.DiskUsage }

func (fakeMetrics) RecentReportedMounts(context.Context, uuid.UUID, int) ([]map[string]struct{}, error) {
	return nil, nil
}

func (f fakeMetrics) PreviousDisks(context.Context, uuid.UUID) ([]model.DiskUsage, error) {
	if f.previous == nil {
		return nil, nil
	}
	return *f.previous, nil
}

type fakeOrgs struct{ org model.Organization }

func (f fakeOrgs) GetByID(context.Context, uuid.UUID) (model.Organization, error) { return f.org, nil }

type fakeRecipients []store.Recipient

func (f fakeRecipients) ResolveRecipients(context.Context, uuid.UUID, uuid.UUID, string) ([]store.Recipient, error) {
	return f, nil
}

// fakeNotifier yalnızca kanalın var olduğunu bildirir; teslimi outbox.Worker yapar (bkz. internal/outbox testleri).
type fakeNotifier struct{ channel string }

func (f *fakeNotifier) Channel() string { return f.channel }

func (f *fakeNotifier) Personal() bool { return true }

func (f *fakeNotifier) Send(context.Context, string, notify.Message) error { return nil }

// fakeOutbox, kuyruğa yazılanları kaydeder; fail doluysa yazma o hatayla başarısız olur.
type fakeOutbox struct {
	rows []store.OutboxMessage
	fail error
}

func (f *fakeOutbox) Enqueue(_ context.Context, m store.OutboxMessage) (uuid.UUID, error) {
	if f.fail != nil {
		return uuid.Nil, f.fail
	}
	f.rows = append(f.rows, m)
	return uuid.New(), nil
}

func (f *fakeOutbox) subjects() []string {
	var s []string
	for _, m := range f.rows {
		s = append(s, m.Subject)
	}
	return s
}

// fakeTx, fn hata döndürürse alert'lerdeki ve kuyruktaki değişiklikleri geri alır (transaction gibi).
type fakeTx struct {
	alerts *fakeAlerts
	outbox *fakeOutbox
}

func (t fakeTx) Alerts() AlertStore   { return t.alerts }
func (t fakeTx) Outbox() OutboxWriter { return t.outbox }

// Savepoint yalnızca kuyruk yazımlarını geri alır (motor kayıt noktasında yalnızca bildirim yazar).
func (t fakeTx) Savepoint(_ context.Context, fn func(Tx) error) error {
	rows := len(t.outbox.rows)
	if err := fn(t); err != nil {
		t.outbox.rows = t.outbox.rows[:rows]
		return err
	}
	return nil
}

func (t fakeTx) InTx(_ context.Context, fn func(Tx) error) error {
	t.alerts.mu.Lock()
	saved := make([]model.Alert, len(t.alerts.alerts))
	for i, a := range t.alerts.alerts {
		saved[i] = *a
	}
	t.alerts.mu.Unlock()
	rows := len(t.outbox.rows)
	if err := fn(t); err != nil {
		t.alerts.mu.Lock()
		t.alerts.alerts = t.alerts.alerts[:0]
		for i := range saved {
			a := saved[i]
			t.alerts.alerts = append(t.alerts.alerts, &a)
		}
		t.alerts.mu.Unlock()
		t.outbox.rows = t.outbox.rows[:rows]
		return err
	}
	return nil
}

type fakeEnv struct {
	engine  *Engine
	alerts  *fakeAlerts
	pending *fakePending
	hosts   *fakeHosts
	outbox  *fakeOutbox
	host    uuid.UUID
	org     uuid.UUID
}

func newFakeEnv(t *testing.T, recipients fakeRecipients) *fakeEnv {
	t.Helper()
	f := &fakeEnv{alerts: &fakeAlerts{}, pending: &fakePending{}, outbox: &fakeOutbox{}, host: uuid.New(), org: uuid.New()}
	f.hosts = &fakeHosts{host: model.Host{ID: f.host, OrganizationID: f.org, Title: "web-1", IP: "10.0.0.5"}}
	f.engine = newEngineWith(Stores{
		Alerts:        f.alerts,
		Pending:       f.pending,
		Thresholds:    fakeThresholds{model.MetricTypeCPU: {MetricType: model.MetricTypeCPU, WarningLevel: 70, CriticalLevel: 90}},
		Hosts:         f.hosts,
		Metrics:       fakeMetrics{},
		Organizations: fakeOrgs{org: model.Organization{ID: f.org, Name: "Acme"}},
		Recipients:    recipients,
		Tx:            fakeTx{alerts: f.alerts, outbox: f.outbox},
	}, []notify.Notifier{&fakeNotifier{channel: model.ChannelEmail}, &fakeNotifier{channel: model.ChannelSMS}},
		"https://panel.example.com/", nil)
	return f
}

func (f *fakeEnv) report(cpu float64) {
	f.engine.EvaluateMetrics(context.Background(), f.host, f.org, cpu, 10, nil)
}

// Açılma, yükselme, düşme ve çözülme her biri bir bildirimdir; aynı seviyede kalmak bildirim üretmez.
func TestFakeEngineNotifiesOnLifecycleChanges(t *testing.T) {
	f := newFakeEnv(t, fakeRecipients{{Channel: model.ChannelEmail, Address: "ops@acme.test", Name: "Ops"}})

	for _, cpu := range []float64{50, 75, 80, 95, 96, 72, 40, 30} {
		f.report(cpu)
	}
	got := f.outbox.subjects()
	wantLevels := []string{"UYARI", "KRİTİK", "UYARI", "ÇÖZÜLDÜ"}
	if len(got) != len(wantLevels) {
		t.Fatalf("subjects = %q, want %d notifications (%v)", got, len(wantLevels), wantLevels)
	}
	for i, level := range wantLevels {
		if want := "[HealthBeat] -- " + level + " / Acme / web-1(10.0.0.5) - "; !strings.HasPrefix(got[i], want) {
			t.Errorf("subject %d = %q, want prefix %q", i, got[i], want)
		}
	}
	wantEvents := []string{store.AlertEventOpened, store.AlertEventLevelChanged, store.AlertEventLevelChanged, store.AlertEventResolved}
	wantRowLevels := []string{model.AlertLevelWarning, model.AlertLevelCritical, model.AlertLevelWarning, model.AlertLevelWarning}
	for i, row := range f.outbox.rows {
		if row.Kind != store.OutboxKindAlert || row.AlertID == nil || row.Seal ||
			row.AlertEvent != wantEvents[i] || row.AlertLevel != wantRowLevels[i] {
			t.Fatalf("row %d = %+v, want event %s at level %s", i, row, wantEvents[i], wantRowLevels[i])
		}
	}
}

// Her alıcı kuyrukta ayrı bir satırdır (alıcılar birbirini görmez); hepsi aynı metni taşır. Göndericisi olmayan kanalın
// alıcısı ve kanalı kapalı kuralın alıcısı atlanır.
func TestFakeEngineQueuesOneRowPerRecipient(t *testing.T) {
	f := newFakeEnv(t, fakeRecipients{
		{Channel: model.ChannelEmail, Address: "a@acme.test", Name: "A"},
		{Channel: model.ChannelSlack, Address: "#ops", Name: "Slack"},
		{Channel: model.ChannelSMS, Address: "+905550000000", Name: "Nöbetçi"},
		{Channel: model.ChannelEmail, Address: "b@acme.test", Name: "B"},
		{Channel: model.ChannelEmail, Address: "c@acme.test", Name: "C", ChannelOff: true},
	})
	f.report(95)

	var got []string
	for _, row := range f.outbox.rows {
		got = append(got, row.Channel+":"+strings.Join(row.Recipients, ","))
		if row.Subject != f.outbox.rows[0].Subject || row.Body != f.outbox.rows[0].Body {
			t.Fatal("recipients received different messages for the same alert")
		}
	}
	if strings.Join(got, " ") != "email:a@acme.test sms:+905550000000 email:b@acme.test" {
		t.Fatalf("rows = %v, want one per deliverable recipient", got)
	}
}

// Teslim edilebilir alıcı yoksa bildirim metni hiç kurulmaz (sunucu/organizasyon okunmaz); alert yine kaydedilir.
func TestFakeEngineSkipsLookupsWithoutRecipients(t *testing.T) {
	f := newFakeEnv(t, fakeRecipients{{Channel: model.ChannelSlack, Address: "#ops", Name: "Slack"}})
	f.report(95)
	if len(f.outbox.rows) != 0 || f.hosts.lookups != 0 {
		t.Fatalf("rows=%d host lookups=%d; want nothing", len(f.outbox.rows), f.hosts.lookups)
	}
	if len(f.alerts.alerts) != 1 {
		t.Fatalf("alerts = %d, want the alert itself to be recorded", len(f.alerts.alerts))
	}
}

// Alert kaydı esastır: bildirim kuyruğa yazılamazsa alert yine açılır (bildirimsiz); düzelince normal akış sürer ve
// "ÇÖZÜLDÜ" bildirimi gider.
func TestFakeEngineKeepsAlertWhenNotificationCannotBeQueued(t *testing.T) {
	f := newFakeEnv(t, fakeRecipients{{Channel: model.ChannelEmail, Address: "ops@acme.test", Name: "Ops"}})
	f.outbox.fail = errors.New("db down")
	f.report(95)
	if len(f.alerts.alerts) != 1 || f.alerts.alerts[0].Status != model.AlertStatusOpen || len(f.outbox.rows) != 0 {
		t.Fatalf("alerts=%d rows=%d after a failed enqueue; want the alert recorded without a notification", len(f.alerts.alerts), len(f.outbox.rows))
	}

	f.outbox.fail = nil
	f.report(96) // aynı seviye: yeni bildirim yok
	f.report(30) // çözülme
	got := f.outbox.subjects()
	if len(got) != 1 || !strings.HasPrefix(got[0], "[HealthBeat] -- ÇÖZÜLDÜ / ") {
		t.Fatalf("subjects = %q, want only the resolution", got)
	}
	if f.alerts.alerts[0].Status != model.AlertStatusResolved {
		t.Fatalf("alert status = %s, want resolved", f.alerts.alerts[0].Status)
	}
}
