package httpapi_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/uuid"

	"healthbeat-server/internal/testdb"
)

type orgView struct {
	ID       uuid.UUID  `json:"id"`
	Name     string     `json:"name"`
	Parent   *uuid.UUID `json:"parent_organization_id"`
	Address  *string    `json:"address"`
	Access   string     `json:"access"`
	Children []orgView  `json:"-"`
}

func (a *api) createChildOrg(token, name string, parent uuid.UUID) uuid.UUID {
	a.t.Helper()
	var o idBody
	a.expect(201, "POST", "/api/v1/organizations", token, map[string]any{"name": name, "parent_organization_id": parent, "address": name + " Cad. 1"}, &o)
	return o.ID
}

// Ağaç:  holding ─┬─ acme ── acme-ist
//
//	└─ beta
//
// acme'ye atanan yönetici acme + acme-ist'i yönetir; holding'i yalnızca adıyla (bağlam) görür, beta'yı hiç görmez.
func TestOrgAdminOfASubsidiaryManagesTheBranchAndSeesTheParentOnlyAsContext(t *testing.T) {
	a := newAPI(t)
	root, _ := a.login("root@x.test", "super_admin")
	adminTok, adminID := a.login("acme-admin@x.test", "org_admin")

	var h idBody
	a.expect(201, "POST", "/api/v1/organizations", root, map[string]any{"name": "holding", "address": "Gizli Mah. 5"}, &h)
	holding := h.ID
	acme := a.createChildOrg(root, "acme", holding)
	ist := a.createChildOrg(root, "acme-ist", acme)
	beta := a.createChildOrg(root, "beta", holding)
	testdb.AssignOrg(t, a.pool, adminID, acme)
	hostIst := a.createPushHost(root, ist, "ist-1")
	hostBeta := a.createPushHost(root, beta, "beta-1")
	a.createPushHost(root, holding, "holding-1")

	var orgs []orgView
	a.expect(200, "GET", "/api/v1/organizations", adminTok, nil, &orgs)
	got := map[string]orgView{}
	for _, o := range orgs {
		got[o.Name] = o
	}
	if len(got) != 3 || got["acme"].Access != "full" || got["acme-ist"].Access != "full" || got["holding"].Access != "context" {
		t.Fatalf("organizations = %+v, want acme+acme-ist full and holding as context (beta hidden)", got)
	}
	if got["holding"].Address != nil {
		t.Fatalf("a context organization exposed its address: %v", *got["holding"].Address)
	}
	if got["acme-ist"].Address == nil || got["acme-ist"].Parent == nil || *got["acme-ist"].Parent != acme {
		t.Fatalf("a full-access organization must carry parent and address: %+v", got["acme-ist"])
	}

	// Alt dalın sunucuları yönetilir; üst şirketin ve kardeş dalın sunucuları kapalıdır.
	a.expect(200, "GET", "/api/v1/organizations/"+ist.String()+"/hosts", adminTok, nil, nil)
	a.expect(200, "GET", "/api/v1/hosts/"+hostIst.ID.String(), adminTok, nil, nil)
	a.expect(201, "POST", "/api/v1/hosts", adminTok, map[string]any{"organization_id": ist, "title": "yeni", "ip": "10.0.0.2", "mode": "push", "interval_seconds": 10}, nil)
	a.expect(403, "GET", "/api/v1/organizations/"+holding.String()+"/hosts", adminTok, nil, nil)
	a.expect(403, "GET", "/api/v1/organizations/"+beta.String()+"/hosts", adminTok, nil, nil)
	a.expect(403, "GET", "/api/v1/hosts/"+hostBeta.ID.String(), adminTok, nil, nil)
	a.expect(403, "POST", "/api/v1/hosts", adminTok, map[string]any{"organization_id": holding, "title": "x", "ip": "10.0.0.2", "mode": "push", "interval_seconds": 10}, nil)

	// Bağlam organizasyonu tek başına okunabilir (yalnızca ad ve konum); beta okunamaz.
	var ctx orgView
	a.expect(200, "GET", "/api/v1/organizations/"+holding.String(), adminTok, nil, &ctx)
	if ctx.Access != "context" || ctx.Address != nil {
		t.Fatalf("context organization = %+v", ctx)
	}
	a.expect(403, "GET", "/api/v1/organizations/"+beta.String(), adminTok, nil, nil)

	// Bağlam organizasyonunun eşiği, iletişim kişisi ve bildirim kuralı da değiştirilemez / görülemez.
	a.expect(403, "GET", "/api/v1/organizations/"+holding.String()+"/contacts", adminTok, nil, nil)
	a.expect(403, "POST", "/api/v1/thresholds", adminTok, map[string]any{"organization_id": holding, "metric_type": "cpu", "warning_level": 70, "critical_level": 90}, nil)
	a.expect(201, "POST", "/api/v1/thresholds", adminTok, map[string]any{"organization_id": ist, "metric_type": "cpu", "warning_level": 70, "critical_level": 90}, nil)

	// Üst şirketin eşiği salt okunur bağlam olarak listelenir (geçerli değeri bilebilsin), ama değiştirilemez;
	// kardeş dalın eşiği hiç görünmez.
	var holdingTh, betaTh idBody
	a.expect(201, "POST", "/api/v1/thresholds", root, map[string]any{"organization_id": holding, "metric_type": "ram", "warning_level": 60, "critical_level": 80}, &holdingTh)
	a.expect(201, "POST", "/api/v1/thresholds", root, map[string]any{"organization_id": beta, "metric_type": "disk", "warning_level": 1, "critical_level": 2}, &betaTh)
	var ths []struct {
		ID uuid.UUID `json:"id"`
	}
	a.expect(200, "GET", "/api/v1/thresholds", adminTok, nil, &ths)
	seen := map[uuid.UUID]bool{}
	for _, th := range ths {
		seen[th.ID] = true
	}
	if !seen[holdingTh.ID] || seen[betaTh.ID] {
		t.Fatalf("thresholds visible to the sub-admin: parent=%v sibling=%v, want parent visible and sibling hidden", seen[holdingTh.ID], seen[betaTh.ID])
	}
	a.expect(403, "PUT", "/api/v1/thresholds/"+holdingTh.ID.String(), adminTok, map[string]any{"warning_level": 5}, nil)
	a.expect(403, "DELETE", "/api/v1/thresholds/"+holdingTh.ID.String(), adminTok, nil, nil)

	// Organizasyon ağacını yalnızca super_admin değiştirir.
	a.expect(403, "PUT", "/api/v1/organizations/"+ist.String(), adminTok, map[string]any{"parent_organization_id": nil}, nil)
	a.expect(403, "POST", "/api/v1/organizations", adminTok, map[string]any{"name": "yeni-dal", "parent_organization_id": acme}, nil)

	// Panel görünümü: özet yalnızca erişilebilen sunucuları sayar.
	var sum struct {
		Total int `json:"total_hosts"`
	}
	a.expect(200, "GET", "/api/v1/dashboard/summary", adminTok, nil, &sum)
	if sum.Total != 2 { // ist-1 ve yeni
		t.Fatalf("dashboard total_hosts = %d, want 2", sum.Total)
	}
}

