package store_test

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"

	"healthbeat-server/internal/model"
	"healthbeat-server/internal/store"
	"healthbeat-server/internal/testdb"
)

func onceWindow(title string, start, end time.Time, hosts, orgs []uuid.UUID) model.MaintenanceWindow {
	return model.MaintenanceWindow{Title: title, Recurrence: model.RecurrenceOnce, StartsAt: &start, EndsAt: &end,
		RepeatEvery: 1, HostIDs: hosts, OrgIDs: orgs}
}

func weeklyWindow(title string, from time.Time, hosts, orgs []uuid.UUID) model.MaintenanceWindow {
	return model.MaintenanceWindow{Title: title, Recurrence: model.RecurrenceWeekly, StartMinute: ptr(120),
		DurationMinutes: ptr(90), RepeatEvery: 2, Weekdays: ptr(1<<1 | 1<<3), ValidFrom: &from, HostIDs: hosts, OrgIDs: orgs}
}

func TestMaintenanceWindowCreateGetUpdateDelete(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	s := store.NewMaintenanceWindows(pool)
	org := testdb.Org(t, pool, "o")
	h1, h2 := testdb.PushHost(t, pool, org, "h1", "a"), testdb.PushHost(t, pool, org, "h2", "b")
	user := testdb.User(t, pool, "admin@example.com", model.RoleSuperAdmin, "Password-123")

	start := time.Date(2026, 10, 12, 1, 0, 0, 0, time.UTC)
	in := onceWindow("DC bakımı", start, start.Add(4*time.Hour), []uuid.UUID{h1, h1}, []uuid.UUID{org})
	in.CreatedBy = &user
	w, err := s.Create(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	if w.ID == uuid.Nil || w.Title != "DC bakımı" || !w.StartsAt.Equal(start) || *w.CreatedBy != user || w.EndedAt != nil {
		t.Fatalf("created = %+v", w)
	}
	if len(w.HostIDs) != 1 || w.HostIDs[0] != h1 || len(w.OrgIDs) != 1 || w.OrgIDs[0] != org {
		t.Fatalf("scope = hosts %v orgs %v, want the duplicate host stored once and the organization", w.HostIDs, w.OrgIDs)
	}

	from := time.Date(2026, 10, 6, 0, 0, 0, 0, time.UTC)
	up := weeklyWindow("Güncelleme", from, []uuid.UUID{h2}, nil)
	up.ID = w.ID
	w2, err := s.Update(ctx, up)
	if err != nil {
		t.Fatal(err)
	}
	if w2.Recurrence != model.RecurrenceWeekly || w2.StartsAt != nil || *w2.StartMinute != 120 || w2.RepeatEvery != 2 ||
		*w2.Weekdays != 0b1010 || !w2.ValidFrom.Equal(from) || !slices.Equal(w2.HostIDs, []uuid.UUID{h2}) || len(w2.OrgIDs) != 0 ||
		w2.UpdatedAt.Before(w.UpdatedAt) || !w2.CreatedAt.Equal(w.CreatedAt) {
		t.Fatalf("updated = %+v", w2)
	}

	if err := s.Delete(ctx, w.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get(ctx, w.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("after delete: err = %v, want ErrNotFound", err)
	}
	if err := s.Delete(ctx, w.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("second delete: err = %v, want ErrNotFound", err)
	}
	var scopeRows int
	if err := pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM maintenance_window_hosts) + (SELECT count(*) FROM maintenance_window_orgs)`).Scan(&scopeRows); err != nil || scopeRows != 0 {
		t.Fatalf("scope rows after delete = %d (err %v), want 0", scopeRows, err)
	}
}

// Kısıtlara uymayan alanlar ve olmayan kapsam adlandırılmış hatalarla reddedilir; yarım kayıt kalmaz.
func TestMaintenanceWindowRejectsInvalidShapes(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	s := store.NewMaintenanceWindows(pool)
	host := testdb.PushHost(t, pool, testdb.Org(t, pool, "o"), "h", "a")
	start := time.Date(2026, 10, 12, 1, 0, 0, 0, time.UTC)
	from := time.Date(2026, 10, 6, 0, 0, 0, 0, time.UTC)
	hosts := []uuid.UUID{host}

	bad := map[string]model.MaintenanceWindow{
		"end before start": onceWindow("x", start, start.Add(-time.Hour), hosts, nil),
		"blank title":      onceWindow("  ", start, start.Add(time.Hour), hosts, nil),
		"once with repeat fields": func() model.MaintenanceWindow {
			w := onceWindow("x", start, start.Add(time.Hour), hosts, nil)
			w.StartMinute = ptr(0)
			return w
		}(),
		"weekly without days":   func() model.MaintenanceWindow { w := weeklyWindow("x", from, hosts, nil); w.Weekdays = nil; return w }(),
		"weekly every 13 weeks": func() model.MaintenanceWindow { w := weeklyWindow("x", from, hosts, nil); w.RepeatEvery = 13; return w }(),
		"daily over 24 hours": {Title: "x", Recurrence: model.RecurrenceDaily, StartMinute: ptr(0), DurationMinutes: ptr(1441),
			RepeatEvery: 1, ValidFrom: &from, HostIDs: hosts},
		"monthly day 29": {Title: "x", Recurrence: model.RecurrenceMonthly, StartMinute: ptr(0), DurationMinutes: ptr(60),
			RepeatEvery: 1, MonthDay: ptr(29), ValidFrom: &from, HostIDs: hosts},
		"monthly day and week": {Title: "x", Recurrence: model.RecurrenceMonthly, StartMinute: ptr(0), DurationMinutes: ptr(60),
			RepeatEvery: 1, MonthDay: ptr(1), MonthWeek: ptr(1), MonthWeekday: ptr(7), ValidFrom: &from, HostIDs: hosts},
		"monthly week without weekday": {Title: "x", Recurrence: model.RecurrenceMonthly, StartMinute: ptr(0), DurationMinutes: ptr(60),
			RepeatEvery: 1, MonthWeek: ptr(-1), ValidFrom: &from, HostIDs: hosts},
		"until before from": func() model.MaintenanceWindow {
			w := weeklyWindow("x", from, hosts, nil)
			w.ValidUntil = ptr(from.AddDate(0, 0, -1))
			return w
		}(),
	}
	for name, w := range bad {
		if _, err := s.Create(ctx, w); !errors.Is(err, store.ErrMaintenanceWindowInvalid) {
			t.Errorf("%s: err = %v, want ErrMaintenanceWindowInvalid", name, err)
		}
	}
	if _, err := s.Create(ctx, onceWindow("x", start, start.Add(time.Hour), []uuid.UUID{uuid.New()}, nil)); !errors.Is(err, store.ErrMaintenanceScopeMissing) {
		t.Errorf("unknown host: err = %v, want ErrMaintenanceScopeMissing", err)
	}
	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM maintenance_windows`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("windows after rejected creates = %d (err %v), want 0", n, err)
	}

	for _, ok := range []model.MaintenanceWindow{
		{Title: "son gün", Recurrence: model.RecurrenceMonthly, StartMinute: ptr(1320), DurationMinutes: ptr(60), RepeatEvery: 3,
			MonthDay: ptr(model.MonthLast), ValidFrom: &from, HostIDs: hosts},
		{Title: "son cuma", Recurrence: model.RecurrenceMonthly, StartMinute: ptr(1320), DurationMinutes: ptr(10080), RepeatEvery: 1,
			MonthWeek: ptr(model.MonthLast), MonthWeekday: ptr(5), ValidFrom: &from, HostIDs: hosts},
		{Title: "gece", Recurrence: model.RecurrenceDaily, StartMinute: ptr(1380), DurationMinutes: ptr(180), RepeatEvery: 30,
			ValidFrom: &from, ValidUntil: &from, HostIDs: hosts},
	} {
		if _, err := s.Create(ctx, ok); err != nil {
			t.Errorf("%s rejected: %v", ok.Title, err)
		}
	}
}

