package httpapi_test

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"healthbeat-server/internal/testdb"
)

type occurrenceJSON struct {
	Start      time.Time `json:"start"`
	End        time.Time `json:"end"`
	StartLocal string    `json:"start_local"`
	EndLocal   string    `json:"end_local"`
}

type scopeJSON struct {
	ID   uuid.UUID `json:"id"`
	Name string    `json:"name"`
}

type maintenanceJSON struct {
	ID            uuid.UUID        `json:"id"`
	Title         string           `json:"title"`
	Recurrence    string           `json:"recurrence"`
	StartsAt      *time.Time       `json:"starts_at"`
	StartsLocal   *string          `json:"starts_local"`
	EndsLocal     *string          `json:"ends_local"`
	StartTime     *string          `json:"start_time"`
	Weekdays      []int            `json:"weekdays"`
	ValidFrom     *string          `json:"valid_from"`
	EndedAt       *time.Time       `json:"ended_at"`
	Hosts         []scopeJSON      `json:"hosts"`
	Organizations []scopeJSON      `json:"organizations"`
	HiddenScope   int              `json:"hidden_scope"`
	Status        string           `json:"status"`
	Current       *occurrenceJSON  `json:"current"`
	Next          *occurrenceJSON  `json:"next"`
	Last          *occurrenceJSON  `json:"last"`
	SkippedLocal  []string         `json:"skipped_local"`
	Upcoming      []occurrenceJSON `json:"upcoming"`
	CanManage     bool             `json:"can_manage"`
}

type maintenanceListJSON struct {
	Timezone string            `json:"timezone"`
	Windows  []maintenanceJSON `json:"windows"`
}

const maintenancePath = "/api/v1/maintenance-windows"

func once(title, from, to string, hosts, orgs []uuid.UUID) map[string]any {
	return map[string]any{"title": title, "recurrence": "once", "starts_local": from, "ends_local": to,
		"host_ids": nonNil(hosts), "organization_ids": nonNil(orgs)}
}

func nonNil(ids []uuid.UUID) []uuid.UUID {
	if ids == nil {
		return []uuid.UUID{}
	}
	return ids
}

func setTimezone(a *api, root, name string) {
	a.expect(200, "PATCH", "/api/v1/settings", root, map[string]any{"timezone": name}, nil)
}

func localStarts(os []occurrenceJSON) []string {
	out := make([]string, len(os))
	for i, o := range os {
		out[i] = o.StartLocal
	}
	return out
}

// Saatler kurulumun saat diliminde alınır ve verilir; kesin an yanıtta UTC'dir. Oluşturma, liste, ayrıntı, düzenleme,
// silme ve denetim kaydı.
func TestMaintenanceCRUDUsesInstallationLocalTime(t *testing.T) {
	a := newAPI(t)
	root, _ := a.login("root@x.test", "super_admin")
	setTimezone(a, root, "Europe/Istanbul")
	org := a.createOrg(root, "A")
	h1, h2 := a.createPushHost(root, org, "h1"), a.createPushHost(root, org, "h2")

	var w maintenanceJSON
	a.expect(201, "POST", maintenancePath, root, once("DC bakımı", "2099-10-12T01:00", "2099-10-12T05:00", []uuid.UUID{h1.ID}, nil), &w)
	if w.StartsAt == nil || !w.StartsAt.Equal(time.Date(2099, 10, 11, 22, 0, 0, 0, time.UTC)) || *w.StartsLocal != "2099-10-12T01:00" ||
		*w.EndsLocal != "2099-10-12T05:00" || w.Status != "scheduled" || w.Next == nil || len(w.Upcoming) != 1 || !w.CanManage {
		t.Fatalf("created one-off window = %+v", w)
	}
	if len(w.Hosts) != 1 || w.Hosts[0].Name != "h1" || w.HiddenScope != 0 {
		t.Fatalf("scope = %+v hidden %d", w.Hosts, w.HiddenScope)
	}

	var weekly maintenanceJSON
	a.expect(201, "POST", maintenancePath, root, map[string]any{
		"title": "Güncelleme", "recurrence": "weekly", "start_time": "22:00", "duration_minutes": 90, "repeat_every": 2,
		"weekdays": []int{4, 2, 2}, "valid_from": "2099-01-07", "organization_ids": []uuid.UUID{org},
	}, &weekly)
	if *weekly.StartTime != "22:00" || !slices.Equal(weekly.Weekdays, []int{2, 4}) || *weekly.ValidFrom != "2099-01-07" ||
		weekly.Organizations[0].Name != "A" {
		t.Fatalf("weekly = %+v", weekly)
	}
	if got := localStarts(weekly.Upcoming); !slices.Equal(got, []string{"2099-01-08T22:00", "2099-01-20T22:00", "2099-01-22T22:00", "2099-02-03T22:00", "2099-02-05T22:00"}) {
		t.Fatalf("weekly upcoming = %v", got)
	}

	var list maintenanceListJSON
	a.expect(200, "GET", maintenancePath, root, nil, &list)
	if list.Timezone != "Europe/Istanbul" || len(list.Windows) != 2 || list.Windows[0].ID != weekly.ID || list.Windows[0].Upcoming != nil {
		t.Fatalf("list = %+v", list)
	}

	edit := once("DC bakımı (uzadı)", "2099-10-12T01:00", "2099-10-12T07:30", []uuid.UUID{h2.ID}, []uuid.UUID{org})
	id := w.ID
	w = maintenanceJSON{}
	a.expect(200, "PUT", maintenancePath+"/"+id.String(), root, edit, &w)
	if w.Title != "DC bakımı (uzadı)" || *w.EndsLocal != "2099-10-12T07:30" || len(w.Hosts) != 1 || w.Hosts[0].ID != h2.ID || len(w.Organizations) != 1 {
		t.Fatalf("updated = %+v", w)
	}

	a.expect(204, "DELETE", maintenancePath+"/"+w.ID.String(), root, nil, nil)
	a.expect(404, "GET", maintenancePath+"/"+w.ID.String(), root, nil, nil)
	a.expect(404, "DELETE", maintenancePath+"/"+w.ID.String(), root, nil, nil)
	for action, want := range map[string]int{"maintenance.create": 2, "maintenance.update": 1, "maintenance.delete": 1} {
		if n := a.auditCount(action); n != want {
			t.Errorf("%s audit entries = %d, want %d", action, n, want)
		}
	}
}

