package httpapi_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/google/uuid"

	"healthbeat-server/internal/testdb"
)

func (a *api) mountThresholds(token string, id uuid.UUID) map[string]thresholdLevels {
	a.t.Helper()
	var resp struct {
		Mounts []struct {
			Mount  string          `json:"mount"`
			Custom thresholdLevels `json:"custom"`
		} `json:"mount_thresholds"`
	}
	a.expect(200, "GET", "/api/v1/hosts/"+id.String()+"/thresholds", token, nil, &resp)
	out := map[string]thresholdLevels{}
	for _, m := range resp.Mounts {
		out[m.Mount] = m.Custom
	}
	return out
}

func (a *api) openDiskAlerts(token string) map[string]string { // mount -> seviye
	a.t.Helper()
	var open []struct {
		AlertType string `json:"alert_type"`
		Subject   string `json:"subject"`
		Level     string `json:"level"`
	}
	a.expect(200, "GET", "/api/v1/alerts?status=open", token, nil, &open)
	out := map[string]string{}
	for _, al := range open {
		if al.AlertType == "disk" {
			out[al.Subject] = al.Level
		}
	}
	return out
}

// "/" -> 70/90 ama "/storage" -> 90/95; sunucuda ayarlanır ve sunucu rapor verince uygulanır.
func TestPerMountDiskThresholdsEndToEndOverTheAPI(t *testing.T) {
	a := newAPI(t)
	root, _ := a.login("root@x.test", "super_admin")
	org := a.createOrg(root, "A")
	c := a.createPushHost(root, org, "web-1")
	path := "/api/v1/hosts/" + c.ID.String() + "/thresholds"
	a.setThreshold(root, "disk", 85, 95) // varsayılan

	if got := a.mountThresholds(root, c.ID); len(got) != 0 {
		t.Fatalf("a new host starts with no mount thresholds: %v", got)
	}
	a.expect(200, "PUT", path, root, map[string]any{
		"thresholds": map[string]any{},
		"mount_thresholds": map[string]any{
			"/":        map[string]any{"warning_level": 70, "critical_level": 90},
			"/storage": map[string]any{"warning_level": 90, "critical_level": 95},
		},
	}, nil)
	if got := a.mountThresholds(root, c.ID); len(got) != 2 || got["/"] != (thresholdLevels{70, 90}) || got["/storage"] != (thresholdLevels{90, 95}) {
		t.Fatalf("mount thresholds = %v", got)
	}
	// Bunlar, varsayılanda kalan host geneli disk eşiğinden ayrıdır.
	if v := a.hostThresholds(root, c.ID)["disk"]; v.Custom != nil {
		t.Fatalf("a mount threshold turned into the host's disk threshold: %+v", v)
	}

	a.pushDisks(c, map[string]float64{"/": 75, "/storage": 88, "/data": 80})
	if got := a.openDiskAlerts(root); len(got) != 1 || got["/"] != "warning" {
		t.Fatalf("alerts = %v, want only a warning for / (75 >= 70; /storage 88 < 90; /data 80 < default 85)", got)
	}
	a.pushDisks(c, map[string]float64{"/": 75, "/storage": 96, "/data": 80})
	if got := a.openDiskAlerts(root); got["/storage"] != "critical" || got["/"] != "warning" {
		t.Fatalf("alerts = %v, want / warning and /storage critical (96 >= 95)", got)
	}

	// null yalnızca o mount'u kaldırır; diğeri kendi değerini korur.
	a.expect(200, "PUT", path, root, map[string]any{"thresholds": map[string]any{}, "mount_thresholds": map[string]any{"/storage": nil}}, nil)
	if got := a.mountThresholds(root, c.ID); len(got) != 1 || got["/"] != (thresholdLevels{70, 90}) {
		t.Fatalf("after removing /storage: %v", got)
	}
	a.pushDisks(c, map[string]float64{"/": 75, "/storage": 96, "/data": 80})
	if got := a.openDiskAlerts(root); got["/storage"] != "critical" {
		t.Fatalf("/storage now follows the default 85/95, and 96%% is critical: %v", got)
	}
	// mount_thresholds'u hiç vermemek hiçbirini değiştirmez.
	a.expect(200, "PUT", path, root, map[string]any{"thresholds": map[string]any{"cpu": map[string]any{"warning_level": 1, "critical_level": 2}}}, nil)
	if got := a.mountThresholds(root, c.ID); len(got) != 1 {
		t.Fatalf("a PUT without mount_thresholds changed them: %v", got)
	}
	if n := a.auditCount("host.update_thresholds"); n != 3 {
		t.Errorf("audit rows = %d, want 3", n)
	}
}