// Bitirilen pencere bir daha bitirilemez ve değiştirilemez; istisna aynı tekrar için öncekinin yerine geçer.
func TestMaintenanceWindowEndAndOverrides(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	s := store.NewMaintenanceWindows(pool)
	host := testdb.PushHost(t, pool, testdb.Org(t, pool, "o"), "h", "a")
	from := time.Date(2026, 10, 6, 0, 0, 0, 0, time.UTC)
	w, err := s.Create(ctx, weeklyWindow("haftalık", from, []uuid.UUID{host}, nil))
	if err != nil {
		t.Fatal(err)
	}

	occ := time.Date(2026, 10, 7, 2, 0, 0, 0, time.UTC)
	if err := s.SetOverride(ctx, w.ID, occ, nil, nil); err != nil {
		t.Fatal(err)
	}
	early := occ.Add(40 * time.Minute)
	if err := s.SetOverride(ctx, w.ID, occ, &early, nil); err != nil {
		t.Fatal(err)
	}
	got, err := s.Get(ctx, w.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Overrides) != 1 || !got.Overrides[0].OccurrenceStart.Equal(occ) || got.Overrides[0].EndedAt == nil || !got.Overrides[0].EndedAt.Equal(early) {
		t.Fatalf("overrides = %+v, want one early end replacing the skip", got.Overrides)
	}
	if err := s.SetOverride(ctx, uuid.New(), occ, nil, nil); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("override on a missing window: err = %v, want ErrNotFound", err)
	}

	at := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	ended, err := s.End(ctx, w.ID, at)
	if err != nil || ended.EndedAt == nil || !ended.EndedAt.Equal(at) {
		t.Fatalf("end: %+v err=%v", ended.EndedAt, err)
	}
	if _, err := s.End(ctx, w.ID, at); !errors.Is(err, store.ErrMaintenanceWindowEnded) {
		t.Fatalf("second end: err = %v, want ErrMaintenanceWindowEnded", err)
	}
	if _, err := s.End(ctx, uuid.New(), at); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("end of a missing window: err = %v, want ErrNotFound", err)
	}
	up := weeklyWindow("değişti", from, []uuid.UUID{host}, nil)
	up.ID = w.ID
	if _, err := s.Update(ctx, up); !errors.Is(err, store.ErrMaintenanceWindowEnded) {
		t.Fatalf("update of an ended window: err = %v, want ErrMaintenanceWindowEnded", err)
	}
}

