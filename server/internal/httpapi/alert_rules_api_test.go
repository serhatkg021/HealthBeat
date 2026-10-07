package httpapi_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/uuid"

	"healthbeat-server/internal/testdb"
)

type thresholdBody struct {
	ID              uuid.UUID `json:"id"`
	MetricType      string    `json:"metric_type"`
	WarningLevel    float64   `json:"warning_level"`
	CriticalLevel   float64   `json:"critical_level"`
	DurationSeconds *int      `json:"duration_seconds"`
}

// Varsayılan eşikler protokol 4 türlerini ve süreyi kabul eder; süre yalnızca o türlerde verilebilir ve güncellemede
// verilmemiş = değişmez, null = kaldır.
func TestThresholdAPIDurationsAndNewTypes(t *testing.T) {
	a := newAPI(t)
	root, _ := a.login("root@x.test", "super_admin")

	var th thresholdBody
	a.expect(201, "POST", "/api/v1/thresholds", root, map[string]any{
		"metric_type": "disk_latency", "warning_level": 30, "critical_level": 50, "duration_seconds": 600}, &th)
	if th.DurationSeconds == nil || *th.DurationSeconds != 600 {
		t.Fatalf("created = %+v", th)
	}
	for name, body := range map[string]map[string]any{
		"duration on cpu":   {"metric_type": "cpu", "warning_level": 70, "critical_level": 90, "duration_seconds": 60},
		"duration 0":        {"metric_type": "temperature", "warning_level": 80, "critical_level": 90, "duration_seconds": 0},
		"temperature > 500": {"metric_type": "temperature", "warning_level": 80, "critical_level": 900},
		"unknown type":      {"metric_type": "gpu", "warning_level": 1, "critical_level": 2},
	} {
		var e struct {
			Error string `json:"error"`
		}
		if code := a.call("POST", "/api/v1/thresholds", root, body, &e); code != 400 {
			t.Errorf("%s: %d %q, want 400", name, code, e.Error)
		}
		if name == "unknown type" && !strings.Contains(e.Error, "time_offset") {
			t.Errorf("unknown type error does not list the types: %q", e.Error)
		}
	}

	path := "/api/v1/thresholds/" + th.ID.String()
	a.expect(200, "PUT", path, root, map[string]any{"warning_level": 35}, &th)
	if th.WarningLevel != 35 || th.DurationSeconds == nil || *th.DurationSeconds != 600 {
		t.Fatalf("level-only update changed the duration: %+v", th)
	}
	a.expect(200, "PUT", path, root, map[string]any{"duration_seconds": 120}, &th)
	if *th.DurationSeconds != 120 {
		t.Fatalf("duration update = %+v", th)
	}
	var raw map[string]json.RawMessage
	a.expect(200, "PUT", path, root, map[string]any{"duration_seconds": nil}, &raw)
	if _, ok := raw["duration_seconds"]; ok {
		t.Fatalf("duration not cleared: %s", raw["duration_seconds"])
	}
	for name, body := range map[string]any{
		"string duration":   map[string]any{"duration_seconds": "10m"},
		"negative duration": map[string]any{"duration_seconds": -5},
	} {
		if code := a.call("PUT", path, root, body, nil); code != 400 {
			t.Errorf("%s: %d, want 400", name, code)
		}
	}
	var cpu thresholdBody
	a.expect(201, "POST", "/api/v1/thresholds", root, map[string]any{"metric_type": "cpu", "warning_level": 70, "critical_level": 90}, &cpu)
	a.expect(400, "PUT", "/api/v1/thresholds/"+cpu.ID.String(), root, map[string]any{"duration_seconds": 60}, nil)
}

