package httpapi_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"

	"healthbeat-server/internal/model"
	"healthbeat-server/internal/testdb"
)

type hostServices struct {
	Services []model.HostService `json:"services"`
	Watched  []string            `json:"watched"`
}

func (a *api) pushFixture(c createdHost, name string) {
	a.t.Helper()
	raw, err := os.ReadFile("../../testdata/payloads/" + name)
	if err != nil {
		a.t.Fatal(err)
	}
	if code := a.push(c, c.APIToken, json.RawMessage(raw)); code != 204 {
		a.t.Fatalf("push %s = %d", name, code)
	}
}

// Protokol 4 verisi API'de ölçüldüğü gibi döner; eski agent'ın sunucusunda yeni alanlar hiç yoktur.
func TestProtocol4DataInAPI(t *testing.T) {
	a := newAPI(t)
	root, _ := a.login("root@x.test", "super_admin")
	org := a.createOrg(root, "A")
	v4 := a.createPushHost(root, org, "v4")
	v3 := a.createPushHost(root, org, "v3")
	a.pushFixture(v4, "v4_health_performance.json")
	a.pushFixture(v3, "v3_inventory.json")
	path := func(c createdHost, rest string) string { return "/api/v1/hosts/" + c.ID.String() + rest }

	// Metrikler: son nokta ve geçmiş aynı biçimde.
	var latest map[string]json.RawMessage
	a.expect(200, "GET", path(v4, "/metrics/latest"), root, nil, &latest)
	var system struct {
		CPUDetail struct {
			IOWait float64 `json:"iowait_pct"`
		} `json:"cpu_detail"`
		MemoryDetail map[string]any `json:"memory_detail"`
	}
	var diskIO []struct {
		Name    string  `json:"name"`
		AwaitMs float64 `json:"await_ms"`
	}
	var netIO []struct {
		RxBps float64 `json:"rx_bps"`
	}
	if err := json.Unmarshal(latest["system"], &system); err != nil || system.CPUDetail.IOWait != 0.1670843776106934 {
		t.Fatalf("latest system = %s (%v)", latest["system"], err)
	}
	if _, ok := system.MemoryDetail["oom_kills"]; ok {
		t.Error("oom_kills is in the time series; it belongs to system_state")
	}
	if err := json.Unmarshal(latest["disk_io"], &diskIO); err != nil || len(diskIO) != 1 || diskIO[0].AwaitMs != 0.8333333333333334 {
		t.Fatalf("latest disk_io = %s (%v)", latest["disk_io"], err)
	}
	if err := json.Unmarshal(latest["net_io"], &netIO); err != nil || len(netIO) != 2 || netIO[0].RxBps != 10458.774401897601 {
		t.Fatalf("latest net_io = %s (%v)", latest["net_io"], err)
	}
	var history []map[string]json.RawMessage
	a.expect(200, "GET", path(v4, "/metrics"), root, nil, &history)
	if len(history) != 1 || string(history[0]["disk_io"]) != string(latest["disk_io"]) {
		t.Fatalf("history = %v", history)
	}
	var old map[string]json.RawMessage
	a.expect(200, "GET", path(v3, "/metrics/latest"), root, nil, &old)
	for _, k := range []string{"system", "disk_io", "net_io"} {
		if v, ok := old[k]; ok {
			t.Errorf("protocol 3 metric has %s = %s, want it absent", k, v)
		}
	}

	// Anlık durumlar yalnızca tek sunucu yanıtında.
	var host struct {
		SystemState *struct {
			OOMKills     *uint64 `json:"oom_kills"`
			Temperatures []struct {
				Celsius float64 `json:"celsius"`
			} `json:"temperatures"`
			TimeSync *struct {
				OffsetMs float64 `json:"offset_ms"`
			} `json:"time_sync"`
			Updates *struct {
				Security int `json:"security"`
			} `json:"updates"`
		} `json:"system_state"`
	}
	a.expect(200, "GET", path(v4, ""), root, nil, &host)
	st := host.SystemState
	if st == nil || st.OOMKills == nil || *st.OOMKills != 2 || len(st.Temperatures) != 2 || st.Temperatures[0].Celsius != 32.85 ||
		st.TimeSync == nil || st.TimeSync.OffsetMs != 2.2285 || st.Updates == nil || st.Updates.Security != 3 {
		t.Fatalf("system_state = %+v", st)
	}
	var oldHost map[string]json.RawMessage
	a.expect(200, "GET", path(v3, ""), root, nil, &oldHost)
	if v, ok := oldHost["system_state"]; ok {
		t.Errorf("protocol 3 host has system_state = %s", v)
	}
	var list []map[string]json.RawMessage
	a.expect(200, "GET", "/api/v1/organizations/"+org.String()+"/hosts", root, nil, &list)
	for _, h := range list {
		if _, ok := h["system_state"]; ok {
			t.Error("host list carries system_state; it is only in the single-host response")
		}
	}

	// Docker: sağlık, çıkış kodu ve OOM.
	var containers []struct {
		Name      string `json:"name"`
		Health    string `json:"health"`
		Streak    *int   `json:"health_failing_streak"`
		ExitCode  *int   `json:"exit_code"`
		OOMKilled *bool  `json:"oom_killed"`
	}
	a.expect(200, "GET", path(v4, "/docker"), root, nil, &containers)
	got := map[string]string{}
	for _, c := range containers {
		s := c.Health
		if c.Streak != nil {
			s += fmt.Sprintf("/streak=%d", *c.Streak)
		}
		if c.ExitCode != nil {
			s += fmt.Sprintf("/exit=%d", *c.ExitCode)
		}
		if c.OOMKilled != nil {
			s += fmt.Sprintf("/oom=%v", *c.OOMKilled)
		}
		got[c.Name] = s
	}
	if got["api"] != "healthy/streak=0" || got["db"] != "unhealthy/streak=3" || got["migrate"] != "/exit=137/oom=true" {
		t.Fatalf("containers = %v", got)
	}

	// Servisler: agent'ın bildirdiği liste, henüz izlenen yok; eski agent'ta liste boş.
	var svc hostServices
	a.expect(200, "GET", path(v4, "/services"), root, nil, &svc)
	if len(svc.Services) != 3 || svc.Services[1].Name != "nginx.service" || svc.Services[1].Active != "failed" ||
		svc.Services[1].Restarts == nil || *svc.Services[1].Restarts != 5 || svc.Services[1].Since == nil || svc.Services[1].Watched ||
		svc.Watched == nil || len(svc.Watched) != 0 {
		t.Fatalf("services = %+v", svc)
	}
	var none map[string]json.RawMessage
	a.expect(200, "GET", path(v3, "/services"), root, nil, &none)
	if string(none["services"]) != "[]" || string(none["watched"]) != "[]" {
		t.Fatalf("protocol 3 services = %v, want empty lists", none)
	}
}