// ForHost, sunucuyu doğrudan ya da doğrudan organizasyonu üzerinden kapsayan ve geride kalmamış pencereleri döndürür;
// alt organizasyonun penceresi üst organizasyondaki sunucuyu kapsamaz.
func TestMaintenanceWindowsForHost(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	s := store.NewMaintenanceWindows(pool)
	parent := testdb.Org(t, pool, "üst")
	child := testdb.ChildOrg(t, pool, "alt", parent)
	host := testdb.PushHost(t, pool, parent, "h", "a")
	other := testdb.PushHost(t, pool, child, "x", "b")
	now := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)

	create := func(w model.MaintenanceWindow) uuid.UUID {
		t.Helper()
		out, err := s.Create(ctx, w)
		if err != nil {
			t.Fatal(err)
		}
		return out.ID
	}
	direct := create(onceWindow("doğrudan", now.Add(time.Hour), now.Add(2*time.Hour), []uuid.UUID{host}, nil))
	byOrg := create(weeklyWindow("organizasyon", now.AddDate(0, 0, -30), nil, []uuid.UUID{parent}))
	create(onceWindow("geçmiş", now.Add(-3*time.Hour), now.Add(-time.Hour), []uuid.UUID{host}, nil))
	create(weeklyWindow("alt dal", now.AddDate(0, 0, -30), nil, []uuid.UUID{child}))
	create(onceWindow("başka sunucu", now, now.Add(time.Hour), []uuid.UUID{other}, nil))
	oldSeries := weeklyWindow("eski seri", now.AddDate(0, 0, -60), []uuid.UUID{host}, nil)
	oldSeries.ValidUntil = ptr(now.AddDate(0, 0, -9))
	create(oldSeries)
	ended := create(weeklyWindow("bitirilen", now.AddDate(0, 0, -30), []uuid.UUID{host}, nil))
	if _, err := s.End(ctx, ended, now.Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := s.SetOverride(ctx, byOrg, now.Add(24*time.Hour), nil, nil); err != nil {
		t.Fatal(err)
	}

	got, err := s.ForHost(ctx, host, now)
	if err != nil {
		t.Fatal(err)
	}
	var ids []uuid.UUID
	for _, w := range got {
		ids = append(ids, w.ID)
		if w.HostIDs != nil || w.OrgIDs != nil {
			t.Errorf("%s: ForHost filled the scope lists", w.Title)
		}
	}
	if !slices.Equal(ids, []uuid.UUID{direct, byOrg}) {
		t.Fatalf("ForHost = %v, want [direct, byOrg] = [%s %s]", ids, direct, byOrg)
	}
	if len(got[1].Overrides) != 1 {
		t.Fatalf("overrides of the organization window = %+v, want the skip", got[1].Overrides)
	}
}

