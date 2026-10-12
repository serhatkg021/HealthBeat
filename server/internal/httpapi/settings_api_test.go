package httpapi_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"strings"
	"testing"

	"github.com/google/uuid"

	"healthbeat-server/internal/testdb"
	"healthbeat-server/internal/testsmtp"
)

type settingsView struct {
	Values    map[string]any `json:"values"`
	Defaults  map[string]any `json:"defaults"`
	Changed   []string       `json:"changed"`
	UpdatedBy *struct {
		ID   uuid.UUID `json:"id"`
		Name string    `json:"name"`
	} `json:"updated_by"`
}

type apiErrorBody struct {
	Error  string            `json:"error"`
	Code   string            `json:"code"`
	Fields map[string]string `json:"fields"`
}

// Ayarlar, kanallar, sistem kişileri ve sistem kuralları yalnızca settings.* izni olanlara (varsayılan olarak
// super_admin) açıktır.
func TestSettingsEndpointsNeedSettingsPermissions(t *testing.T) {
	a := newAPI(t)
	orgAdmin, _ := a.login("oa@x.test", "org_admin")
	operator, _ := a.login("op@x.test", "operator")
	for _, tok := range []string{orgAdmin, operator} {
		for _, c := range []struct{ method, path string }{
			{"GET", "/api/v1/settings"}, {"PATCH", "/api/v1/settings"}, {"POST", "/api/v1/settings/reset"},
			{"GET", "/api/v1/notification-channels"}, {"PATCH", "/api/v1/notification-channels/email"},
			{"POST", "/api/v1/notification-channels/email/test"},
			{"GET", "/api/v1/system-contacts"}, {"POST", "/api/v1/system-contacts"},
			{"PUT", "/api/v1/system-contacts/" + uuid.NewString()}, {"DELETE", "/api/v1/system-contacts/" + uuid.NewString()},
			{"GET", "/api/v1/notification-routes/system"}, {"GET", "/api/v1/notification-recipients/system"},
		} {
			if code := a.call(c.method, c.path, tok, map[string]any{}, nil); code != 403 {
				t.Errorf("%s %s = %d, want 403", c.method, c.path, code)
			}
		}
	}
	a.expect(401, "GET", "/api/v1/settings", "", nil, nil)
}

func TestSettingsAPI(t *testing.T) {
	a := newAPI(t)
	root, rootID := a.login("root@x.test", "super_admin")

	var v settingsView
	a.expect(200, "GET", "/api/v1/settings", root, nil, &v)
	if v.Values["log_level"] != "info" || v.Defaults["metrics_retention_days"] != float64(30) || len(v.Changed) != 0 || v.UpdatedBy != nil {
		t.Fatalf("initial settings = %+v", v)
	}

	a.expect(200, "PATCH", "/api/v1/settings", root, map[string]any{"log_level": "debug", "latest_agent_version": "1.3.0"}, &v)
	if v.Values["log_level"] != "debug" || strings.Join(v.Changed, ",") != "latest_agent_version,log_level" ||
		v.UpdatedBy == nil || v.UpdatedBy.ID != rootID {
		t.Fatalf("after patch = %+v", v)
	}
	if n := a.auditCount("settings.update"); n != 1 {
		t.Fatalf("settings.update audit entries = %d, want 1", n)
	}
	var details string
	if err := a.pool.QueryRow(context.Background(), `SELECT details::text FROM audit_logs WHERE action = 'settings.update'`).Scan(&details); err != nil ||
		!strings.Contains(details, `"log_level": {"new": "debug", "old": "info"}`) {
		t.Fatalf("audit details = %s err=%v", details, err)
	}

	// Aynı değer: yanıt 200, denetim kaydı yazılmaz.
	a.expect(200, "PATCH", "/api/v1/settings", root, map[string]any{"log_level": "debug"}, nil)
	if n := a.auditCount("settings.update"); n != 1 {
		t.Fatalf("a no-op change was audited (%d entries)", n)
	}

	// Geçersiz değer alanı adlandıran 400; bilinmeyen alan ve yanlış tür de 400.
	var e apiErrorBody
	a.expect(400, "PATCH", "/api/v1/settings", root, map[string]any{"access_token_ttl_seconds": 10}, &e)
	if e.Code != "validation_failed" || e.Fields["access_token_ttl_seconds"] == "" {
		t.Fatalf("invalid value error = %+v", e)
	}
	a.expect(400, "PATCH", "/api/v1/settings", root, map[string]any{"min_supported_agent_version": "2.0.0"}, &e)
	if e.Fields["min_supported_agent_version"] == "" {
		t.Fatalf("min above latest error = %+v", e)
	}
	a.expect(400, "PATCH", "/api/v1/settings", root, map[string]any{"log_levle": "debug"}, &e)
	a.expect(400, "PATCH", "/api/v1/settings", root, map[string]any{"metrics_retention_days": "30"}, &e)
	a.expect(400, "PATCH", "/api/v1/settings", root, map[string]any{}, nil)

	// Varsayılana dönüş.
	a.expect(200, "POST", "/api/v1/settings/reset", root, map[string]any{"fields": []string{"log_level"}}, &v)
	if v.Values["log_level"] != "info" || strings.Join(v.Changed, ",") != "latest_agent_version" {
		t.Fatalf("after reset = %+v", v)
	}
	a.expect(400, "POST", "/api/v1/settings/reset", root, map[string]any{"fields": []string{"updated_by"}}, &e)
	if e.Fields["updated_by"] == "" {
		t.Fatalf("unknown field reset error = %+v", e)
	}
	if n := a.auditCount("settings.reset"); n != 1 {
		t.Fatalf("settings.reset audit entries = %d, want 1", n)
	}
}

