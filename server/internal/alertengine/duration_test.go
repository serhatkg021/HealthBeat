package alertengine

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"healthbeat-server/internal/model"
	"healthbeat-server/internal/notify"
)

// Süre koşulu: eşik kesintisiz süre boyunca aşılmadıkça alert açılmaz; koşul arada kalkarsa süre baştan sayılır; aktif
// alert'in seviye değişimi beklemez. Mekanizma eşik türünden bağımsızdır (API süreyi yalnızca protokol 4 türlerine verir).
func TestThresholdDurationDelaysOpening(t *testing.T) {
	f := newFakeEnv(t, fakeRecipients{})
	d := 300
	f.engine.thresholds = fakeThresholds{model.MetricTypeCPU: {MetricType: model.MetricTypeCPU, WarningLevel: 70, CriticalLevel: 90, DurationSeconds: &d}}
	t0 := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)
	clock := t0
	f.engine.now = func() time.Time { return clock }
	at := func(offset time.Duration, cpu float64) {
		t.Helper()
		clock = t0.Add(offset)
		f.report(cpu)
	}
	open := func() *model.Alert {
		f.alerts.mu.Lock()
		defer f.alerts.mu.Unlock()
		return f.alerts.active(f.host, model.MetricTypeCPU, "", false)
	}
	pending := func() int {
		f.pending.mu.Lock()
		defer f.pending.mu.Unlock()
		return len(f.pending.since)
	}

	at(0, 75)
	if open() != nil || pending() != 1 {
		t.Fatalf("first breach: alert=%v pending=%d, want no alert and a pending condition", open(), pending())
	}
	at(4*time.Minute, 95) // kritik de olsa süre dolmadı
	if open() != nil {
		t.Fatal("opened before the duration elapsed")
	}
	at(5*time.Minute, 75)
	a := open()
	if a == nil || a.Level != model.AlertLevelWarning || pending() != 0 {
		t.Fatalf("after 5 minutes: alert=%+v pending=%d, want an open warning and no pending row", a, pending())
	}
	at(6*time.Minute, 95) // aktif alert'in yükselmesi beklemez
	if a := open(); a == nil || a.Level != model.AlertLevelCritical {
		t.Fatalf("escalation waited for the duration: %+v", a)
	}
	at(7*time.Minute, 10)
	if open() != nil || pending() != 0 {
		t.Fatal("did not resolve below the threshold")
	}

	// Kesinti: koşul arada kalkarsa süre baştan sayılır.
	at(10*time.Minute, 80)
	at(12*time.Minute, 10)
	if pending() != 0 {
		t.Fatal("pending row kept after the condition cleared")
	}
	at(13*time.Minute, 80)
	at(17*time.Minute, 80)
	if open() != nil {
		t.Fatal("the interrupted breach was counted from its first start")
	}
	at(18*time.Minute, 80)
	if open() == nil {
		t.Fatal("did not open 5 minutes after the restarted breach")
	}
}

// Süresiz eşik (nil) bekleme kaydı yazmadan hemen açar: mevcut türlerin davranışı değişmez.
func TestThresholdWithoutDurationOpensImmediately(t *testing.T) {
	f := newFakeEnv(t, fakeRecipients{})
	f.report(95)
	f.alerts.mu.Lock()
	a := f.alerts.active(f.host, model.MetricTypeCPU, "", false)
	f.alerts.mu.Unlock()
	if a == nil || f.pending.marks != 0 {
		t.Fatalf("alert=%v pending marks=%d, want an immediate alert and no pending write", a, f.pending.marks)
	}
}

// Bekleme kaydı yazılamazsa alert erken açılmaz; bir sonraki raporda yeniden denenir.
func TestPendingWriteFailureDoesNotOpenEarly(t *testing.T) {
	alerts := &fakeAlerts{}
	d := 60
	host, org := uuid.New(), uuid.New()
	e := newEngineWith(Stores{
		Alerts:        alerts,
		Pending:       &failingPending{},
		Thresholds:    fakeThresholds{model.MetricTypeCPU: {MetricType: model.MetricTypeCPU, WarningLevel: 70, CriticalLevel: 90, DurationSeconds: &d}},
		Hosts:         &fakeHosts{host: model.Host{ID: host, OrganizationID: org}},
		Metrics:       fakeMetrics{},
		Organizations: fakeOrgs{},
		Recipients:    fakeRecipients{},
		Tx:            fakeTx{alerts: alerts, outbox: &fakeOutbox{}},
	}, []notify.Notifier{&fakeNotifier{channel: model.ChannelEmail}}, "", nil)
	e.EvaluateMetrics(context.Background(), host, org, 95, 10, nil)
	if len(alerts.alerts) != 0 {
		t.Fatalf("alerts = %d, want none while the pending condition cannot be recorded", len(alerts.alerts))
	}
}
