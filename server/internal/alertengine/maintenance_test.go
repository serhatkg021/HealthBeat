package alertengine_test

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"healthbeat-server/internal/model"
	"healthbeat-server/internal/store"
	"healthbeat-server/internal/testdb"
)

// window, şu an süren tek seferlik bir bakım penceresi açar (hosts ve orgs kapsamıyla) ve kimliğini döndürür.
func (e *env) window(t *testing.T, hosts, orgs []uuid.UUID) uuid.UUID {
	t.Helper()
	start, end := time.Now().Add(-time.Hour), time.Now().Add(time.Hour)
	w, err := store.NewMaintenanceWindows(e.pool).Create(e.ctx, model.MaintenanceWindow{Title: "bakım", Recurrence: model.RecurrenceOnce,
		StartsAt: &start, EndsAt: &end, RepeatEvery: 1, HostIDs: hosts, OrgIDs: orgs})
	if err != nil {
		t.Fatal(err)
	}
	return w.ID
}

func (e *env) endWindow(t *testing.T, id uuid.UUID) {
	t.Helper()
	if _, err := store.NewMaintenanceWindows(e.pool).End(e.ctx, id, time.Now()); err != nil {
		t.Fatal(err)
	}
}

// Bakımdayken alert açılır ve seviyesi değişir ama e-posta gitmez; bakım bitince güncel durumu tek e-postayla, notla
// bildirilir ve işaret kalkar; ikinci tur bir şey göndermez.
func TestMaintenanceDefersNotificationsUntilItEnds(t *testing.T) {
	e := newEnv(t)
	w := e.window(t, []uuid.UUID{e.host}, nil)

	e.feedCPU(85) // uyarı
	e.feedCPU(97) // kritiğe çıkar
	rows := e.rows(t, "cpu")
	if len(rows) != 1 || rows[0].Level != model.AlertLevelCritical || !rows[0].NotifyPending {
		t.Fatalf("during maintenance: alerts = %+v, want one critical alert marked as deferred", rows)
	}
	if n := len(e.messages()); n != 0 {
		t.Fatalf("%d emails during maintenance, want none", n)
	}
	e.engine.FlushDeferred(e.ctx)
	if n := len(e.messages()); n != 0 {
		t.Fatalf("flush while still in maintenance sent %d emails", n)
	}

	e.endWindow(t, w)
	e.engine.FlushDeferred(e.ctx)
	msgs := e.messages()
	if len(msgs) != 1 || !strings.Contains(msgs[0].Text(), "KRİTİK") || !strings.Contains(msgs[0].Text(), "bakımdayken açıldı") {
		t.Fatalf("after maintenance: %d emails, want one critical with the maintenance note:\n%v", len(msgs), msgs)
	}
	if rows := e.rows(t, "cpu"); rows[0].NotifyPending {
		t.Fatal("the deferred mark is still set after the notification")
	}
	e.engine.FlushDeferred(e.ctx)
	if n := len(e.messages()); n != 1 {
		t.Fatalf("a second flush sent more emails: %d in total", n)
	}

	// Bakımdan sonra her şey olağan: çözülme bildirilir.
	e.feedCPU(20)
	if n := len(e.messages()); n != 2 {
		t.Fatalf("resolution after maintenance: %d emails in total, want 2", n)
	}
}

// Bakımda açılıp yine bakımda çözülen alert için hiç e-posta gitmez; açılışı bildirilmeden (bakım bitip bekleyen
// bildirim gitmeden) çözülen alert için de.
func TestMaintenanceAlertResolvedBeforeItWasNotifiedSendsNothing(t *testing.T) {
	e := newEnv(t)
	w := e.window(t, []uuid.UUID{e.host}, nil)
	e.feedCPU(97)
	e.feedCPU(20)
	if rows := e.rows(t, "cpu"); len(rows) != 1 || rows[0].Status != model.AlertStatusResolved || rows[0].NotifyPending {
		t.Fatalf("resolved during maintenance: %+v", rows)
	}

	e.feedCPU(97) // bakımda yeniden açılır
	e.endWindow(t, w)
	e.feedCPU(20) // bekleyen bildirim gönderilmeden çözülür
	e.engine.FlushDeferred(e.ctx)
	if n := len(e.messages()); n != 0 {
		t.Fatalf("%d emails, want none", n)
	}
}