func TestWatchedServicesSelection(t *testing.T) {
	a := newAPI(t)
	root, _ := a.login("root@x.test", "super_admin")
	c := a.createPushHost(root, a.createOrg(root, "A"), "web-1")
	a.pushFixture(c, "v4_health_performance.json")
	path := "/api/v1/hosts/" + c.ID.String() + "/watched-services"
	servicesPath := "/api/v1/hosts/" + c.ID.String() + "/services"

	// Raporlanmayan bir servis de seçilebilir; liste sıralı döner.
	var s hostServices
	a.expect(200, "PUT", path, root, map[string]any{"services": []string{"nginx.service", "gone.service"}}, &s)
	if strings.Join(s.Watched, ",") != "gone.service,nginx.service" {
		t.Fatalf("watched = %v", s.Watched)
	}
	watched := map[string]bool{}
	for _, sv := range s.Services {
		watched[sv.Name] = sv.Watched
	}
	if !watched["nginx.service"] || watched["cron.service"] || len(s.Services) != 3 {
		t.Fatalf("services after PUT = %+v", s.Services)
	}
	if a.auditCount("host.update_watched_services") != 1 {
		t.Errorf("audit rows = %d, want 1", a.auditCount("host.update_watched_services"))
	}

	// Geçersiz istekler reddedilir ve seçimi değiştirmez.
	var e struct {
		Error string `json:"error"`
	}
	if code := a.call("PUT", path, root, map[string]any{}, &e); code != 400 || !strings.Contains(e.Error, `"services" zorunlu`) {
		t.Errorf("missing key: %d %q", code, e.Error)
	}
	tooMany := make([]string, 257)
	for i := range tooMany {
		tooMany[i] = fmt.Sprintf("s%d.service", i)
	}
	for name, body := range map[string]any{
		"wrong type":   map[string]any{"services": "nginx.service"},
		"duplicate":    map[string]any{"services": []string{"a", "a"}},
		"empty name":   map[string]any{"services": []string{" "}},
		"control char": map[string]any{"services": []string{"a\u0000b"}},
		"too long":     map[string]any{"services": []string{strings.Repeat("x", 257)}},
		"too many":     map[string]any{"services": tooMany},
	} {
		if code := a.call("PUT", path, root, body, nil); code != 400 {
			t.Errorf("%s: %d, want 400", name, code)
		}
	}
	a.expect(200, "GET", servicesPath, root, nil, &s)
	if strings.Join(s.Watched, ",") != "gone.service,nginx.service" {
		t.Fatalf("a rejected request changed the selection: %v", s.Watched)
	}

	// Boş liste: hiçbiri.
	a.expect(200, "PUT", path, root, map[string]any{"services": []string{}}, &s)
	if len(s.Watched) != 0 || s.Services[1].Watched {
		t.Fatalf("after clearing: %+v", s)
	}
	a.expect(400, "PUT", "/api/v1/hosts/not-a-uuid/watched-services", root, map[string]any{"services": []string{}}, nil)
	a.expect(404, "PUT", "/api/v1/hosts/"+uuid.NewString()+"/watched-services", root, map[string]any{"services": []string{}}, nil)
	a.expect(404, "GET", "/api/v1/hosts/"+uuid.NewString()+"/services", root, nil, nil)
}