type channelView struct {
	Channel     string         `json:"channel"`
	Enabled     bool           `json:"enabled"`
	Config      map[string]any `json:"config"`
	SecretSet   bool           `json:"secret_set"`
	VerifiedAt  *string        `json:"verified_at"`
	Ready       bool           `json:"ready"`
	Implemented bool           `json:"implemented"`
	Personal    bool           `json:"personal"`
	RuleCount   int            `json:"rule_count"`
}

func TestNotificationChannelsAPI(t *testing.T) {
	a := newAPI(t)
	root, rootID := a.login("root@x.test", "super_admin")
	const secret = "çok-gizli-smtp-şifresi"

	// Test ortamı e-posta kanalını açık başlatır; kanal listesi gönderilebilirlik bilgisini taşır.
	var list []channelView
	a.expect(200, "GET", "/api/v1/notification-channels", root, nil, &list)
	if len(list) != 1 || list[0].Channel != "email" || !list[0].Enabled || !list[0].Ready || !list[0].Implemented ||
		!list[0].Personal || list[0].SecretSet || list[0].Config["username"] != "" {
		t.Fatalf("channels = %+v", list)
	}

	srv := testsmtp.Start(t)
	var ch channelView
	a.expect(200, "PATCH", "/api/v1/notification-channels/email", root, map[string]any{
		"config": map[string]any{"host": srv.Host, "port": json.Number(srv.Port), "username": "hb"}, "secret": secret,
	}, &ch)
	if !ch.SecretSet || ch.Config["host"] != srv.Host || ch.Config["from"] != "hb@x.test" {
		t.Fatalf("after patch = %+v", ch)
	}
	// Seviye kanalda değil bildirim kurallarındadır: eski alan bilinmeyen alan olarak reddedilir.
	a.expect(400, "PATCH", "/api/v1/notification-channels/email", root, map[string]any{"owner_min_level": "critical"}, nil)

	// Ayarı eksik bırakan değişiklik reddedilir; kanal yoksa 404.
	var e apiErrorBody
	a.expect(400, "PATCH", "/api/v1/notification-channels/email", root, map[string]any{"config": map[string]any{"from": ""}}, &e)
	if e.Fields["enabled"] == "" {
		t.Fatalf("clearing from of an enabled channel = %+v", e)
	}
	a.expect(400, "PATCH", "/api/v1/notification-channels/email", root, map[string]any{"config": map[string]any{"port": 0}}, &e)
	if e.Fields["config.port"] == "" {
		t.Fatalf("bad port = %+v", e)
	}
	a.expect(404, "PATCH", "/api/v1/notification-channels/pigeon", root, map[string]any{"enabled": true}, nil)

	// Deneme gönderimi: sahte sunucuya gider, kanal doğrulanır.
	a.expect(204, "POST", "/api/v1/notification-channels/email/test", root, map[string]any{"to": "ali@x.test"}, nil)
	if msgs := srv.Messages(); len(msgs) != 1 || msgs[0].To[0] != "ali@x.test" {
		t.Fatalf("test messages = %+v", msgs)
	}
	a.expect(200, "GET", "/api/v1/notification-channels", root, nil, &list)
	if list[0].VerifiedAt == nil {
		t.Fatal("the channel is not verified after a successful test")
	}
	a.expect(400, "POST", "/api/v1/notification-channels/email/test", root, map[string]any{"to": "değil"}, &e)
	if e.Fields["to"] == "" {
		t.Fatalf("bad recipient = %+v", e)
	}
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	dead := ln.Addr().(*net.TCPAddr).Port
	ln.Close()
	a.expect(200, "PATCH", "/api/v1/notification-channels/email", root, map[string]any{"config": map[string]any{"port": dead}}, nil)
	a.expect(502, "POST", "/api/v1/notification-channels/email/test", root, map[string]any{"to": "ali@x.test"}, &e)
	if e.Code != "channel_test_failed" || !strings.Contains(e.Error, "connect") {
		t.Fatalf("failed test = %+v", e)
	}

	// Kurala bağlı kanal sayısı.
	org := a.createOrg(root, "acme")
	a.expect(201, "POST", "/api/v1/notification-routes", root, map[string]any{"organization_id": org, "user_id": rootID}, nil)
	a.expect(200, "GET", "/api/v1/notification-channels", root, nil, &list)
	if list[0].RuleCount != 1 {
		t.Fatalf("rule_count = %d, want 1", list[0].RuleCount)
	}

	// Şifre hiçbir yanıtta ve hiçbir denetim kaydında yer almaz.
	for _, path := range []string{"/api/v1/notification-channels"} {
		var raw json.RawMessage
		a.expect(200, "GET", path, root, nil, &raw)
		if strings.Contains(string(raw), secret) {
			t.Fatalf("%s leaks the secret: %s", path, raw)
		}
		// Şifresi çözülebilen kanal işaretsizdir (çözülemeyen durumu settings testleri kapsar).
		if !strings.Contains(string(raw), `"secret_unreadable":false`) {
			t.Fatalf("%s does not report secret_unreadable: %s", path, raw)
		}
	}
	var dump string
	if err := a.pool.QueryRow(context.Background(), `SELECT coalesce(string_agg(details::text, ' '), '') FROM audit_logs`).Scan(&dump); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(dump, secret) || !strings.Contains(dump, `"secret": {"new": true, "old": false}`) {
		t.Fatalf("audit details = %s", dump)
	}
	if a.auditCount("notification_channel.update") != 2 || a.auditCount("notification_channel.test") != 2 {
		t.Fatalf("channel audit entries: update=%d test=%d, want 2 and 2 (the failed test is recorded too)",
			a.auditCount("notification_channel.update"), a.auditCount("notification_channel.test"))
	}
}