// Kurallara uymayan istek alanı adlandıran 400 alır; önizleme kaydetmez.
func TestMaintenanceValidationAndPreview(t *testing.T) {
	a := newAPI(t)
	root, _ := a.login("root@x.test", "super_admin")
	host := a.createPushHost(root, a.createOrg(root, "A"), "h")
	hosts := []any{host.ID}

	for field, body := range map[string]map[string]any{
		"starts_local": {"title": "x", "recurrence": "once", "starts_local": "12.10.2099 01:00", "ends_local": "2099-10-12T02:00", "host_ids": hosts},
		"ends_local":   {"title": "x", "recurrence": "once", "starts_local": "2099-10-12T02:00", "ends_local": "2099-10-12T01:00", "host_ids": hosts},
		"month_day":    {"title": "x", "recurrence": "monthly", "start_time": "02:00", "duration_minutes": 60, "month_day": 29, "valid_from": "2099-01-01", "host_ids": hosts},
		"weekdays":     {"title": "x", "recurrence": "weekly", "start_time": "02:00", "duration_minutes": 60, "weekdays": []int{8}, "valid_from": "2099-01-01", "host_ids": hosts},
		"scope":        {"title": "x", "recurrence": "daily", "start_time": "02:00", "duration_minutes": 60, "valid_from": "2099-01-01"},
		"title":        {"title": " ", "recurrence": "daily", "start_time": "02:00", "duration_minutes": 60, "valid_from": "2099-01-01", "host_ids": hosts},
	} {
		var e struct {
			Fields map[string]string `json:"fields"`
		}
		a.expect(400, "POST", maintenancePath, root, body, &e)
		if e.Fields[field] == "" {
			t.Errorf("%s: fields = %v, want the field named", field, e.Fields)
		}
	}
	var e struct {
		Fields map[string]string `json:"fields"`
	}
	a.expect(400, "POST", maintenancePath, root, once("x", "2099-10-12T01:00", "2099-10-12T02:00", []uuid.UUID{uuid.New()}, nil), &e)
	if e.Fields["scope"] == "" {
		t.Errorf("unknown host: fields = %v", e.Fields)
	}

	var p struct {
		Timezone string           `json:"timezone"`
		Upcoming []occurrenceJSON `json:"upcoming"`
	}
	a.expect(200, "POST", maintenancePath+"/preview", root, map[string]any{
		"title": "x", "recurrence": "monthly", "start_time": "03:00", "duration_minutes": 120, "month_week": -1, "month_weekday": 5,
		"valid_from": "2099-01-01",
	}, &p)
	if got := localStarts(p.Upcoming); len(got) != 5 || got[0] != "2099-01-30T03:00" {
		t.Errorf("preview (last Friday from January 2099) = %v", got)
	}
	a.expect(400, "POST", maintenancePath+"/preview", root, map[string]any{"title": "x", "recurrence": "daily", "start_time": "25:00"}, nil)
	var list maintenanceListJSON
	a.expect(200, "GET", maintenancePath, root, nil, &list)
	if len(list.Windows) != 0 {
		t.Fatalf("windows after rejected creates and previews = %d, want 0", len(list.Windows))
	}
}

