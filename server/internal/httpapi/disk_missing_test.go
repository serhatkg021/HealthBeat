package httpapi_test

import (
	"testing"
)

// API üzerinden uçtan uca: sahibi önemli mount'ları seçer, agent onlardan birini raporlamayı
// bırakır ve üç rapor sonra panel critical bir "disk kayboldu" alert'i gösterir.
func TestMissingDiskAlertEndToEndOverTheAPI(t *testing.T) {
	a := newAPI(t)
	root, _ := a.login("root@x.test", "super_admin")
	org := a.createOrg(root, "A")
	c := a.createPushHost(root, org, "web-1")
	a.expect(200, "PUT", "/api/v1/hosts/"+c.ID.String()+"/disk-alerts", root, map[string]any{"all_mounts_alert": false, "custom_alert_mounts": []string{"/", "/storage"}}, nil)

	type alertRow struct {
		AlertType string `json:"alert_type"`
		Subject   string `json:"subject"`
		Level     string `json:"level"`
	}
	missing := func() []alertRow {
		var open []alertRow
		a.expect(200, "GET", "/api/v1/alerts?status=open", root, nil, &open)
		var out []alertRow
		for _, al := range open {
			if al.AlertType == "disk_missing" {
				out = append(out, al)
			}
		}
		return out
	}

	a.pushDisks(c, map[string]float64{"/": 10, "/storage": 10})
	a.pushDisks(c, map[string]float64{"/": 10})
	a.pushDisks(c, map[string]float64{"/": 10})
	if got := missing(); len(got) != 0 {
		t.Fatalf("alerts after 2 reports without /storage: %+v", got)
	}
	a.pushDisks(c, map[string]float64{"/": 10})
	got := missing()
	if len(got) != 1 || got[0].Subject != "/storage" || got[0].Level != "critical" {
		t.Fatalf("alerts = %+v, want a critical disk_missing for /storage", got)
	}
	var dash struct {
		Critical int `json:"open_critical_alerts"`
	}
	a.expect(200, "GET", "/api/v1/dashboard/summary", root, nil, &dash)
	if dash.Critical != 1 {
		t.Fatalf("dashboard critical alerts = %d, want the missing disk counted", dash.Critical)
	}

	a.pushDisks(c, map[string]float64{"/": 10, "/storage": 10})
	if got := missing(); len(got) != 0 {
		t.Fatalf("alerts = %+v, want it resolved when /storage is reported again", got)
	}
}
