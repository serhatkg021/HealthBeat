package httpapi_test

import (
	"encoding/json"
	"sort"
	"strings"
	"testing"

	"github.com/google/uuid"

	"healthbeat-server/internal/testdb"
)

type overviewBody struct {
	Organizations []struct {
		ID   uuid.UUID `json:"id"`
		Name string    `json:"name"`
	} `json:"organizations"`
	Hosts []struct {
		ID             uuid.UUID `json:"id"`
		OrganizationID uuid.UUID `json:"organization_id"`
		Title          string    `json:"title"`
		Mode           string    `json:"mode"`
		Status         string    `json:"status"`
	} `json:"hosts"`
	Alerts []struct {
		HostID    uuid.UUID `json:"host_id"`
		AlertType string    `json:"alert_type"`
		Level     string    `json:"level"`
		Status    string    `json:"status"`
	} `json:"alerts"`
}

func (b overviewBody) hostnames() []string {
	var out []string
	for _, c := range b.Hosts {
		out = append(out, c.Title)
	}
	sort.Strings(out)
	return out
}

func (b overviewBody) orgNames() []string {
	out := []string{}
	for _, o := range b.Organizations {
		out = append(out, o.Name)
	}
	sort.Strings(out)
	return out
}

func (a *api) overview(token string) overviewBody {
	a.t.Helper()
	var b overviewBody
	a.expect(200, "GET", "/api/v1/dashboard/overview", token, nil, &b)
	return b
}

// Panelin Özet süzgeçleri bu tek istekle beslenir; herkes yalnızca kendi kapsamını görmelidir.
func TestDashboardOverviewIsScopedToWhatTheCallerMayView(t *testing.T) {
	a := newAPI(t)
	root, _ := a.login("root@x.test", "super_admin")
	adminTok, adminID := a.login("oa@x.test", "org_admin")
	loneAdminTok, _ := a.login("lone-oa@x.test", "org_admin") // hiçbir organizasyona atanmamış
	opTok, opID := a.login("op@x.test", "operator")
	loneOpTok, _ := a.login("lone-op@x.test", "operator") // hiçbir host'a atanmamış

	orgA, orgB := a.createOrg(root, "Alpha"), a.createOrg(root, "Beta")
	testdb.AssignOrg(t, a.pool, adminID, orgA)
	a1 := a.createPushHost(root, orgA, "a-1")
	a2 := a.createPushHost(root, orgA, "a-2")
	b1 := a.createPushHost(root, orgB, "b-1")
	testdb.AssignHost(t, a.pool, opID, a1.ID)
	testdb.AssignOrg(t, a.pool, opID, orgA) // bir organizasyona atanmış olsa da operatör organizasyon adlarını almaz

	a.setThreshold(root, "cpu", 50, 90)
	cpu := func(c createdHost, pct float64) {
		a.expect(204, "POST", "/api/v1/metrics", c.APIToken,
			map[string]any{"cpu_usage_pct": pct, "ram_usage_pct": 1, "disk": []any{}, "docker_containers": []any{}}, nil, "X-Host-ID", c.ID.String())
	}
	cpu(a1, 95) // Alpha: kritik
	cpu(a2, 95) // Alpha: kritik oldu ...
	cpu(a2, 10) // ... ve düzeldi: çözülmüş alert Özet'e girmemeli
	cpu(b1, 60) // Beta: uyarı

	all := a.overview(root)
	if got := all.hostnames(); strings.Join(got, ",") != "a-1,a-2,b-1" {
		t.Fatalf("super_admin hosts = %v", got)
	}
	if got := all.orgNames(); strings.Join(got, ",") != "Alpha,Beta" {
		t.Fatalf("super_admin organizations = %v", got)
	}
	if len(all.Alerts) != 2 {
		t.Fatalf("super_admin alerts = %+v, want the two open cpu alerts", all.Alerts)
	}
	for _, al := range all.Alerts {
		if al.Status != "open" {
			t.Fatalf("overview returned a non-open alert: %+v", al)
		}
	}

	mine := a.overview(adminTok)
	if got := mine.hostnames(); strings.Join(got, ",") != "a-1,a-2" || strings.Join(mine.orgNames(), ",") != "Alpha" {
		t.Fatalf("org_admin sees hosts %v orgs %v, want only Alpha's", got, mine.orgNames())
	}
	if len(mine.Alerts) != 1 || mine.Alerts[0].HostID != a1.ID || mine.Alerts[0].Level != "critical" {
		t.Fatalf("org_admin alerts = %+v, want only a-1's critical alert", mine.Alerts)
	}

	op := a.overview(opTok)
	if got := op.hostnames(); strings.Join(got, ",") != "a-1" {
		t.Fatalf("operator hosts = %v, want only the assigned a-1", got)
	}
	if len(op.Organizations) != 0 {
		t.Fatalf("operator received organization names: %+v (no organization.view)", op.Organizations)
	}
	if len(op.Alerts) != 1 || op.Alerts[0].HostID != a1.ID {
		t.Fatalf("operator alerts = %+v", op.Alerts)
	}
	if op.Hosts[0].OrganizationID != orgA {
		t.Fatalf("operator's host lost its organization_id")
	}

	// Kapsamı boş olan hesaplar boş görür — "kapsamsız" sanılıp her şeyi görmemeli.
	for name, tok := range map[string]string{"org_admin without organizations": loneAdminTok, "operator without hosts": loneOpTok} {
		b := a.overview(tok)
		if len(b.Hosts) != 0 || len(b.Alerts) != 0 || len(b.Organizations) != 0 {
			t.Errorf("%s sees data: %+v", name, b)
		}
	}
}

func TestDashboardOverviewIsSlimAndNeverCarriesSecrets(t *testing.T) {
	a := newAPI(t)
	root, _ := a.login("root@x.test", "super_admin")
	org := a.createOrg(root, "A")
	c := a.createPushHost(root, org, "web-1")

	var raw struct {
		Hosts []map[string]any `json:"hosts"`
	}
	a.expect(200, "GET", "/api/v1/dashboard/overview", root, nil, &raw)
	if len(raw.Hosts) != 1 {
		t.Fatalf("hosts = %v", raw.Hosts)
	}
	want := map[string]bool{"id": true, "organization_id": true, "title": true, "ip": true, "mode": true, "status": true, "last_seen": true}
	for k := range raw.Hosts[0] {
		if !want[k] {
			t.Errorf("overview host exposes unexpected field %q", k)
		}
	}
	body, _ := json.Marshal(raw)
	if strings.Contains(string(body), c.APIToken) {
		t.Fatal("overview leaks a host credential")
	}
	// Boş sistemde alanlar null değil boş dizidir; panel doğrudan .map çağırır.
	empty := newAPI(t)
	tok, _ := empty.login("root@x.test", "super_admin")
	var probe map[string]json.RawMessage
	empty.expect(200, "GET", "/api/v1/dashboard/overview", tok, nil, &probe)
	for _, k := range []string{"organizations", "hosts", "alerts"} {
		if string(probe[k]) != "[]" {
			t.Errorf("%s = %s on an empty system, want []", k, probe[k])
		}
	}
}

func TestDashboardOverviewRequiresAuthentication(t *testing.T) {
	a := newAPI(t)
	a.expect(401, "GET", "/api/v1/dashboard/overview", "", nil, nil)
}