func TestMountThresholdRequestValidation(t *testing.T) {
	a := newAPI(t)
	root, _ := a.login("root@x.test", "super_admin")
	org := a.createOrg(root, "A")
	c := a.createPushHost(root, org, "web-1")
	path := "/api/v1/hosts/" + c.ID.String() + "/thresholds"
	ok := func(w, c float64) map[string]any { return map[string]any{"warning_level": w, "critical_level": c} }
	put := func(mounts any) int {
		return a.call("PUT", path, root, map[string]any{"thresholds": map[string]any{}, "mount_thresholds": mounts}, nil)
	}

	tooMany := map[string]any{}
	for i := 0; i < 65; i++ {
		tooMany[fmt.Sprintf("/m%d", i)] = ok(1, 2)
	}
	bad := map[string]any{
		"relative path":      map[string]any{"data": ok(1, 2)},
		"empty path":         map[string]any{"": ok(1, 2)},
		"control character":  map[string]any{"/a\u0001b": ok(1, 2)},
		"warning > critical": map[string]any{"/data": ok(90, 80)},
		"percent over 100":   map[string]any{"/data": ok(50, 101)},
		"negative":           map[string]any{"/data": ok(-1, 50)},
		"half a pair":        map[string]any{"/data": map[string]any{"warning_level": 50}},
		"wrong type":         map[string]any{"/data": "80"},
		"not an object":      []string{"/data"},
		"too many mounts":    tooMany,
	}
	for name, mounts := range bad {
		if got := put(mounts); got != 400 {
			t.Errorf("%s: %d, want 400", name, got)
		}
	}
	// Kötü bir mount tüm isteği reddeder, geçerli host geneli kısım dahil.
	if got := a.call("PUT", path, root, map[string]any{
		"thresholds":       map[string]any{"cpu": ok(60, 70)},
		"mount_thresholds": map[string]any{"/data": ok(1, 2), "/bad": ok(9, 1)},
	}, nil); got != 400 {
		t.Fatalf("mixed valid+invalid: %d", got)
	}
	if v := a.hostThresholds(root, c.ID)["cpu"]; v.Custom != nil {
		t.Fatalf("a rejected request still applied cpu: %+v", v)
	}
	if got := a.mountThresholds(root, c.ID); len(got) != 0 {
		t.Fatalf("a rejected request still stored mounts: %v", got)
	}
	// Boşluklu yollar sorun değil; agent'ın raporladığı gibi birebir karşılaştırılırlar.
	if got := put(map[string]any{"/mnt/My Disk": ok(1, 2)}); got != 200 {
		t.Errorf("path with a space: %d", got)
	}
	// "thresholds" zorunlu kalır.
	a.expect(400, "PUT", path, root, map[string]any{"mount_thresholds": map[string]any{"/data": ok(1, 2)}}, nil)
}

func TestMountThresholdPermissionsAndCreation(t *testing.T) {
	a := newAPI(t)
	root, _ := a.login("root@x.test", "super_admin")
	adminTok, adminID := a.login("oa@x.test", "org_admin")
	opTok, opID := a.login("op@x.test", "operator")
	orgA, orgB := a.createOrg(root, "A"), a.createOrg(root, "B")
	testdb.AssignOrg(t, a.pool, adminID, orgA)
	mine := a.createPushHost(root, orgA, "a-1")
	theirs := a.createPushHost(root, orgB, "b-1")
	testdb.AssignHost(t, a.pool, opID, mine.ID)
	body := map[string]any{"thresholds": map[string]any{}, "mount_thresholds": map[string]any{"/": map[string]any{"warning_level": 10, "critical_level": 20}}}
	p := func(c createdHost) string { return "/api/v1/hosts/" + c.ID.String() + "/thresholds" }

	a.expect(200, "PUT", p(mine), adminTok, body, nil)
	a.expect(403, "PUT", p(theirs), adminTok, body, nil)
	a.expect(403, "PUT", p(mine), opTok, body, nil)
	if got := a.mountThresholds(opTok, mine.ID); got["/"] != (thresholdLevels{10, 20}) {
		t.Fatalf("an operator must be able to read their host's mount thresholds: %v", got)
	}
	if got := a.mountThresholds(root, theirs.ID); len(got) != 0 {
		t.Fatalf("a forbidden PUT changed another organization's host: %v", got)
	}

	// Oluşturma: host ile birlikte saklanır, ya hep ya hiç, ve threshold.edit gerekir.
	create := func(token string, mounts any) (int, createdHost) {
		var c createdHost
		return a.call("POST", "/api/v1/hosts", token, map[string]any{
			"organization_id": orgA, "title": "h" + uuid.NewString()[:4], "ip": "10.0.0.1", "mode": "push", "interval_seconds": 10,
			"mount_thresholds": mounts,
		}, &c), c
	}
	code, c := create(adminTok, map[string]any{"/storage": map[string]any{"warning_level": 90, "critical_level": 95}, "/": nil})
	if code != 201 {
		t.Fatalf("create with mount thresholds: %d", code)
	}
	if got := a.mountThresholds(root, c.ID); len(got) != 1 || got["/storage"] != (thresholdLevels{90, 95}) {
		t.Fatalf("stored = %v, want only /storage (null entries are ignored)", got)
	}
	var before int
	a.pool.QueryRow(context.Background(), `SELECT count(*) FROM hosts`).Scan(&before)
	if code, _ := create(adminTok, map[string]any{"/data": map[string]any{"warning_level": 90, "critical_level": 80}}); code != 400 {
		t.Fatalf("invalid mount threshold on create: %d, want 400", code)
	}
	var after int
	a.pool.QueryRow(context.Background(), `SELECT count(*) FROM hosts`).Scan(&after)
	if after != before {
		t.Fatalf("a rejected creation left a host behind (%d -> %d)", before, after)
	}
	a.pool.Exec(context.Background(), `DELETE FROM role_permissions WHERE role = 'org_admin' AND permission_key = 'threshold.edit'`)
	if code, _ := create(adminTok, map[string]any{"/storage": map[string]any{"warning_level": 90, "critical_level": 95}}); code != 403 {
		t.Errorf("mount thresholds without threshold.edit: %d, want 403", code)
	}
	if code, _ := create(adminTok, map[string]any{"/storage": nil}); code != 201 {
		t.Errorf("nothing custom without threshold.edit: %d, want 201", code)
	}
}