func TestOrganizationTreeEditingRules(t *testing.T) {
	a := newAPI(t)
	root, _ := a.login("root@x.test", "super_admin")
	holding := a.createOrg(root, "holding")
	acme := a.createChildOrg(root, "acme", holding)
	ist := a.createChildOrg(root, "acme-ist", acme)

	a.expect(404, "POST", "/api/v1/organizations", root, map[string]any{"name": "x", "parent_organization_id": uuid.New()}, nil)
	a.expect(409, "POST", "/api/v1/organizations", root, map[string]any{"name": "acme", "parent_organization_id": holding}, nil) // kardeş adı çakışması
	a.expect(409, "PUT", "/api/v1/organizations/"+holding.String(), root, map[string]any{"parent_organization_id": ist}, nil)    // döngü
	a.expect(400, "PUT", "/api/v1/organizations/"+holding.String(), root, map[string]any{"parent_organization_id": "not-a-uuid"}, nil)
	a.expect(409, "DELETE", "/api/v1/organizations/"+acme.String(), root, nil, nil) // alt dalı var

	// null = kök yap; alan hiç verilmezse üst şirket değişmez.
	var moved orgView
	a.expect(200, "PUT", "/api/v1/organizations/"+acme.String(), root, map[string]any{"address": "Yeni adres"}, &moved)
	if moved.Parent == nil || *moved.Parent != holding || moved.Address == nil || *moved.Address != "Yeni adres" {
		t.Fatalf("update without parent field changed the parent: %+v", moved)
	}
	moved = orgView{} // JSON'da olmayan alanlar önceki değeri korumasın
	a.expect(200, "PUT", "/api/v1/organizations/"+acme.String(), root, map[string]any{"parent_organization_id": nil}, &moved)
	if moved.Parent != nil {
		t.Fatalf("parent = %v, want detached (root)", moved.Parent)
	}
	moved = orgView{}
	a.expect(200, "PUT", "/api/v1/organizations/"+acme.String(), root, map[string]any{"address": ""}, &moved)
	if moved.Address != nil {
		t.Fatalf("empty address must clear it: %v", *moved.Address)
	}
	if a.auditCount("organization.update") != 3 {
		t.Fatalf("audit rows = %d, want 3", a.auditCount("organization.update"))
	}
}