// Organizasyon yöneticisi kendi dalına değen pencereleri görür, yalnızca kapsamı tamamen dalında olanları yönetir;
// görülemeyen kapsamın adı verilmez. Operatör atandığı sunucuya ve onun organizasyonuna değen pencereleri görür,
// hiçbirini yönetemez.
func TestMaintenanceVisibilityAndManagement(t *testing.T) {
	a := newAPI(t)
	root, _ := a.login("root@x.test", "super_admin")
	orgA, orgB := a.createOrg(root, "A"), a.createOrg(root, "B")
	hA, hA2, hB := a.createPushHost(root, orgA, "a1"), a.createPushHost(root, orgA, "a2"), a.createPushHost(root, orgB, "b1")
	admin, adminID := a.login("admin@x.test", "org_admin")
	testdb.AssignOrg(t, a.pool, adminID, orgA)
	op, opID := a.login("op@x.test", "operator")
	testdb.AssignHost(t, a.pool, opID, hA.ID)

	create := func(token string, want int, body map[string]any) maintenanceJSON {
		t.Helper()
		var w maintenanceJSON
		a.expect(want, "POST", maintenancePath, token, body, &w)
		return w
	}
	mixed := create(root, 201, once("karışık", "2099-10-12T01:00", "2099-10-12T02:00", []uuid.UUID{hA.ID, hB.ID}, nil))
	onlyB := create(root, 201, once("yalnız B", "2099-10-12T01:00", "2099-10-12T02:00", []uuid.UUID{hB.ID}, nil))
	byOrgA := create(root, 201, once("A organizasyonu", "2099-10-12T01:00", "2099-10-12T02:00", nil, []uuid.UUID{orgA}))
	onlyA2 := create(admin, 201, once("a2", "2099-10-12T01:00", "2099-10-12T02:00", []uuid.UUID{hA2.ID}, nil))
	create(admin, 403, once("dal dışı", "2099-10-12T01:00", "2099-10-12T02:00", []uuid.UUID{hB.ID}, nil))
	create(admin, 403, once("dal dışı org", "2099-10-12T01:00", "2099-10-12T02:00", nil, []uuid.UUID{orgB}))
	create(op, 403, once("operatör", "2099-10-12T01:00", "2099-10-12T02:00", []uuid.UUID{hA.ID}, nil))

	titles := func(token string) map[string]maintenanceJSON {
		t.Helper()
		var l maintenanceListJSON
		a.expect(200, "GET", maintenancePath, token, nil, &l)
		out := map[string]maintenanceJSON{}
		for _, w := range l.Windows {
			out[w.Title] = w
		}
		return out
	}
	adminView := titles(admin)
	if len(adminView) != 3 || adminView["yalnız B"].ID != uuid.Nil {
		t.Fatalf("org admin sees %v, want karışık, A organizasyonu, a2", keys(adminView))
	}
	if m := adminView["karışık"]; m.CanManage || m.HiddenScope != 1 || len(m.Hosts) != 1 || m.Hosts[0].Name != "a1" {
		t.Errorf("mixed window for the org admin = %+v, want read-only with one hidden host", m)
	}
	if !adminView["A organizasyonu"].CanManage || !adminView["a2"].CanManage {
		t.Error("the org admin cannot manage windows inside the branch")
	}
	a.expect(403, "PUT", maintenancePath+"/"+mixed.ID.String(), admin, once("x", "2099-10-12T01:00", "2099-10-12T02:00", []uuid.UUID{hA.ID}, nil), nil)
	a.expect(403, "DELETE", maintenancePath+"/"+mixed.ID.String(), admin, nil, nil)
	a.expect(403, "POST", maintenancePath+"/"+mixed.ID.String()+"/end", admin, nil, nil)
	a.expect(404, "GET", maintenancePath+"/"+onlyB.ID.String(), admin, nil, nil)
	a.expect(403, "PUT", maintenancePath+"/"+onlyA2.ID.String(), admin, once("x", "2099-10-12T01:00", "2099-10-12T02:00", []uuid.UUID{hB.ID}, nil), nil)

	opView := titles(op)
	if len(opView) != 2 || opView["karışık"].ID == uuid.Nil || opView["A organizasyonu"].ID == uuid.Nil {
		t.Fatalf("operator sees %v, want karışık and A organizasyonu", keys(opView))
	}
	for _, w := range opView {
		if w.CanManage {
			t.Errorf("operator can manage %q", w.Title)
		}
	}
	a.expect(404, "GET", maintenancePath+"/"+onlyA2.ID.String(), op, nil, nil)
	a.expect(200, "GET", maintenancePath+"/"+byOrgA.ID.String(), op, nil, nil)
	a.expect(403, "POST", maintenancePath+"/"+byOrgA.ID.String()+"/skip-next", op, nil, nil)
}

