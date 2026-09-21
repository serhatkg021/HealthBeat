package httpapi_test

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"

	"healthbeat-server/internal/testdb"
)

func (a *api) containerThresholds(token string, id uuid.UUID) map[string]thresholdLevels {
	a.t.Helper()
	var resp struct {
		Containers []struct {
			Container string          `json:"container"`
			Custom    thresholdLevels `json:"custom"`
		} `json:"container_thresholds"`
	}
	a.expect(200, "GET", "/api/v1/hosts/"+id.String()+"/thresholds", token, nil, &resp)
	out := map[string]thresholdLevels{}
	for _, c := range resp.Containers {
		out[c.Container] = c.Custom
	}
	return out
}

func (a *api) pushContainers(c createdHost, counts map[string]int) {
	a.t.Helper()
	var cs []map[string]any
	for n, r := range counts {
		cs = append(cs, map[string]any{"name": n, "image": n + ":1", "status": "running", "cpu_pct": 1, "ram_mb": 1, "restart_count": r, "uptime_seconds": 1})
	}
	a.expect(204, "POST", "/api/v1/metrics", c.APIToken,
		map[string]any{"cpu_usage_pct": 1, "ram_usage_pct": 1, "disk": []any{}, "docker_containers": cs}, nil, "X-Host-ID", c.ID.String())
}

func (a *api) openRestartAlerts(token string) map[string]string { // container -> seviye
	a.t.Helper()
	var open []struct {
		AlertType string `json:"alert_type"`
		Subject   string `json:"subject"`
		Level     string `json:"level"`
	}
	a.expect(200, "GET", "/api/v1/alerts?status=open", token, nil, &open)
	out := map[string]string{}
	for _, al := range open {
		if al.AlertType == "docker_restart" {
			out[al.Subject] = al.Level
		}
	}
	return out
}

// "web" 3/10'da uyarır, "batch" 50/100'e kadar rahatça yeniden başlayabilir; sunucuda ayarlanır ve
// sunucu rapor verince uygulanır.
func TestPerContainerRestartThresholdsEndToEndOverTheAPI(t *testing.T) {
	a := newAPI(t)
	root, _ := a.login("root@x.test", "super_admin")
	org := a.createOrg(root, "A")
	c := a.createPushHost(root, org, "web-host")
	path := "/api/v1/hosts/" + c.ID.String() + "/thresholds"
	a.setThreshold(root, "docker_restart", 3, 10) // varsayılan

	if got := a.containerThresholds(root, c.ID); len(got) != 0 {
		t.Fatalf("a new host starts with no container thresholds: %v", got)
	}
	a.expect(200, "PUT", path, root, map[string]any{
		"thresholds":           map[string]any{},
		"container_thresholds": map[string]any{"batch": map[string]any{"warning_level": 50, "critical_level": 100}},
	}, nil)
	if got := a.containerThresholds(root, c.ID); len(got) != 1 || got["batch"] != (thresholdLevels{50, 100}) {
		t.Fatalf("container thresholds = %v", got)
	}
	if v := a.hostThresholds(root, c.ID)["docker_restart"]; v.Custom != nil {
		t.Fatalf("a container threshold turned into the host's docker_restart threshold: %+v", v)
	}
	if got := a.mountThresholds(root, c.ID); len(got) != 0 {
		t.Fatalf("a container threshold showed up as a mount threshold: %v", got)
	}

	a.pushContainers(c, map[string]int{"web": 5, "batch": 5})
	if got := a.openRestartAlerts(root); len(got) != 1 || got["web"] != "warning" {
		t.Fatalf("alerts = %v, want only web (warning): batch is under its own 50", got)
	}
	a.pushContainers(c, map[string]int{"web": 5, "batch": 60})
	if got := a.openRestartAlerts(root); got["batch"] != "warning" {
		t.Fatalf("batch at 60 is over its own warning 50: %v", got)
	}

	// null yalnızca o container'ın özel eşiğini kaldırır; batch artık varsayılan 3/10'u izler.
	a.expect(200, "PUT", path, root, map[string]any{"thresholds": map[string]any{}, "container_thresholds": map[string]any{"batch": nil}}, nil)
	if got := a.containerThresholds(root, c.ID); len(got) != 0 {
		t.Fatalf("after removing batch: %v", got)
	}
	a.pushContainers(c, map[string]int{"web": 5, "batch": 60})
	if got := a.openRestartAlerts(root); got["batch"] != "critical" {
		t.Fatalf("batch now follows the default 3/10 and 60 restarts is critical: %v", got)
	}
}