func TestWatchedServicesPermissions(t *testing.T) {
	a := newAPI(t)
	root, _ := a.login("root@x.test", "super_admin")
	adminTok, adminID := a.login("oa@x.test", "org_admin")
	opTok, opID := a.login("op@x.test", "operator")
	otherOp, _ := a.login("op2@x.test", "operator")
	orgA, orgB := a.createOrg(root, "A"), a.createOrg(root, "B")
	testdb.AssignOrg(t, a.pool, adminID, orgA)
	mine := a.createPushHost(root, orgA, "a-1")
	theirs := a.createPushHost(root, orgB, "b-1")
	testdb.AssignHost(t, a.pool, opID, mine.ID)
	body := map[string]any{"services": []string{"nginx.service"}}
	put := func(c createdHost) string { return "/api/v1/hosts/" + c.ID.String() + "/watched-services" }
	get := func(c createdHost) string { return "/api/v1/hosts/" + c.ID.String() + "/services" }

	a.expect(200, "PUT", put(mine), root, body, nil)
	a.expect(200, "PUT", put(mine), adminTok, body, nil)
	a.expect(403, "PUT", put(theirs), adminTok, body, nil)
	a.expect(403, "GET", get(theirs), adminTok, nil, nil)
	// Operatör kendi sunucusunun servislerini görür, seçimi değiştiremez.
	a.expect(200, "GET", get(mine), opTok, nil, nil)
	a.expect(403, "PUT", put(mine), opTok, body, nil)
	a.expect(403, "GET", get(mine), otherOp, nil, nil)
	a.expect(401, "GET", get(mine), "", nil, nil)
	a.expect(401, "PUT", put(mine), "", body, nil)

	// İki katman da kendi başına geçerli (rol izinleri panelden değiştirilebilir): host.update izni olmayan
	// org_admin değiştiremez; host.update verilmiş operatör de organizasyonu yönetmediği için değiştiremez.
	if _, err := a.pool.Exec(context.Background(), `DELETE FROM role_permissions WHERE role = 'org_admin' AND permission_key = 'host.update'`); err != nil {
		t.Fatal(err)
	}
	if _, err := a.pool.Exec(context.Background(), `INSERT INTO role_permissions (role, permission_key) VALUES ('operator', 'host.update')`); err != nil {
		t.Fatal(err)
	}
	a.deps.ResetPermissionCache()
	a.expect(403, "PUT", put(mine), adminTok, body, nil)
	a.expect(403, "PUT", put(mine), opTok, body, nil)
	a.expect(200, "GET", get(mine), adminTok, nil, nil)

	if n := a.auditCount("host.update_watched_services"); n != 2 {
		t.Errorf("audit rows = %d, want 2 (only the accepted changes)", n)
	}
}