func keys(m map[string]maintenanceJSON) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

// "Bu tekrarı bitir" süren tekrarı, "sıradaki tekrarı atla" yaklaşan ilk tekrarı, "pencereyi bitir" seriyi kapatır;
// uygulanamadıklarında 409 döner.
func TestMaintenanceEndSkipAndEndOccurrence(t *testing.T) {
	a := newAPI(t)
	root, _ := a.login("root@x.test", "super_admin")
	setTimezone(a, root, "Europe/Istanbul")
	ist, err := time.LoadLocation("Europe/Istanbul")
	if err != nil {
		t.Fatal(err)
	}
	host := a.createPushHost(root, a.createOrg(root, "A"), "h")
	now := time.Now().In(ist)
	local := func(t time.Time) string { return t.In(ist).Format("2006-01-02T15:04") }

	var running maintenanceJSON
	a.expect(201, "POST", maintenancePath, root, once("şimdi", local(now.Add(-time.Hour)), local(now.Add(time.Hour)), []uuid.UUID{host.ID}, nil), &running)
	if running.Status != "active" || running.Current == nil {
		t.Fatalf("running one-off window = %+v", running)
	}
	var cut maintenanceJSON
	a.expect(200, "POST", maintenancePath+"/"+running.ID.String()+"/end-occurrence", root, nil, &cut)
	if cut.Status != "past" || cut.Current != nil || cut.Last == nil || cut.Last.StartLocal != local(now.Add(-time.Hour)) {
		t.Fatalf("after end-occurrence = %+v, want past with the occurrence as the last one", cut)
	}
	a.expect(409, "POST", maintenancePath+"/"+running.ID.String()+"/end-occurrence", root, nil, nil)

	tomorrow := now.AddDate(0, 0, 1).Format("2006-01-02")
	var daily maintenanceJSON
	a.expect(201, "POST", maintenancePath, root, map[string]any{"title": "gece", "recurrence": "daily", "start_time": "02:00",
		"duration_minutes": 60, "valid_from": tomorrow, "host_ids": []uuid.UUID{host.ID}}, &daily)
	first := daily.Upcoming[0].StartLocal
	if first != tomorrow+"T02:00" || daily.Status != "scheduled" {
		t.Fatalf("daily = %+v", daily)
	}
	a.expect(409, "POST", maintenancePath+"/"+daily.ID.String()+"/end-occurrence", root, nil, nil)
	var skipped maintenanceJSON
	a.expect(200, "POST", maintenancePath+"/"+daily.ID.String()+"/skip-next", root, nil, &skipped)
	if skipped.Next == nil || skipped.Next.StartLocal != now.AddDate(0, 0, 2).Format("2006-01-02")+"T02:00" || slices.Contains(localStarts(skipped.Upcoming), first) {
		t.Fatalf("after skip-next: next %+v upcoming %v", skipped.Next, localStarts(skipped.Upcoming))
	}
	if !slices.Equal(skipped.SkippedLocal, []string{first}) {
		t.Fatalf("skipped_local = %v, want [%s]", skipped.SkippedLocal, first)
	}

	var ended maintenanceJSON
	a.expect(200, "POST", maintenancePath+"/"+daily.ID.String()+"/end", root, nil, &ended)
	if ended.EndedAt == nil || ended.Status != "past" || ended.Next != nil || !ended.CanManage {
		t.Fatalf("after end = %+v", ended)
	}
	a.expect(409, "POST", maintenancePath+"/"+daily.ID.String()+"/end", root, nil, nil)
	a.expect(409, "POST", maintenancePath+"/"+daily.ID.String()+"/skip-next", root, nil, nil)
	a.expect(409, "PUT", maintenancePath+"/"+daily.ID.String(), root, once("x", "2099-10-12T01:00", "2099-10-12T02:00", []uuid.UUID{host.ID}, nil), nil)
	for action, want := range map[string]int{"maintenance.end": 1, "maintenance.skip": 1, "maintenance.end_occurrence": 1} {
		if n := a.auditCount(action); n != want {
			t.Errorf("%s audit entries = %d, want %d", action, n, want)
		}
	}
}