// Sunucu eşikleri: süre ve konu bazlı (disk, sensör, servis) eşikler yazılır ve okunur.
func TestHostSubjectThresholdsAPI(t *testing.T) {
	a := newAPI(t)
	root, _ := a.login("root@x.test", "super_admin")
	c := a.createPushHost(root, a.createOrg(root, "A"), "web-1")
	path := "/api/v1/hosts/" + c.ID.String() + "/thresholds"

	var resp struct {
		Thresholds []struct {
			MetricType string `json:"metric_type"`
			Custom     *struct {
				DurationSeconds *int `json:"duration_seconds"`
			} `json:"custom"`
		} `json:"thresholds"`
		SubjectThresholds []struct {
			MetricType string `json:"metric_type"`
			Subject    string `json:"subject"`
			Custom     struct {
				WarningLevel    float64 `json:"warning_level"`
				DurationSeconds *int    `json:"duration_seconds"`
			} `json:"custom"`
		} `json:"subject_thresholds"`
	}
	a.expect(200, "PUT", path, root, map[string]any{
		"thresholds": map[string]any{"temperature": map[string]any{"warning_level": 80, "critical_level": 90, "duration_seconds": 300}},
		"subject_thresholds": map[string]any{
			"service_restart": map[string]any{"nginx.service": map[string]any{"warning_level": 3, "critical_level": 6}},
			"disk_latency":    map[string]any{"sda": map[string]any{"warning_level": 20, "critical_level": 40, "duration_seconds": 120}},
		},
	}, &resp)
	if len(resp.SubjectThresholds) != 2 || resp.SubjectThresholds[0].MetricType != "disk_latency" ||
		*resp.SubjectThresholds[0].Custom.DurationSeconds != 120 || resp.SubjectThresholds[1].Subject != "nginx.service" {
		t.Fatalf("subject thresholds = %+v", resp.SubjectThresholds)
	}
	found := false
	for _, v := range resp.Thresholds {
		if v.MetricType == "temperature" && v.Custom != nil && v.Custom.DurationSeconds != nil && *v.Custom.DurationSeconds == 300 {
			found = true
		}
	}
	if !found {
		t.Fatalf("temperature with duration missing: %+v", resp.Thresholds)
	}

	for name, body := range map[string]any{
		"subject for cpu":   map[string]any{"thresholds": map[string]any{}, "subject_thresholds": map[string]any{"cpu": map[string]any{"x": nil}}},
		"duration on ram":   map[string]any{"thresholds": map[string]any{"ram": map[string]any{"warning_level": 1, "critical_level": 2, "duration_seconds": 60}}},
		"half levels":       map[string]any{"thresholds": map[string]any{}, "subject_thresholds": map[string]any{"disk_latency": map[string]any{"sda": map[string]any{"warning_level": 1}}}},
		"empty sensor name": map[string]any{"thresholds": map[string]any{}, "subject_thresholds": map[string]any{"temperature": map[string]any{" ": nil}}},
	} {
		if code := a.call("PUT", path, root, body, nil); code != 400 {
			t.Errorf("%s: %d, want 400", name, code)
		}
	}
	// Konu bazlı eşiği kaldırmak (null) yalnızca onu siler.
	a.expect(200, "PUT", path, root, map[string]any{"thresholds": map[string]any{},
		"subject_thresholds": map[string]any{"disk_latency": map[string]any{"sda": nil}}}, &resp)
	if len(resp.SubjectThresholds) != 1 || resp.SubjectThresholds[0].Subject != "nginx.service" {
		t.Fatalf("after removing sda: %+v", resp.SubjectThresholds)
	}
}

type ruleView struct {
	Rule          string `json:"rule"`
	TakesDuration bool   `json:"takes_duration"`
	Default       *struct {
		Level           string `json:"level"`
		DurationSeconds *int   `json:"duration_seconds"`
	} `json:"default"`
	Custom *struct {
		Level string `json:"level"`
	} `json:"custom"`
}