func TestContactsAPI(t *testing.T) {
	a := newAPI(t)
	root, _ := a.login("root@x.test", "super_admin")
	adminTok, adminID := a.login("oa@x.test", "org_admin")
	opTok, opID := a.login("op@x.test", "operator")
	orgA, orgB := a.createOrg(root, "A"), a.createOrg(root, "B")
	testdb.AssignOrg(t, a.pool, adminID, orgA)
	host := a.createPushHost(root, orgA, "a-1")
	testdb.AssignHost(t, a.pool, opID, host.ID)
	base := "/api/v1/organizations/" + orgA.String() + "/contacts"

	type contact struct {
		ID      uuid.UUID  `json:"id"`
		Name    string     `json:"name"`
		Email   *string    `json:"email"`
		Manager *uuid.UUID `json:"manager_contact_id"`
	}
	var boss, dev contact
	a.expect(201, "POST", base, adminTok, map[string]any{"name": " Ayşe Yılmaz ", "department": "BT", "title": "Müdür", "email": "ayse@musteri.test"}, &boss)
	if boss.Name != "Ayşe Yılmaz" {
		t.Fatalf("name not trimmed: %q", boss.Name)
	}
	a.expect(201, "POST", base, adminTok, map[string]any{"name": "Ali", "phone": "+90 555 111 22 33", "manager_contact_id": boss.ID}, &dev)
	if dev.Manager == nil || *dev.Manager != boss.ID {
		t.Fatalf("manager not stored: %+v", dev)
	}

	for name, body := range map[string]map[string]any{
		"no name":            {"email": "a@x.test"},
		"no phone or email":  {"name": "X"},
		"bad email":          {"name": "X", "email": "not-an-email"},
		"email with display": {"name": "X", "email": "Ali <a@x.test>"},
		"bad phone":          {"name": "X", "phone": "abc"},
		"unknown field":      {"name": "X", "email": "a@x.test", "role": "admin"},
	} {
		if code := a.call("POST", base, adminTok, body, nil); code != 400 {
			t.Errorf("%s: %d, want 400", name, code)
		}
	}
	// Yönetici başka organizasyondan olamaz.
	var foreign contact
	a.expect(201, "POST", "/api/v1/organizations/"+orgB.String()+"/contacts", root, map[string]any{"name": "Yabancı", "email": "y@x.test"}, &foreign)
	a.expect(400, "POST", base, adminTok, map[string]any{"name": "X", "email": "x@x.test", "manager_contact_id": foreign.ID}, nil)

	var list []contact
	a.expect(200, "GET", base, adminTok, nil, &list)
	if len(list) != 2 {
		t.Fatalf("list = %d contacts, want 2", len(list))
	}

	// Yetkiler: org_admin başka organizasyonu göremez/değiştiremez; operatör iletişim kişisi bile görmez.
	a.expect(403, "GET", "/api/v1/organizations/"+orgB.String()+"/contacts", adminTok, nil, nil)
	a.expect(403, "POST", "/api/v1/organizations/"+orgB.String()+"/contacts", adminTok, map[string]any{"name": "X", "email": "x@x.test"}, nil)
	a.expect(403, "PUT", "/api/v1/contacts/"+foreign.ID.String(), adminTok, map[string]any{"name": "X", "email": "x@x.test"}, nil)
	a.expect(403, "DELETE", "/api/v1/contacts/"+foreign.ID.String(), adminTok, nil, nil)
	a.expect(403, "GET", base, opTok, nil, nil) // operatörün contact.view izni yok: organizasyon değil yalnızca sunucu görür
	a.expect(403, "POST", base, opTok, map[string]any{"name": "X", "email": "x@x.test"}, nil)
	a.expect(401, "GET", base, "", nil, nil)
	a.expect(404, "GET", "/api/v1/organizations/"+uuid.NewString()+"/contacts", root, nil, nil)
	a.expect(404, "PUT", "/api/v1/contacts/"+uuid.NewString(), root, map[string]any{"name": "X", "email": "x@x.test"}, nil)

	devID := dev.ID
	dev = contact{}
	a.expect(200, "PUT", "/api/v1/contacts/"+devID.String(), adminTok, map[string]any{"name": "Ali K.", "email": "ali@musteri.test"}, &dev)
	if dev.Name != "Ali K." || dev.Manager != nil {
		t.Fatalf("update did not replace the fields: %+v", dev)
	}
	a.expect(204, "DELETE", "/api/v1/contacts/"+devID.String(), adminTok, nil, nil)
	a.expect(404, "DELETE", "/api/v1/contacts/"+devID.String(), adminTok, nil, nil)
	if a.auditCount("contact.create") != 3 || a.auditCount("contact.update") != 1 || a.auditCount("contact.delete") != 1 {
		t.Fatalf("audit rows create/update/delete = %d/%d/%d, want 3/1/1",
			a.auditCount("contact.create"), a.auditCount("contact.update"), a.auditCount("contact.delete"))
	}
}

