package httpapi_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/uuid"

	"healthbeat-server/internal/testdb"
)

type thresholdLevels struct {
	Warning  float64 `json:"warning_level"`
	Critical float64 `json:"critical_level"`
}

type thresholdView struct {
	MetricType string           `json:"metric_type"`
	Default    *thresholdLevels `json:"default"`
	Custom     *thresholdLevels `json:"custom"`
}

func (a *api) hostThresholds(token string, id uuid.UUID) map[string]thresholdView {
	a.t.Helper()
	var resp struct {
		Thresholds []thresholdView `json:"thresholds"`
	}
	a.expect(200, "GET", "/api/v1/hosts/"+id.String()+"/thresholds", token, nil, &resp)
	out := map[string]thresholdView{}
	for _, v := range resp.Thresholds {
		out[v.MetricType] = v
	}
	if len(out) != 4 || len(resp.Thresholds) != 4 {
		a.t.Fatalf("expected one entry for each of the four threshold metrics, got %+v", resp.Thresholds)
	}
	return out
}

func (a *api) setThreshold(token, metric string, warn, crit float64) {
	a.t.Helper()
	a.expect(201, "POST", "/api/v1/thresholds", token, map[string]any{"metric_type": metric, "warning_level": warn, "critical_level": crit}, nil)
}

// Özel değeri olmayan host global varsayılanı izler; kendi değerleri olan host onları
// izler ve onları kaldırmak varsayılana geri döndürür.
func TestCustomThresholdOverridesTheDefaultForOneHostOnly(t *testing.T) {
	a := newAPI(t)
	root, _ := a.login("root@x.test", "super_admin")
	org := a.createOrg(root, "A")
	x := a.createPushHost(root, org, "x")
	y := a.createPushHost(root, org, "y")
	a.setThreshold(root, "cpu", 50, 90) // varsayılan

	openAlerts := func() (n int) {
		var open []struct {
			HostID uuid.UUID `json:"host_id"`
		}
		a.expect(200, "GET", "/api/v1/alerts?status=open", root, nil, &open)
		return len(open)
	}
	openFor := func(c createdHost) bool {
		var open []struct {
			HostID uuid.UUID `json:"host_id"`
		}
		a.expect(200, "GET", "/api/v1/alerts?status=open", root, nil, &open)
		for _, al := range open {
			if al.HostID == c.ID {
				return true
			}
		}
		return false
	}

	a.expect(200, "PUT", "/api/v1/hosts/"+x.ID.String()+"/thresholds", root,
		map[string]any{"thresholds": map[string]any{"cpu": map[string]any{"warning_level": 20, "critical_level": 30}}}, nil)

	// Aynı okuma (%40 cpu) x için kritik (özel 30), y için sorun değil (varsayılan 90).
	a.expect(204, "POST", "/api/v1/metrics", x.APIToken, metrics(40, 10), nil, "X-Host-ID", x.ID.String())
	a.expect(204, "POST", "/api/v1/metrics", y.APIToken, metrics(40, 10), nil, "X-Host-ID", y.ID.String())
	if !openFor(x) || openFor(y) || openAlerts() != 1 {
		t.Fatalf("x alerting=%v y alerting=%v, want only x (custom threshold)", openFor(x), openFor(y))
	}

	// Varsayılan satırın kendisine x'in özel değerleri dokunmaz.
	tx, ty := a.hostThresholds(root, x.ID)["cpu"], a.hostThresholds(root, y.ID)["cpu"]
	if tx.Custom == nil || *tx.Custom != (thresholdLevels{20, 30}) || tx.Default == nil || *tx.Default != (thresholdLevels{50, 90}) {
		t.Fatalf("x cpu = %+v", tx)
	}
	if ty.Custom != nil || ty.Default == nil || *ty.Default != (thresholdLevels{50, 90}) {
		t.Fatalf("y cpu = %+v, want default only", ty)
	}

	// Özel değeri bırakmak x'i varsayılana döndürür: sonraki okuma alert'i çözer.
	a.expect(200, "PUT", "/api/v1/hosts/"+x.ID.String()+"/thresholds", root,
		map[string]any{"thresholds": map[string]any{"cpu": nil}}, nil)
	a.expect(204, "POST", "/api/v1/metrics", x.APIToken, metrics(40, 10), nil, "X-Host-ID", x.ID.String())
	if openFor(x) {
		t.Fatal("x still alerts after its custom threshold was removed")
	}
}