type systemContactView struct {
	ID        uuid.UUID `json:"id"`
	Name      string    `json:"name"`
	Email     *string   `json:"email"`
	Phone     *string   `json:"phone"`
	Scope     string    `json:"scope"`
	RuleCount int       `json:"rule_count"`
}

// Sistem kişileri organizasyonsuz kişilerdir: e-postaları kendi aralarında tekildir, yöneticileri olmaz, organizasyon
// kişisi uçlarından görünmez ve değiştirilemez; silinince alıcı oldukları kurallar da gider.
func TestSystemContactsAPI(t *testing.T) {
	a := newAPI(t)
	root, _ := a.login("root@x.test", "super_admin")
	org := a.createOrg(root, "acme")

	var ali systemContactView
	a.expect(201, "POST", "/api/v1/system-contacts", root, map[string]any{"name": "Ali", "email": "ali@x.test", "phone": "+905550000001"}, &ali)
	if ali.Scope != "system" || *ali.Email != "ali@x.test" || ali.RuleCount != 0 {
		t.Fatalf("created = %+v", ali)
	}
	a.expect(409, "POST", "/api/v1/system-contacts", root, map[string]any{"name": "Kopya", "email": "ALI@x.test"}, nil)
	var orgContact struct {
		ID uuid.UUID `json:"id"`
	}
	a.expect(201, "POST", "/api/v1/organizations/"+org.String()+"/contacts", root, map[string]any{"name": "Aynı adres", "email": "ali@x.test"}, &orgContact)
	for name, body := range map[string]map[string]any{
		"no address":   {"name": "Boş"},
		"no name":      {"email": "x@x.test"},
		"bad phone":    {"name": "X", "phone": "abc"},
		"unknown key":  {"name": "X", "email": "x@x.test", "mail": "y"},
		"display name": {"name": "X", "email": "X <x@x.test>"},
		"manager":      {"name": "X", "email": "x@x.test", "manager_contact_id": orgContact.ID},
	} {
		if code := a.call("POST", "/api/v1/system-contacts", root, body, nil); code != 400 {
			t.Errorf("%s: %d, want 400", name, code)
		}
	}

	a.expect(201, "POST", "/api/v1/notification-routes", root, map[string]any{"contact_id": ali.ID}, nil)
	var upd systemContactView
	a.expect(200, "PUT", "/api/v1/system-contacts/"+ali.ID.String(), root, map[string]any{"name": "Ali Veli", "email": "ali@x.test"}, &upd)
	if upd.Name != "Ali Veli" || upd.Phone != nil || upd.RuleCount != 1 {
		t.Fatalf("updated = %+v", upd)
	}
	a.expect(404, "PUT", "/api/v1/system-contacts/"+uuid.NewString(), root, map[string]any{"name": "X", "email": "x@x.test"}, nil)
	// Uçlar birbirinin kişisini görmez.
	a.expect(404, "PUT", "/api/v1/system-contacts/"+orgContact.ID.String(), root, map[string]any{"name": "X", "email": "x@x.test"}, nil)
	a.expect(404, "PUT", "/api/v1/contacts/"+ali.ID.String(), root, map[string]any{"name": "X", "email": "x@x.test"}, nil)
	a.expect(404, "DELETE", "/api/v1/contacts/"+ali.ID.String(), root, nil, nil)
	var orgList []struct {
		ID uuid.UUID `json:"id"`
	}
	a.expect(200, "GET", "/api/v1/organizations/"+org.String()+"/contacts", root, nil, &orgList)
	if len(orgList) != 1 || orgList[0].ID != orgContact.ID {
		t.Fatalf("organization contacts = %+v", orgList)
	}

	var list []systemContactView
	a.expect(200, "GET", "/api/v1/system-contacts", root, nil, &list)
	if len(list) != 1 || list[0].Name != "Ali Veli" || list[0].RuleCount != 1 {
		t.Fatalf("system contacts = %+v", list)
	}
	a.expect(204, "DELETE", "/api/v1/system-contacts/"+ali.ID.String(), root, nil, nil)
	a.expect(404, "DELETE", "/api/v1/system-contacts/"+ali.ID.String(), root, nil, nil)
	var rules []routeView
	a.expect(200, "GET", "/api/v1/notification-routes/system", root, nil, &rules)
	if len(rules) != 0 {
		t.Fatalf("system rules after deleting the contact = %+v", rules)
	}
	for action, want := range map[string]int{"contact.create": 2, "contact.update": 1, "contact.delete": 1} {
		if n := a.auditCount(action); n != want {
			t.Errorf("%s audit entries = %d, want %d", action, n, want)
		}
	}
}