// List, görünürlük süzgeciyle yalnızca kapsamı görülebilen sunucu ya da organizasyonlara değen pencereleri döndürür.
func TestMaintenanceWindowListVisibility(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	s := store.NewMaintenanceWindows(pool)
	a, b := testdb.Org(t, pool, "a"), testdb.Org(t, pool, "b")
	ha, hb := testdb.PushHost(t, pool, a, "ha", "1"), testdb.PushHost(t, pool, b, "hb", "2")
	start := time.Date(2026, 10, 12, 1, 0, 0, 0, time.UTC)
	for _, w := range []model.MaintenanceWindow{
		onceWindow("a sunucusu", start, start.Add(time.Hour), []uuid.UUID{ha}, nil),
		onceWindow("a organizasyonu", start, start.Add(time.Hour), nil, []uuid.UUID{a}),
		onceWindow("b", start, start.Add(time.Hour), []uuid.UUID{hb}, []uuid.UUID{b}),
	} {
		if _, err := s.Create(ctx, w); err != nil {
			t.Fatal(err)
		}
	}
	titles := func(vis *store.MaintenanceVisibility) []string {
		t.Helper()
		ws, err := s.List(ctx, vis)
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, w := range ws {
			out = append(out, w.Title)
		}
		slices.Sort(out)
		return out
	}
	if got := titles(nil); len(got) != 3 {
		t.Errorf("unfiltered = %v, want all three", got)
	}
	if got := titles(&store.MaintenanceVisibility{OrgIDs: []uuid.UUID{a}, HostIDs: []uuid.UUID{ha}}); !slices.Equal(got, []string{"a organizasyonu", "a sunucusu"}) {
		t.Errorf("organization a = %v", got)
	}
	if got := titles(&store.MaintenanceVisibility{HostIDs: []uuid.UUID{ha}}); !slices.Equal(got, []string{"a sunucusu"}) {
		t.Errorf("operator of ha = %v", got)
	}
	if got := titles(&store.MaintenanceVisibility{}); len(got) != 0 {
		t.Errorf("nothing visible = %v, want none", got)
	}
}

// Sunucu ya da organizasyon silinince kapsam satırları gider; pencere kalır.
func TestMaintenanceScopeFollowsDeletes(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	s := store.NewMaintenanceWindows(pool)
	org := testdb.Org(t, pool, "o")
	host := testdb.PushHost(t, pool, org, "h", "a")
	empty := testdb.Org(t, pool, "boş")
	start := time.Date(2026, 10, 12, 1, 0, 0, 0, time.UTC)
	w, err := s.Create(ctx, onceWindow("x", start, start.Add(time.Hour), []uuid.UUID{host}, []uuid.UUID{empty}))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM hosts WHERE id = $1`, host); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM organizations WHERE id = $1`, empty); err != nil {
		t.Fatal(err)
	}
	got, err := s.Get(ctx, w.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.HostIDs) != 0 || len(got.OrgIDs) != 0 {
		t.Fatalf("scope after deletes = %v / %v, want empty", got.HostIDs, got.OrgIDs)
	}
}

// Ertelenen bildirim bayrağı yalnızca aktif alert'lerde listelenir.
func TestAlertNotifyPendingFlag(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	alerts := store.NewAlerts(pool)
	host := testdb.PushHost(t, pool, testdb.Org(t, pool, "o"), "h", "a")
	open, _, err := alerts.CreateIfNoneActive(ctx, host, "cpu", "", "warning", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	resolved, _, err := alerts.CreateIfNoneActive(ctx, host, "ram", "", "warning", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []uuid.UUID{open.ID, resolved.ID} {
		if err := alerts.SetNotifyPending(ctx, id, true); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := alerts.Resolve(ctx, resolved.ID, nil, nil); err != nil {
		t.Fatal(err)
	}
	got, err := alerts.ListNotifyPending(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != open.ID {
		t.Fatalf("pending = %+v, want only the open alert", got)
	}
	if err := alerts.SetNotifyPending(ctx, open.ID, false); err != nil {
		t.Fatal(err)
	}
	if got, err := alerts.ListNotifyPending(ctx); err != nil || len(got) != 0 {
		t.Fatalf("after clearing: %v err=%v", got, err)
	}
}