func TestContainerThresholdRequestValidation(t *testing.T) {
	a := newAPI(t)
	root, _ := a.login("root@x.test", "super_admin")
	org := a.createOrg(root, "A")
	c := a.createPushHost(root, org, "web-1")
	path := "/api/v1/hosts/" + c.ID.String() + "/thresholds"
	ok := func(w, c float64) map[string]any { return map[string]any{"warning_level": w, "critical_level": c} }
	put := func(containers any) int {
		return a.call("PUT", path, root, map[string]any{"thresholds": map[string]any{}, "container_thresholds": containers}, nil)
	}

	tooMany := map[string]any{}
	for i := 0; i < 65; i++ {
		tooMany["c"+strings.Repeat("x", i)] = ok(1, 2)
	}
	bad := map[string]any{
		"empty name":         map[string]any{"": ok(1, 2)},
		"blank name":         map[string]any{"   ": ok(1, 2)},
		"name too long":      map[string]any{strings.Repeat("a", 256): ok(1, 2)},
		"warning > critical": map[string]any{"web": ok(10, 5)},
		"negative":           map[string]any{"web": ok(-1, 5)},
		"too many restarts":  map[string]any{"web": ok(1, 2_000_000)},
		"half a pair":        map[string]any{"web": map[string]any{"warning_level": 5}},
		"wrong type":         map[string]any{"web": "5"},
		"not an object":      []string{"web"},
		"too many":           tooMany,
	}
	for name, containers := range bad {
		if got := put(containers); got != 400 {
			t.Errorf("%s: %d, want 400", name, got)
		}
	}
	// Yüzde sınırı docker_restart için geçerli değildir: 500 restart geçerli bir eşiktir.
	if got := put(map[string]any{"web": ok(100, 500), "my-app_1.worker": ok(1, 2)}); got != 200 {
		t.Errorf("valid names/levels: %d", got)
	}
	// Reddedilen bir istek hiçbir şeyi değiştirmez.
	before := a.containerThresholds(root, c.ID)
	if got := a.call("PUT", path, root, map[string]any{
		"thresholds":           map[string]any{"cpu": ok(60, 70)},
		"container_thresholds": map[string]any{"web": ok(1, 2), "bad": ok(9, 1)},
	}, nil); got != 400 {
		t.Fatalf("mixed valid+invalid: %d", got)
	}
	after := a.containerThresholds(root, c.ID)
	if len(after) != len(before) || after["web"] != before["web"] {
		t.Fatalf("a rejected request changed the container thresholds: %v -> %v", before, after)
	}
	if v := a.hostThresholds(root, c.ID)["cpu"]; v.Custom != nil {
		t.Fatalf("a rejected request still applied cpu: %+v", v)
	}
}

func TestContainerThresholdPermissionsAndCreation(t *testing.T) {
	a := newAPI(t)
	root, _ := a.login("root@x.test", "super_admin")
	adminTok, adminID := a.login("oa@x.test", "org_admin")
	opTok, opID := a.login("op@x.test", "operator")
	orgA, orgB := a.createOrg(root, "A"), a.createOrg(root, "B")
	testdb.AssignOrg(t, a.pool, adminID, orgA)
	mine := a.createPushHost(root, orgA, "a-1")
	theirs := a.createPushHost(root, orgB, "b-1")
	testdb.AssignHost(t, a.pool, opID, mine.ID)
	body := map[string]any{"thresholds": map[string]any{}, "container_thresholds": map[string]any{"web": map[string]any{"warning_level": 1, "critical_level": 2}}}
	p := func(c createdHost) string { return "/api/v1/hosts/" + c.ID.String() + "/thresholds" }

	a.expect(200, "PUT", p(mine), adminTok, body, nil)
	a.expect(403, "PUT", p(theirs), adminTok, body, nil)
	a.expect(403, "PUT", p(mine), opTok, body, nil)
	if got := a.containerThresholds(opTok, mine.ID); got["web"] != (thresholdLevels{1, 2}) {
		t.Fatalf("an operator must be able to read their host's container thresholds: %v", got)
	}
	if got := a.containerThresholds(root, theirs.ID); len(got) != 0 {
		t.Fatalf("a forbidden PUT changed another organization's host: %v", got)
	}

	create := func(token string, containers any) (int, createdHost) {
		var c createdHost
		return a.call("POST", "/api/v1/hosts", token, map[string]any{
			"organization_id": orgA, "title": "h" + uuid.NewString()[:4], "ip": "10.0.0.1", "mode": "push", "interval_seconds": 10,
			"container_thresholds": containers,
		}, &c), c
	}
	code, c := create(adminTok, map[string]any{"db": map[string]any{"warning_level": 5, "critical_level": 9}, "web": nil})
	if code != 201 {
		t.Fatalf("create with container thresholds: %d", code)
	}
	if got := a.containerThresholds(root, c.ID); len(got) != 1 || got["db"] != (thresholdLevels{5, 9}) {
		t.Fatalf("stored = %v, want only db (null entries are ignored)", got)
	}
	var before, after int
	a.pool.QueryRow(context.Background(), `SELECT count(*) FROM hosts`).Scan(&before)
	if code, _ := create(adminTok, map[string]any{"db": map[string]any{"warning_level": 9, "critical_level": 5}}); code != 400 {
		t.Fatalf("invalid container threshold on create: %d, want 400", code)
	}
	a.pool.QueryRow(context.Background(), `SELECT count(*) FROM hosts`).Scan(&after)
	if after != before {
		t.Fatalf("a rejected creation left a host behind (%d -> %d)", before, after)
	}
	a.pool.Exec(context.Background(), `DELETE FROM role_permissions WHERE role = 'org_admin' AND permission_key = 'threshold.edit'`)
	if code, _ := create(adminTok, map[string]any{"db": map[string]any{"warning_level": 5, "critical_level": 9}}); code != 403 {
		t.Errorf("container thresholds without threshold.edit: %d, want 403", code)
	}
	if code, _ := create(adminTok, map[string]any{"db": nil}); code != 201 {
		t.Errorf("nothing custom without threshold.edit: %d, want 201", code)
	}
}