func TestStatusRulesAPI(t *testing.T) {
	a := newAPI(t)
	root, _ := a.login("root@x.test", "super_admin")
	adminTok, adminID := a.login("oa@x.test", "org_admin")
	opTok, opID := a.login("op@x.test", "operator")
	orgA, orgB := a.createOrg(root, "A"), a.createOrg(root, "B")
	testdb.AssignOrg(t, a.pool, adminID, orgA)
	mine := a.createPushHost(root, orgA, "a-1")
	theirs := a.createPushHost(root, orgB, "b-1")
	testdb.AssignHost(t, a.pool, opID, mine.ID)

	// Başlangıçta hiç kural yok: her şey kapalı.
	var list []map[string]any
	a.expect(200, "GET", "/api/v1/status-rules", root, nil, &list)
	if len(list) != 0 {
		t.Fatalf("initial rules = %v", list)
	}
	hostPath := func(c createdHost) string { return "/api/v1/hosts/" + c.ID.String() + "/status-rules" }
	var views []ruleView
	a.expect(200, "GET", hostPath(mine), root, nil, &views)
	if len(views) != 11 || views[0].Rule != "service_failed" || !views[0].TakesDuration || views[0].Default != nil {
		t.Fatalf("host rules = %+v", views)
	}
	for _, v := range views {
		if v.Rule == "fs_readonly" && v.TakesDuration {
			t.Error("fs_readonly takes a duration")
		}
	}

	// Genel kurallar yalnızca super_admin'in; organizasyon kuralı o organizasyonu yönetenin.
	global := map[string]any{"organization_id": nil, "rules": map[string]any{
		"service_failed": map[string]any{"level": "critical", "duration_seconds": 60}, "reboot_required": map[string]any{"level": "info"}}}
	a.expect(200, "PUT", "/api/v1/status-rules", root, global, &list)
	if len(list) != 2 {
		t.Fatalf("global rules after PUT = %v", list)
	}
	a.expect(403, "PUT", "/api/v1/status-rules", adminTok, global, nil)
	orgRules := func(org uuid.UUID) map[string]any {
		return map[string]any{"organization_id": org, "rules": map[string]any{"reboot_required": map[string]any{"level": "off"}}}
	}
	a.expect(200, "PUT", "/api/v1/status-rules", adminTok, orgRules(orgA), nil)
	a.expect(403, "PUT", "/api/v1/status-rules", adminTok, orgRules(orgB), nil)
	a.expect(404, "PUT", "/api/v1/status-rules", root, orgRules(uuid.New()), nil)
	a.expect(403, "PUT", "/api/v1/status-rules", opTok, orgRules(orgA), nil)
	a.expect(200, "GET", "/api/v1/status-rules", adminTok, nil, &list)
	if len(list) != 3 {
		t.Fatalf("org_admin sees %d rules, want the 2 global + 1 own", len(list))
	}

	for name, body := range map[string]any{
		"missing rules":           map[string]any{"organization_id": nil},
		"unknown rule":            map[string]any{"rules": map[string]any{"gpu_hot": map[string]any{"level": "info"}}},
		"bad level":               map[string]any{"rules": map[string]any{"oom_kill": map[string]any{"level": "loud"}}},
		"duration on fs_readonly": map[string]any{"rules": map[string]any{"fs_readonly": map[string]any{"level": "critical", "duration_seconds": 30}}},
		"duration > 30 days":      map[string]any{"rules": map[string]any{"security_updates": map[string]any{"level": "info", "duration_seconds": 2592001}}},
	} {
		if code := a.call("PUT", "/api/v1/status-rules", root, body, nil); code != 400 {
			t.Errorf("%s: %d, want 400", name, code)
		}
	}

	// Sunucu görünümü: varsayılan organizasyon zincirinden ya da genelden; sunucunun kendi kuralı ayrı.
	a.expect(200, "PUT", hostPath(mine), adminTok, map[string]any{"rules": map[string]any{"service_failed": map[string]any{"level": "warning"}}}, &views)
	byRule := map[string]ruleView{}
	for _, v := range views {
		byRule[v.Rule] = v
	}
	if v := byRule["service_failed"]; v.Default == nil || v.Default.Level != "critical" || *v.Default.DurationSeconds != 60 ||
		v.Custom == nil || v.Custom.Level != "warning" {
		t.Fatalf("service_failed view = %+v", v)
	}
	if v := byRule["reboot_required"]; v.Default == nil || v.Default.Level != "off" || v.Custom != nil {
		t.Fatalf("reboot_required view = %+v (org A turned it off)", v)
	}
	a.expect(400, "PUT", hostPath(mine), adminTok, map[string]any{}, nil)
	a.expect(403, "PUT", hostPath(theirs), adminTok, map[string]any{"rules": map[string]any{}}, nil)
	a.expect(403, "GET", hostPath(theirs), adminTok, nil, nil)
	a.expect(200, "GET", hostPath(mine), opTok, nil, nil)
	a.expect(403, "PUT", hostPath(mine), opTok, map[string]any{"rules": map[string]any{}}, nil)
	a.expect(404, "GET", "/api/v1/hosts/"+uuid.NewString()+"/status-rules", root, nil, nil)
	a.expect(401, "GET", "/api/v1/status-rules", "", nil, nil)

	// İki katman da kendi başına geçerli (rol izinleri panelden değiştirilebilir): threshold.edit izni alınmış org_admin
	// değiştiremez; threshold.edit verilmiş operatör de organizasyonu yönetmediği için değiştiremez.
	if _, err := a.pool.Exec(context.Background(), `DELETE FROM role_permissions WHERE role = 'org_admin' AND permission_key = 'threshold.edit'`); err != nil {
		t.Fatal(err)
	}
	if _, err := a.pool.Exec(context.Background(), `INSERT INTO role_permissions (role, permission_key) VALUES ('operator', 'threshold.edit')`); err != nil {
		t.Fatal(err)
	}
	a.deps.ResetPermissionCache()
	a.expect(403, "PUT", hostPath(mine), adminTok, map[string]any{"rules": map[string]any{}}, nil)
	a.expect(403, "PUT", "/api/v1/status-rules", adminTok, orgRules(orgA), nil)
	a.expect(403, "PUT", hostPath(mine), opTok, map[string]any{"rules": map[string]any{}}, nil)
	a.expect(403, "PUT", "/api/v1/status-rules", opTok, orgRules(orgA), nil)

	if n := a.auditCount("status_rule.update"); n != 2 {
		t.Errorf("status_rule.update audit rows = %d, want 2", n)
	}
	if n := a.auditCount("host.update_status_rules"); n != 1 {
		t.Errorf("host.update_status_rules audit rows = %d, want 1", n)
	}
}