func TestHostThresholdsRequestValidationAndAtomicity(t *testing.T) {
	a := newAPI(t)
	root, _ := a.login("root@x.test", "super_admin")
	org := a.createOrg(root, "A")
	c := a.createPushHost(root, org, "web-1")
	path := "/api/v1/hosts/" + c.ID.String() + "/thresholds"
	ok := func(w, c float64) map[string]any { return map[string]any{"warning_level": w, "critical_level": c} }

	// Hiçbir yerde yapılandırma yok: dört metrik, varsayılan yok, özel yok.
	for metric, v := range a.hostThresholds(root, c.ID) {
		if v.Default != nil || v.Custom != nil {
			t.Fatalf("%s starts with %+v, want nothing", metric, v)
		}
	}

	var e struct {
		Error string `json:"error"`
	}
	if code := a.call("PUT", path, root, map[string]any{}, &e); code != 400 || !strings.Contains(e.Error, `"thresholds" zorunlu`) {
		t.Errorf("missing key: %d %q", code, e.Error)
	}
	bad := map[string]map[string]any{
		"unknown metric":     {"host_offline": ok(1, 2)},
		"misspelled metric":  {"cpuu": ok(1, 2)},
		"warning > critical": {"cpu": ok(90, 80)},
		"percent over 100":   {"ram": ok(50, 101)},
		"negative":           {"disk": ok(-1, 50)},
		"only warning":       {"cpu": map[string]any{"warning_level": 50}},
		"only critical":      {"cpu": map[string]any{"critical_level": 50}},
		"restarts too high":  {"docker_restart": ok(1, 2_000_000)},
		"wrong type":         {"cpu": "80"},
	}
	for name, thresholds := range bad {
		if got := a.call("PUT", path, root, map[string]any{"thresholds": thresholds}, &e); got != 400 {
			t.Errorf("%s: %d, want 400", name, got)
		}
	}
	// Tek kötü girdi tüm isteği reddeder: geçerli cpu değeri UYGULANMAMIŞ olmalı.
	if got := a.call("PUT", path, root, map[string]any{"thresholds": map[string]any{"cpu": ok(60, 70), "ram": ok(90, 80)}}, nil); got != 400 {
		t.Fatalf("mixed valid+invalid: %d, want 400", got)
	}
	if v := a.hostThresholds(root, c.ID)["cpu"]; v.Custom != nil {
		t.Fatalf("a rejected request still applied cpu: %+v", v)
	}
	a.expect(400, "PUT", "/api/v1/hosts/not-a-uuid/thresholds", root, map[string]any{"thresholds": map[string]any{}}, nil)
	a.expect(404, "PUT", "/api/v1/hosts/"+uuid.NewString()+"/thresholds", root, map[string]any{"thresholds": map[string]any{}}, nil)
	a.expect(404, "GET", "/api/v1/hosts/"+uuid.NewString()+"/thresholds", root, nil, nil)

	// Dışarıda bırakılan metriklere dokunulmaz; null yalnızca o metriği kaldırır.
	a.expect(200, "PUT", path, root, map[string]any{"thresholds": map[string]any{"cpu": ok(60, 70), "ram": ok(65, 75)}}, nil)
	a.expect(200, "PUT", path, root, map[string]any{"thresholds": map[string]any{"disk": ok(80, 95)}}, nil)
	got := a.hostThresholds(root, c.ID)
	if got["cpu"].Custom == nil || got["ram"].Custom == nil || got["disk"].Custom == nil || got["docker_restart"].Custom != nil {
		t.Fatalf("after two partial updates: %+v", got)
	}
	a.expect(200, "PUT", path, root, map[string]any{"thresholds": map[string]any{"ram": nil}}, nil)
	got = a.hostThresholds(root, c.ID)
	if got["ram"].Custom != nil || got["cpu"].Custom == nil || *got["cpu"].Custom != (thresholdLevels{60, 70}) {
		t.Fatalf("null on ram must remove only ram: %+v", got)
	}

	// Değeri değiştirmek bir güncellemedir, çakışma değil ve metrik başına hâlâ tam bir satır var.
	a.expect(200, "PUT", path, root, map[string]any{"thresholds": map[string]any{"cpu": ok(61, 71)}}, nil)
	var rows int
	if err := a.pool.QueryRow(context.Background(), `SELECT count(*) FROM host_custom_thresholds WHERE host_id = $1 AND metric_type = 'cpu'`, c.ID).Scan(&rows); err != nil || rows != 1 {
		t.Fatalf("cpu rows for the host = %d (err %v), want 1", rows, err)
	}
	if v := a.hostThresholds(root, c.ID)["cpu"]; *v.Custom != (thresholdLevels{61, 71}) {
		t.Fatalf("cpu = %+v", v)
	}
	if n := a.auditCount("host.update_thresholds"); n != 4 {
		t.Errorf("host.update_thresholds audit rows = %d, want 4 (the accepted PUTs; rejected ones must not be audited)", n)
	}
}