// Yaz saati olan bir kurulumda hiç yaşanmayan yerel saat ileri kayar; yanıt kaymış saati gösterir.
func TestMaintenanceLocalTimeInDaylightSavingGap(t *testing.T) {
	a := newAPI(t)
	root, _ := a.login("root@x.test", "super_admin")
	setTimezone(a, root, "Europe/Berlin")
	host := a.createPushHost(root, a.createOrg(root, "A"), "h")
	var w maintenanceJSON
	a.expect(201, "POST", maintenancePath, root, once("geçiş", "2026-03-29T02:30", "2026-03-29T05:00", []uuid.UUID{host.ID}, nil), &w)
	if !w.StartsAt.Equal(time.Date(2026, 3, 29, 1, 30, 0, 0, time.UTC)) || *w.StartsLocal != "2026-03-29T03:30" {
		t.Fatalf("02:30 in the gap = %v / %s, want 01:30 UTC shown as 03:30", w.StartsAt, *w.StartsLocal)
	}
	if !strings.HasPrefix(*w.EndsLocal, "2026-03-29T05:00") {
		t.Fatalf("ends_local = %s", *w.EndsLocal)
	}
}

// Bakımdaki sunucunun Özet/Sunucular listesinde ve ayrıntısında kesintisiz bakımın bitişi (yerel saatiyle) bulunur;
// bakımda olmayan sunucuda alan yoktur. Pencere bitirilince alan kalkar.
func TestHostResponsesCarryMaintenanceUntil(t *testing.T) {
	a := newAPI(t)
	root, _ := a.login("root@x.test", "super_admin")
	setTimezone(a, root, "Europe/Istanbul")
	ist, err := time.LoadLocation("Europe/Istanbul")
	if err != nil {
		t.Fatal(err)
	}
	org := a.createOrg(root, "A")
	busy, idle := a.createPushHost(root, org, "busy"), a.createPushHost(root, org, "idle")
	now := time.Now().In(ist)
	end := now.Add(2 * time.Hour).Truncate(time.Minute)
	var w maintenanceJSON
	a.expect(201, "POST", maintenancePath, root, once("şimdi", now.Add(-time.Hour).Format("2006-01-02T15:04"), end.Format("2006-01-02T15:04"),
		[]uuid.UUID{busy.ID}, nil), &w)

	type hostJSON struct {
		ID         uuid.UUID  `json:"id"`
		Until      *time.Time `json:"maintenance_until"`
		UntilLocal *string    `json:"maintenance_until_local"`
	}
	var ov struct {
		Hosts []hostJSON `json:"hosts"`
	}
	a.expect(200, "GET", "/api/v1/dashboard/overview", root, nil, &ov)
	for _, h := range ov.Hosts {
		switch h.ID {
		case busy.ID:
			if h.Until == nil || !h.Until.Equal(end) || *h.UntilLocal != end.Format("2006-01-02T15:04") {
				t.Errorf("busy host in the overview = %+v, want until %s", h, end)
			}
		case idle.ID:
			if h.Until != nil {
				t.Errorf("idle host carries maintenance_until %v", h.Until)
			}
		}
	}
	var detail hostJSON
	a.expect(200, "GET", "/api/v1/hosts/"+busy.ID.String(), root, nil, &detail)
	if detail.Until == nil || !detail.Until.Equal(end) {
		t.Errorf("host detail = %+v", detail)
	}

	a.expect(200, "POST", maintenancePath+"/"+w.ID.String()+"/end", root, nil, nil)
	detail = hostJSON{}
	a.expect(200, "GET", "/api/v1/hosts/"+busy.ID.String(), root, nil, &detail)
	if detail.Until != nil {
		t.Errorf("after ending the window: maintenance_until = %v", detail.Until)
	}
}