// Kurallar yalnızca açık ve kişiye giden kanallara yazılabilir; kapalı kanaldaki kural listede işaretlenir ve seviyesi
// değiştirilebilir.
func TestNotificationRoutesFollowTheChannels(t *testing.T) {
	a := newAPI(t)
	root, rootID := a.login("root@x.test", "super_admin")
	org := a.createOrg(root, "acme")

	var r routeView
	a.expect(201, "POST", "/api/v1/notification-routes", root, map[string]any{"organization_id": org, "user_id": rootID}, &r)

	a.expect(200, "PATCH", "/api/v1/notification-channels/email", root, map[string]any{"enabled": false}, nil)
	var e apiErrorBody
	other, otherID := a.login("oa@x.test", "org_admin")
	testdb.AssignOrg(t, a.pool, otherID, org)
	a.expect(400, "POST", "/api/v1/notification-routes", other, map[string]any{"organization_id": org, "user_id": otherID}, &e)
	if !strings.Contains(e.Fields["channel"], "kapalı") {
		t.Fatalf("rule on a closed channel = %+v", e)
	}
	a.expect(400, "POST", "/api/v1/notification-routes", root, map[string]any{"organization_id": org, "user_id": otherID, "channel": "sms"}, &e)
	if !strings.Contains(e.Fields["channel"], "desteklenmiyor") {
		t.Fatalf("rule on an unimplemented channel = %+v", e)
	}

	var routes []struct {
		ID             uuid.UUID `json:"id"`
		ChannelEnabled bool      `json:"channel_enabled"`
	}
	a.expect(200, "GET", "/api/v1/organizations/"+org.String()+"/notification-routes", root, nil, &routes)
	if len(routes) != 1 || routes[0].ChannelEnabled {
		t.Fatalf("routes = %+v, want the rule marked as on a closed channel", routes)
	}
	a.expect(200, "PUT", "/api/v1/notification-routes/"+r.ID.String(), root, map[string]any{"min_level": "critical"}, nil)
	a.expect(204, "DELETE", "/api/v1/notification-routes/"+r.ID.String(), root, nil, nil)
}