func TestHostThresholdPermissions(t *testing.T) {
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
	body := map[string]any{"thresholds": map[string]any{"cpu": map[string]any{"warning_level": 10, "critical_level": 20}}}
	p := func(c createdHost) string { return "/api/v1/hosts/" + c.ID.String() + "/thresholds" }

	a.expect(200, "PUT", p(mine), root, body, nil)
	a.expect(200, "PUT", p(mine), adminTok, body, nil)
	a.expect(403, "PUT", p(theirs), adminTok, body, nil)
	a.expect(403, "GET", p(theirs), adminTok, nil, nil)
	// Bir operatör atanmış bir host'ın eşiklerini okuyabilir ve hiçbir şeyi değiştiremez.
	a.expect(200, "GET", p(mine), opTok, nil, nil)
	a.expect(403, "PUT", p(mine), opTok, body, nil)
	a.expect(403, "GET", p(theirs), opTok, nil, nil)
	a.expect(403, "GET", p(mine), otherOp, nil, nil)
	a.expect(401, "GET", p(mine), "", nil, nil)
	a.expect(401, "PUT", p(mine), "", body, nil)
	if v := a.hostThresholds(root, theirs.ID)["cpu"]; v.Custom != nil {
		t.Fatalf("a forbidden PUT changed another organization's host: %+v", v)
	}
}