type routeView struct {
	ID              uuid.UUID  `json:"id"`
	OrganizationID  *uuid.UUID `json:"organization_id"`
	HostID          *uuid.UUID `json:"host_id"`
	UserID          *uuid.UUID `json:"user_id"`
	ContactID       *uuid.UUID `json:"contact_id"`
	Channel         string     `json:"channel"`
	MinLevel        string     `json:"min_level"`
	RecipientName   string     `json:"recipient_name"`
	RecipientTarget string     `json:"recipient_target"`
}

func TestNotificationRoutesAPI(t *testing.T) {
	a := newAPI(t)
	root, rootID := a.login("root@x.test", "super_admin")
	adminTok, adminID := a.login("oa@x.test", "org_admin")
	otherAdminTok, otherAdminID := a.login("other@x.test", "org_admin")
	opTok, opID := a.login("op@x.test", "operator")
	orgA, orgB := a.createOrg(root, "A"), a.createOrg(root, "B")
	testdb.AssignOrg(t, a.pool, adminID, orgA)
	testdb.AssignOrg(t, a.pool, otherAdminID, orgB)
	host := a.createPushHost(root, orgA, "a-1")
	hostB := a.createPushHost(root, orgB, "b-1")
	testdb.AssignHost(t, a.pool, opID, host.ID)
	var contact struct {
		ID uuid.UUID `json:"id"`
	}
	a.expect(201, "POST", "/api/v1/organizations/"+orgA.String()+"/contacts", adminTok, map[string]any{"name": "Ayşe", "email": "ayse@musteri.test"}, &contact)
	var foreignContact struct {
		ID uuid.UUID `json:"id"`
	}
	a.expect(201, "POST", "/api/v1/organizations/"+orgB.String()+"/contacts", root, map[string]any{"name": "Y", "email": "y@musteri.test"}, &foreignContact)

	// Aday alıcılar: kapsamdaki yönetici(ler), süper yönetici, atanmış operatör (yalnızca sunucu kapsamında) ve kişiler.
	var cands []struct {
		UserID    *uuid.UUID `json:"user_id"`
		ContactID *uuid.UUID `json:"contact_id"`
		Source    string     `json:"source"`
	}
	a.expect(200, "GET", "/api/v1/organizations/"+orgA.String()+"/notification-recipients", adminTok, nil, &cands)
	if len(cands) != 3 { // root, oa, Ayşe
		t.Fatalf("organization candidates = %+v, want root, oa and the contact", cands)
	}
	a.expect(200, "GET", "/api/v1/hosts/"+host.ID.String()+"/notification-recipients", adminTok, nil, &cands)
	if len(cands) != 4 { // + operatör
		t.Fatalf("host candidates = %+v, want the operator as well", cands)
	}
	a.expect(403, "GET", "/api/v1/organizations/"+orgB.String()+"/notification-recipients", adminTok, nil, nil)
	a.expect(403, "GET", "/api/v1/hosts/"+hostB.ID.String()+"/notification-recipients", adminTok, nil, nil)

	route := func(scope string, id uuid.UUID, rcpt string, rid uuid.UUID, level string) map[string]any {
		return map[string]any{scope: id, rcpt: rid, "channel": "email", "min_level": level}
	}
	var r1, r2 routeView
	a.expect(201, "POST", "/api/v1/notification-routes", adminTok, route("organization_id", orgA, "contact_id", contact.ID, "critical"), &r1)
	if r1.RecipientTarget != "ayse@musteri.test" || r1.RecipientName != "Ayşe" || r1.MinLevel != "critical" {
		t.Fatalf("created route = %+v", r1)
	}
	a.expect(201, "POST", "/api/v1/notification-routes", adminTok, route("host_id", host.ID, "user_id", adminID, "warning"), &r2)
	a.expect(409, "POST", "/api/v1/notification-routes", adminTok, route("host_id", host.ID, "user_id", adminID, "critical"), nil) // aynı kapsam+alıcı+kanal

	// Varsayılanlar: kanal e-posta, seviye warning.
	var r3 routeView
	a.expect(201, "POST", "/api/v1/notification-routes", root, map[string]any{"host_id": host.ID, "user_id": rootID}, &r3)
	if r3.Channel != "email" || r3.MinLevel != "warning" {
		t.Fatalf("defaults = %+v", r3)
	}

	bad := map[string]map[string]any{
		"no scope":              {"user_id": adminID},
		"two scopes":            {"organization_id": orgA, "host_id": host.ID, "user_id": adminID},
		"no recipient":          {"organization_id": orgA},
		"two recipients":        {"organization_id": orgA, "user_id": adminID, "contact_id": contact.ID},
		"unimplemented channel": {"organization_id": orgA, "user_id": adminID, "channel": "sms"},
		"unknown channel":       {"organization_id": orgA, "user_id": adminID, "channel": "pigeon"},
		"unknown level":         {"organization_id": orgA, "user_id": adminID, "min_level": "loud"},
		"recipient outside scope (other org's admin)":   {"organization_id": orgA, "user_id": otherAdminID},
		"recipient outside scope (other org's contact)": {"organization_id": orgA, "contact_id": foreignContact.ID},
		"operator not assigned to the scope":            {"organization_id": orgA, "user_id": opID}, // operatör yalnızca sunucu kapsamında aday
	}
	for name, body := range bad {
		if code := a.call("POST", "/api/v1/notification-routes", adminTok, body, nil); code != 400 {
			t.Errorf("%s: %d, want 400", name, code)
		}
	}
	a.expect(201, "POST", "/api/v1/notification-routes", adminTok, route("host_id", host.ID, "user_id", opID, "info"), nil) // sunucuya atanmış operatör olur
	a.expect(404, "POST", "/api/v1/notification-routes", root, route("host_id", uuid.New(), "user_id", rootID, "info"), nil)
	a.expect(404, "POST", "/api/v1/notification-routes", root, route("organization_id", uuid.New(), "user_id", rootID, "info"), nil)

	// Erişim: başka organizasyona kural yazılamaz/okunamaz; operatör yalnızca okur; oturumsuz erişemez.
	a.expect(403, "POST", "/api/v1/notification-routes", adminTok, route("organization_id", orgB, "user_id", adminID, "info"), nil)
	a.expect(403, "POST", "/api/v1/notification-routes", adminTok, route("host_id", hostB.ID, "user_id", adminID, "info"), nil)
	a.expect(403, "GET", "/api/v1/organizations/"+orgA.String()+"/notification-routes", otherAdminTok, nil, nil)
	a.expect(403, "PUT", "/api/v1/notification-routes/"+r1.ID.String(), otherAdminTok, map[string]any{"min_level": "info"}, nil)
	a.expect(403, "DELETE", "/api/v1/notification-routes/"+r1.ID.String(), otherAdminTok, nil, nil)
	a.expect(200, "GET", "/api/v1/hosts/"+host.ID.String()+"/notification-routes", opTok, nil, nil)
	a.expect(403, "POST", "/api/v1/notification-routes", opTok, route("host_id", host.ID, "user_id", opID, "info"), nil)
	a.expect(403, "DELETE", "/api/v1/notification-routes/"+r2.ID.String(), opTok, nil, nil)
	a.expect(401, "GET", "/api/v1/hosts/"+host.ID.String()+"/notification-routes", "", nil, nil)

	var orgRoutes, hostRoutes []routeView
	a.expect(200, "GET", "/api/v1/organizations/"+orgA.String()+"/notification-routes", adminTok, nil, &orgRoutes)
	a.expect(200, "GET", "/api/v1/hosts/"+host.ID.String()+"/notification-routes", adminTok, nil, &hostRoutes)
	if len(orgRoutes) != 1 || len(hostRoutes) != 3 {
		t.Fatalf("org routes = %d (want 1), host routes = %d (want 3)", len(orgRoutes), len(hostRoutes))
	}

	var upd routeView
	a.expect(200, "PUT", "/api/v1/notification-routes/"+r1.ID.String(), adminTok, map[string]any{"min_level": "info"}, &upd)
	if upd.MinLevel != "info" || upd.Channel != "email" {
		t.Fatalf("update = %+v", upd)
	}
	a.expect(400, "PUT", "/api/v1/notification-routes/"+r1.ID.String(), adminTok, map[string]any{"channel": "sms"}, nil)
	a.expect(400, "PUT", "/api/v1/notification-routes/"+r1.ID.String(), adminTok, map[string]any{"min_level": "loud"}, nil)
	a.expect(404, "PUT", "/api/v1/notification-routes/"+uuid.NewString(), root, map[string]any{"min_level": "info"}, nil)
	a.expect(204, "DELETE", "/api/v1/notification-routes/"+r1.ID.String(), adminTok, nil, nil)
	a.expect(404, "DELETE", "/api/v1/notification-routes/"+r1.ID.String(), adminTok, nil, nil)
	if a.auditCount("notification.create") != 4 || a.auditCount("notification.update") != 1 || a.auditCount("notification.delete") != 1 {
		t.Fatalf("audit rows = %d/%d/%d, want 4/1/1", a.auditCount("notification.create"), a.auditCount("notification.update"), a.auditCount("notification.delete"))
	}

	// Alıcı (kişi) silinince kuralı da gider.
	a.expect(204, "DELETE", "/api/v1/contacts/"+contact.ID.String(), adminTok, nil, nil)
	a.expect(200, "GET", "/api/v1/organizations/"+orgA.String()+"/notification-routes", adminTok, nil, &orgRoutes)
	if len(orgRoutes) != 0 {
		t.Fatalf("routes of a deleted contact survived: %+v", orgRoutes)
	}
}