// Uçtan uca: API'den eklenen sistem kişisi ve sistem kuralı, alert açılınca bildirim kuyruğuna alıcı olarak yazılır
// (gönderimin kendisi alertengine ve cmd/server testlerinde).
func TestSystemRuleAddedThroughTheAPIGetsTheAlert(t *testing.T) {
	a := newAPI(t)
	root, _ := a.login("root@x.test", "super_admin")
	var noc systemContactView
	a.expect(201, "POST", "/api/v1/system-contacts", root, map[string]any{"name": "NOC", "email": "noc@x.test"}, &noc)
	a.expect(201, "POST", "/api/v1/notification-routes", root, map[string]any{"contact_id": noc.ID}, nil)

	org := a.createOrg(root, "acme")
	host := a.createPushHost(root, org, "web-1")
	if _, err := a.pool.Exec(context.Background(), `INSERT INTO threshold_defaults (metric_type, warning_level, critical_level) VALUES ('cpu', 80, 95)`); err != nil {
		t.Fatal(err)
	}
	if code := a.push(host, host.APIToken, metrics(97, 10)); code != 204 {
		t.Fatalf("push = %d", code)
	}

	var rows []string
	r, err := a.pool.Query(context.Background(), `SELECT channel || ':' || array_to_string(recipients, ',') FROM notification_outbox WHERE kind = 'alert'`)
	if err != nil {
		t.Fatal(err)
	}
	for r.Next() {
		var s string
		r.Scan(&s)
		rows = append(rows, s)
	}
	r.Close()
	if fmt.Sprint(rows) != "[email:noc@x.test]" {
		t.Fatalf("queued alert notifications = %v, want one to the system contact added through the API", rows)
	}
}

// Kural ekranı kanalların seçilebilirliğini notification.view ile okur; yanıt ayar ayrıntısı taşımaz.
func TestChannelOptionsForTheRuleScreen(t *testing.T) {
	a := newAPI(t)
	root, _ := a.login("root@x.test", "super_admin")
	a.expect(200, "PATCH", "/api/v1/notification-channels/email", root, map[string]any{"secret": "gizli-şifre"}, nil)
	operator, _ := a.login("op@x.test", "operator")
	var raw json.RawMessage
	a.expect(200, "GET", "/api/v1/notification-channels/options", operator, nil, &raw)
	if string(raw) != `[{"channel":"email","enabled":true,"ready":true,"personal":true,"implemented":true}]` {
		t.Fatalf("options = %s", raw)
	}
	if _, err := a.pool.Exec(context.Background(), `DELETE FROM role_permissions WHERE permission_key = 'notification.view' AND role = 'operator'`); err != nil {
		t.Fatal(err)
	}
	a.deps.ResetPermissionCache()
	a.expect(403, "GET", "/api/v1/notification-channels/options", operator, nil, nil)
}