// Organizasyon kapsamlı pencere o organizasyonun sunucularını susturur, alt organizasyonun sunucusunu susturmaz;
// çevrimdışı alert'i de susturulur.
func TestMaintenanceByOrganizationCoversOnlyDirectHosts(t *testing.T) {
	e := newEnv(t)
	child := testdb.ChildOrg(t, e.pool, "alt", e.org)
	other := testdb.PushHost(t, e.pool, child, "db-1", "x")
	e.window(t, nil, []uuid.UUID{e.org})

	e.feedCPU(97)
	e.engine.RaiseOffline(e.ctx, e.host, e.org)
	e.engine.EvaluateMetrics(e.ctx, other, child, 97, 10, nil)
	msgs := e.messages()
	if len(msgs) != 1 || !strings.Contains(msgs[0].Text(), "db-1") {
		t.Fatalf("%d emails, want only the child organization's host to be notified:\n%v", len(msgs), msgs)
	}
	if rows := e.rows(t, model.AlertTypeHostOffline); len(rows) != 1 || !rows[0].NotifyPending {
		t.Fatalf("offline alert during maintenance = %+v, want it deferred", rows)
	}
}

// İki çakışan bakımdan biri bitince bildirim gitmez; ikisi de bitince gider.
func TestOverlappingMaintenanceDefersUntilBothEnd(t *testing.T) {
	e := newEnv(t)
	first := e.window(t, []uuid.UUID{e.host}, nil)
	second := e.window(t, nil, []uuid.UUID{e.org})
	e.feedCPU(97)

	e.endWindow(t, first)
	e.engine.FlushDeferred(e.ctx)
	if n := len(e.messages()); n != 0 {
		t.Fatalf("one window still running: %d emails, want none", n)
	}
	e.endWindow(t, second)
	e.engine.FlushDeferred(e.ctx)
	if n := len(e.messages()); n != 1 {
		t.Fatalf("both windows ended: %d emails, want one", n)
	}
}

// "Bu tekrarı bitir" susturmayı hemen kaldırır: sonraki değişiklik olağan biçimde bildirilir.
func TestEndingTheOccurrenceStopsSuppression(t *testing.T) {
	e := newEnv(t)
	from := time.Now().Add(-24 * time.Hour)
	minute := 0
	duration := 1440
	maint := store.NewMaintenanceWindows(e.pool)
	w, err := maint.Create(e.ctx, model.MaintenanceWindow{Title: "her gün", Recurrence: model.RecurrenceDaily, StartMinute: &minute,
		DurationMinutes: &duration, RepeatEvery: 1, ValidFrom: &from, HostIDs: []uuid.UUID{e.host}})
	if err != nil {
		t.Fatal(err)
	}
	e.feedCPU(85)
	if n := len(e.messages()); n != 0 {
		t.Fatalf("all-day daily maintenance: %d emails", n)
	}
	got, err := maint.Get(e.ctx, w.ID)
	if err != nil {
		t.Fatal(err)
	}
	// Süren tekrarı bulmak için pencerenin bütün gün sürdüğünü kullan: ya bugün ya dün başladı.
	now := time.Now()
	for _, start := range []time.Time{time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC), time.Date(now.Year(), now.Month(), now.Day()-1, 0, 0, 0, 0, time.UTC)} {
		if err := maint.SetOverride(e.ctx, got.ID, start, &now, nil); err != nil {
			t.Fatal(err)
		}
	}
	e.feedCPU(97)
	if n := len(e.messages()); n != 1 {
		t.Fatalf("after ending the occurrence: %d emails, want the level change", n)
	}
}