func TestCreatingAHostWithCustomThresholds(t *testing.T) {
	a := newAPI(t)
	root, _ := a.login("root@x.test", "super_admin")
	adminTok, adminID := a.login("oa@x.test", "org_admin")
	org := a.createOrg(root, "A")
	testdb.AssignOrg(t, a.pool, adminID, org)
	ok := func(w, c float64) map[string]any { return map[string]any{"warning_level": w, "critical_level": c} }
	create := func(token string, thresholds any) (int, createdHost) {
		body := map[string]any{"organization_id": org, "title": "h" + uuid.NewString()[:4], "ip": "10.0.0.1", "mode": "push", "interval_seconds": 10}
		if thresholds != nil {
			body["thresholds"] = thresholds
		}
		var c createdHost
		return a.call("POST", "/api/v1/hosts", token, body, &c), c
	}
	hostCount := func() (n int) {
		if err := a.pool.QueryRow(context.Background(), `SELECT count(*) FROM hosts`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}

	a.setThreshold(root, "ram", 70, 85)
	code, c := create(root, map[string]any{"cpu": ok(30, 40), "ram": nil, "disk": ok(80, 95)})
	if code != 201 {
		t.Fatalf("create with thresholds: %d", code)
	}
	got := a.hostThresholds(root, c.ID)
	if got["cpu"].Custom == nil || *got["cpu"].Custom != (thresholdLevels{30, 40}) || got["disk"].Custom == nil {
		t.Fatalf("custom values not stored: %+v", got)
	}
	if got["ram"].Custom != nil || got["ram"].Default == nil {
		t.Fatalf("null entry must mean default: %+v", got["ram"])
	}

	// Verilmemiş / boş / hepsi null: hepsi "her şey varsayılan" demektir.
	for name, th := range map[string]any{"omitted": nil, "empty": map[string]any{}, "all null": map[string]any{"cpu": nil}} {
		code, c := create(root, th)
		if code != 201 {
			t.Fatalf("%s: %d", name, code)
		}
		for m, v := range a.hostThresholds(root, c.ID) {
			if v.Custom != nil {
				t.Errorf("%s: %s has custom %+v", name, m, v.Custom)
			}
		}
	}

	// Kötü bir eşik tüm oluşturmayı reddeder: geride host satırı kalmaz.
	before := hostCount()
	for name, th := range map[string]any{
		"warning > critical": map[string]any{"cpu": ok(90, 80)},
		"unknown metric":     map[string]any{"nope": ok(1, 2)},
		"half a pair":        map[string]any{"cpu": map[string]any{"warning_level": 5}},
	} {
		if code, _ := create(root, th); code != 400 {
			t.Errorf("%s: %d, want 400", name, code)
		}
	}
	if hostCount() != before {
		t.Fatalf("a rejected creation left a host behind (%d -> %d)", before, hostCount())
	}

	// Özel eşikler host.create'in yanı sıra threshold.edit de gerektirir. Onsuz yalnızca eşik
	// kısmı reddedilir; aynı host'ı varsayılanlarla oluşturmak yine çalışır.
	if _, err := a.pool.Exec(context.Background(), `DELETE FROM role_permissions WHERE role = 'org_admin' AND permission_key = 'threshold.edit'`); err != nil {
		t.Fatal(err)
	}
	if code, _ := create(adminTok, map[string]any{"cpu": ok(30, 40)}); code != 403 {
		t.Errorf("org_admin without threshold.edit set custom thresholds: %d, want 403", code)
	}
	if code, _ := create(adminTok, map[string]any{"cpu": nil}); code != 201 {
		t.Errorf("org_admin without threshold.edit, nothing custom: %d, want 201", code)
	}
	if code, _ := create(adminTok, nil); code != 201 {
		t.Errorf("org_admin without threshold.edit, no thresholds: %d, want 201", code)
	}
}

func TestHostCreateAuditRecordsItsThresholds(t *testing.T) {
	a := newAPI(t)
	root, _ := a.login("root@x.test", "super_admin")
	org := a.createOrg(root, "A")
	var c createdHost
	a.expect(201, "POST", "/api/v1/hosts", root, map[string]any{
		"organization_id": org, "title": "h", "ip": "10.0.0.1", "mode": "push", "interval_seconds": 10,
		"thresholds": map[string]any{"cpu": map[string]any{"warning_level": 30, "critical_level": 40}},
	}, &c)
	var details json.RawMessage
	if err := a.pool.QueryRow(context.Background(), `SELECT details FROM audit_logs WHERE action = 'host.create' AND target_id = $1`, c.ID.String()).Scan(&details); err != nil {
		t.Fatal(err)
	}
	var parsed struct {
		Thresholds map[string]thresholdLevels `json:"thresholds"`
	}
	if err := json.Unmarshal(details, &parsed); err != nil || parsed.Thresholds["cpu"] != (thresholdLevels{30, 40}) {
		t.Fatalf("host.create audit details = %s (err %v), want the cpu 30/40 threshold", details, err)
	}
}