func TestUserProfileFields(t *testing.T) {
	a := newAPI(t)
	root, _ := a.login("root@x.test", "super_admin")

	var u struct {
		ID       uuid.UUID `json:"id"`
		Email    string    `json:"email"`
		FullName *string   `json:"full_name"`
		Phone    *string   `json:"phone"`
		TwoFA    bool      `json:"two_factor_enabled"`
	}
	a.expect(201, "POST", "/api/v1/users", root, map[string]any{
		"email": "New@X.test", "password": "temporary-pass-123", "role": "operator", "full_name": " Ali Veli ", "phone": "+90 (555) 111-22-33",
	}, &u)
	if u.Email != "new@x.test" || u.FullName == nil || *u.FullName != "Ali Veli" || u.Phone == nil || u.TwoFA {
		t.Fatalf("created user = %+v (email lower-cased, name trimmed, 2FA off)", u)
	}
	for name, body := range map[string]map[string]any{
		"bad phone":     {"email": "b@x.test", "password": "temporary-pass-123", "role": "operator", "phone": "call me"},
		"long name":     {"email": "c@x.test", "password": "temporary-pass-123", "role": "operator", "full_name": strings.Repeat("x", 300)},
		"unknown field": {"email": "d@x.test", "password": "temporary-pass-123", "role": "operator", "two_factor_enabled": true},
	} {
		if code := a.call("POST", "/api/v1/users", root, body, nil); code != 400 {
			t.Errorf("%s: %d, want 400", name, code)
		}
	}

	id := u.ID
	u.FullName, u.Phone = nil, nil // JSON'da olmayan alanlar önceki değeri korumasın
	a.expect(200, "PUT", "/api/v1/users/"+id.String(), root, map[string]any{"full_name": "Ali V.", "phone": ""}, &u)
	if u.FullName == nil || *u.FullName != "Ali V." || u.Phone != nil {
		t.Fatalf("after update = %+v (empty phone clears it)", u)
	}
	a.expect(200, "PUT", "/api/v1/users/"+id.String(), root, map[string]any{"role": "org_admin"}, &u) // profile'a dokunmadan rol
	if u.FullName == nil || *u.FullName != "Ali V." {
		t.Fatalf("a role change wiped the profile: %+v", u)
	}
	var raw map[string]json.RawMessage
	a.expect(200, "GET", "/api/v1/users/"+u.ID.String(), root, nil, &raw)
	if _, leaked := raw["password_hash"]; leaked {
		t.Fatal("password hash exposed")
	}
}

func TestHostAPIShowsTitleReportedHostnameAndMachineDuplicates(t *testing.T) {
	a := newAPI(t)
	root, _ := a.login("root@x.test", "super_admin")
	adminTok, adminID := a.login("oa@x.test", "org_admin")
	orgA, orgB := a.createOrg(root, "A"), a.createOrg(root, "B")
	testdb.AssignOrg(t, a.pool, adminID, orgA)
	h1 := a.createPushHost(root, orgA, "web")
	h2 := a.createPushHost(root, orgA, "web-kopya")
	other := a.createPushHost(root, orgB, "web") // aynı ad başka organizasyonda serbest

	// Aynı organizasyonda aynı başlık olmaz.
	a.expect(409, "POST", "/api/v1/hosts", root, map[string]any{"organization_id": orgA, "title": "web", "ip": "10.0.0.3", "mode": "push", "interval_seconds": 10}, nil)
	a.expect(409, "PUT", "/api/v1/hosts/"+h2.ID.String(), root, map[string]any{"title": "web"}, nil)

	report := map[string]any{"cpu_usage_pct": 1, "ram_usage_pct": 1, "disk": []any{}, "host_info": map[string]any{"hostname": "prod-web-01", "machine_id_hash": strings.Repeat("ab", 16)}}
	for _, h := range []createdHost{h1, h2, other} {
		a.expect(204, "POST", "/api/v1/metrics", h.APIToken, report, nil, "X-Host-ID", h.ID.String())
	}

	var got struct {
		Title    string `json:"title"`
		HostInfo struct {
			Hostname string `json:"hostname"`
		} `json:"host_info"`
		Same []struct {
			ID    uuid.UUID `json:"id"`
			Title string    `json:"title"`
		} `json:"same_machine_as"`
	}
	a.expect(200, "GET", "/api/v1/hosts/"+h1.ID.String(), adminTok, nil, &got)
	if got.Title != "web" || got.HostInfo.Hostname != "prod-web-01" {
		t.Fatalf("host = %+v, want panel title and the machine's own hostname kept apart", got)
	}
	// Yalnızca çağıranın görebildiği kopyalar: başka organizasyondaki sunucu adıyla bile sızmaz.
	if len(got.Same) != 1 || got.Same[0].ID != h2.ID || got.Same[0].Title != "web-kopya" {
		t.Fatalf("same_machine_as = %+v, want only the copy this admin may see", got.Same)
	}
	a.expect(200, "GET", "/api/v1/hosts/"+other.ID.String(), root, nil, &got)
	if len(got.Same) != 2 {
		t.Fatalf("super_admin sees %d copies, want 2", len(got.Same))
	}
}
