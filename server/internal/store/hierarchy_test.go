package store_test

import (
	"context"
	"errors"
	"sort"
	"strings"
	"testing"

	"github.com/google/uuid"

	"healthbeat-server/internal/model"
	"healthbeat-server/internal/store"
	"healthbeat-server/internal/testdb"
)

func idSet(ids []uuid.UUID) map[uuid.UUID]bool {
	m := make(map[uuid.UUID]bool, len(ids))
	for _, id := range ids {
		m[id] = true
	}
	return m
}

func sameIDs(t *testing.T, name string, got []uuid.UUID, want ...uuid.UUID) {
	t.Helper()
	g, w := idSet(got), idSet(want)
	if len(g) != len(w) || len(got) != len(g) {
		t.Fatalf("%s = %v, want %v", name, got, want)
	}
	for id := range w {
		if !g[id] {
			t.Fatalf("%s = %v, want %v", name, got, want)
		}
	}
}

// Ağaç:  holding ─┬─ acme ── acme-ist
//
//	└─ beta
func TestOrganizationAssignmentIsInheritedDownButOnlyChainIsVisibleUpward(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	uo := store.NewUserOrganizations(pool)

	holding := testdb.Org(t, pool, "holding")
	acme := testdb.ChildOrg(t, pool, "acme", holding)
	ist := testdb.ChildOrg(t, pool, "acme-ist", acme)
	beta := testdb.ChildOrg(t, pool, "beta", holding)
	admin := testdb.User(t, pool, "a@x.test", "org_admin", "pw")
	testdb.AssignOrg(t, pool, admin, acme)

	full, err := uo.ListOrganizationIDs(ctx, admin)
	if err != nil {
		t.Fatal(err)
	}
	sameIDs(t, "full access", full, acme, ist)

	context_, err := uo.ListContextIDs(ctx, admin)
	if err != nil {
		t.Fatal(err)
	}
	sameIDs(t, "read-only context", context_, holding) // kardeş dal (beta) görünmez

	for name, c := range map[string]struct {
		org  uuid.UUID
		want bool
	}{"assigned": {acme, true}, "descendant": {ist, true}, "parent": {holding, false}, "sibling": {beta, false}} {
		got, err := uo.IsAssigned(ctx, admin, c.org)
		if err != nil || got != c.want {
			t.Errorf("IsAssigned(%s) = %v err=%v, want %v", name, got, err, c.want)
		}
	}

	// Yalnızca alt organizasyona atanan yönetici üst şirketin kalanını hiç görmez, yalnızca zinciri (bağlam) görür.
	sub := testdb.User(t, pool, "sub@x.test", "org_admin", "pw")
	testdb.AssignOrg(t, pool, sub, ist)
	full, _ = uo.ListOrganizationIDs(ctx, sub)
	sameIDs(t, "sub-admin full access", full, ist)
	ctxIDs, _ := uo.ListContextIDs(ctx, sub)
	sameIDs(t, "sub-admin context", ctxIDs, acme, holding)

	// Hem üst şirkete hem alt dala atanan yönetici üst zinciri "bağlam" değil TAM erişimle görür.
	both := testdb.User(t, pool, "both@x.test", "org_admin", "pw")
	testdb.AssignOrg(t, pool, both, holding)
	testdb.AssignOrg(t, pool, both, ist)
	full, _ = uo.ListOrganizationIDs(ctx, both)
	sameIDs(t, "double-assigned full access", full, holding, acme, ist, beta)
	ctxIDs, _ = uo.ListContextIDs(ctx, both)
	sameIDs(t, "double-assigned context", ctxIDs)
}

func TestOrganizationTreeRules(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	orgs := store.NewOrganizations(pool)

	root, err := orgs.Create(ctx, "holding", nil, ptr("İstanbul"))
	if err != nil || root.Address == nil || *root.Address != "İstanbul" {
		t.Fatalf("root: %+v err=%v", root, err)
	}
	child, err := orgs.Create(ctx, "acme", &root.ID, nil)
	if err != nil || child.ParentOrganizationID == nil || *child.ParentOrganizationID != root.ID {
		t.Fatalf("child: %+v err=%v", child, err)
	}
	grand, err := orgs.Create(ctx, "acme-ist", &child.ID, nil)
	if err != nil {
		t.Fatal(err)
	}

	// Aynı üst şirketin altında aynı ad olmaz; farklı üst şirketin altında olur.
	if _, err := orgs.Create(ctx, "acme", &root.ID, nil); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("duplicate sibling name: err=%v, want ErrConflict", err)
	}
	if _, err := orgs.Create(ctx, "acme", &child.ID, nil); err != nil {
		t.Fatalf("same name under a different parent: %v", err)
	}
	// Kökler arasında da ad benzersizdir (NULL üst şirket "aynı" sayılır).
	if _, err := orgs.Create(ctx, "holding", nil, nil); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("duplicate root name: err=%v, want ErrConflict", err)
	}

	// Döngü: bir organizasyon kendi altındaki dalın altına taşınamaz, kendisinin altına da.
	if _, err := orgs.Update(ctx, root.ID, store.OrgPatch{ParentSet: true, Parent: &grand.ID}); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("move under own descendant: err=%v, want ErrConflict", err)
	}
	if _, err := orgs.Update(ctx, root.ID, store.OrgPatch{ParentSet: true, Parent: &root.ID}); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("move under itself: err=%v, want ErrConflict", err)
	}
	// Geçerli taşıma: dalı kök yap.
	moved, err := orgs.Update(ctx, child.ID, store.OrgPatch{ParentSet: true, Parent: nil})
	if err != nil || moved.ParentOrganizationID != nil {
		t.Fatalf("detach: %+v err=%v", moved, err)
	}

	// Alt organizasyonu ya da sunucusu olan silinemez.
	if err := orgs.Delete(ctx, child.ID); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("delete with children: err=%v, want ErrConflict", err)
	}

	desc, err := orgs.WithDescendants(ctx, []uuid.UUID{child.ID})
	if err != nil {
		t.Fatal(err)
	}
	if len(desc) != 3 { // child, grand, child'ın altındaki "acme"
		t.Fatalf("WithDescendants = %v, want 3 organizations", desc)
	}
	chain, err := orgs.Chain(ctx, grand.ID)
	if err != nil || len(chain) != 2 || chain[0] != grand.ID { // grand → child (artık kök)
		t.Fatalf("Chain = %v err=%v", chain, err)
	}
}

func TestHostTitleIsUniquePerOrganization(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	hosts := store.NewHosts(pool, nil)
	a, b := testdb.Org(t, pool, "A"), testdb.Org(t, pool, "B")
	mk := func(org uuid.UUID, title string) error {
		_, err := hosts.Create(ctx, store.CreateHostParams{OrganizationID: org, Title: title, IP: "10.0.0.1", Mode: "push", IntervalSeconds: 10, APITokenHash: ptr("x"), AllMountsAlert: true})
		return err
	}
	if err := mk(a, "web"); err != nil {
		t.Fatal(err)
	}
	if err := mk(a, "web"); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("same title in the same organization: err=%v, want ErrConflict", err)
	}
	if err := mk(b, "web"); err != nil {
		t.Fatalf("same title in another organization: %v", err)
	}
}

func TestThresholdsAreInheritedFromTheNearestAncestor(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	th := store.NewThresholds(pool)

	holding := testdb.Org(t, pool, "holding")
	acme := testdb.ChildOrg(t, pool, "acme", holding)
	ist := testdb.ChildOrg(t, pool, "acme-ist", acme)
	other := testdb.Org(t, pool, "other")
	host := testdb.PushHost(t, pool, ist, "web", "h")
	otherHost := testdb.PushHost(t, pool, other, "web", "h")

	warnOf := func(hostID, orgID uuid.UUID) (float64, bool) {
		t.Helper()
		c, found, err := th.Resolve(ctx, hostID, orgID, model.MetricTypeCPU)
		if err != nil {
			t.Fatal(err)
		}
		return c.WarningLevel, found
	}

	if _, found := warnOf(host, ist); found {
		t.Fatal("found a threshold when none is configured")
	}
	testdb.Threshold(t, pool, nil, nil, "cpu", 90, 99) // genel
	if w, _ := warnOf(host, ist); w != 90 {
		t.Fatalf("global default: %v, want 90", w)
	}
	testdb.Threshold(t, pool, &holding, nil, "cpu", 85, 95) // en üst şirket, genelden önce gelir
	if w, _ := warnOf(host, ist); w != 85 {
		t.Fatalf("grandparent default: %v, want 85", w)
	}
	testdb.Threshold(t, pool, &acme, nil, "cpu", 75, 90) // yakın üst şirket, uzak olandan önce gelir
	if w, _ := warnOf(host, ist); w != 75 {
		t.Fatalf("parent default: %v, want 75", w)
	}
	testdb.Threshold(t, pool, &ist, nil, "cpu", 70, 85) // kendi organizasyonu
	if w, _ := warnOf(host, ist); w != 70 {
		t.Fatalf("own organization default: %v, want 70", w)
	}
	testdb.Threshold(t, pool, nil, &host, "cpu", 50, 60) // sunucuya özel hepsini ezer
	if w, _ := warnOf(host, ist); w != 50 {
		t.Fatalf("host override: %v, want 50", w)
	}
	// Ağacın dışındaki bir organizasyon yalnızca genel varsayılanı görür.
	if w, _ := warnOf(otherHost, other); w != 90 {
		t.Fatalf("unrelated organization: %v, want the global 90", w)
	}

	// Alt organizasyonun varsayılanı üst şirkete sızmaz.
	if w, _ := warnOf(testdb.PushHost(t, pool, holding, "h", "h"), holding); w != 85 {
		t.Fatalf("a child's default leaked upwards: %v, want 85", w)
	}

	// DefaultsFor da aynı mirası uygular (panelde "geçerli değer" için).
	def, err := th.DefaultsFor(ctx, ist)
	if err != nil || def["cpu"].WarningLevel != 70 {
		t.Fatalf("DefaultsFor = %+v err=%v, want cpu 70", def, err)
	}
	def, _ = th.DefaultsFor(ctx, acme)
	if def["cpu"].WarningLevel != 75 {
		t.Fatalf("DefaultsFor(acme) = %+v, want cpu 75", def)
	}
}

func TestContactsCRUDAndConstraints(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	contacts := store.NewContacts(pool)
	org := testdb.Org(t, pool, "acme")
	other := testdb.Org(t, pool, "other")

	boss, err := contacts.Create(ctx, org, store.ContactInput{Name: "Ayşe Yılmaz", Department: ptr("BT"), Title: ptr("Müdür"), Email: ptr("ayse@x.test")})
	if err != nil {
		t.Fatal(err)
	}
	dev, err := contacts.Create(ctx, org, store.ContactInput{Name: "Ali Kaya", ManagerContactID: &boss.ID, Phone: ptr("+905551112233")})
	if err != nil || dev.ManagerContactID == nil || *dev.ManagerContactID != boss.ID {
		t.Fatalf("contact with a manager: %+v err=%v", dev, err)
	}

	// Telefon ya da e-postadan en az biri şart (veritabanı da reddeder).
	if _, err := contacts.Create(ctx, org, store.ContactInput{Name: "İletişimsiz"}); err == nil {
		t.Fatal("a contact with neither phone nor email was accepted")
	}
	// Yönetici başka organizasyondan olamaz.
	foreign, _ := contacts.Create(ctx, other, store.ContactInput{Name: "Yabancı", Email: ptr("y@x.test")})
	if _, err := contacts.Create(ctx, org, store.ContactInput{Name: "X", Email: ptr("x@x.test"), ManagerContactID: &foreign.ID}); err == nil {
		t.Fatal("a manager from another organization was accepted")
	}
	// Kendi yöneticisi olamaz.
	if _, err := contacts.Update(ctx, boss.ID, store.ContactInput{Name: boss.Name, Email: ptr("ayse@x.test"), ManagerContactID: &boss.ID}); err == nil {
		t.Fatal("a contact as its own manager was accepted")
	}

	list, err := contacts.ListByOrganization(ctx, org)
	if err != nil || len(list) != 2 {
		t.Fatalf("list = %d err=%v, want 2 (the other organization's contact must not appear)", len(list), err)
	}
	upd, err := contacts.Update(ctx, dev.ID, store.ContactInput{Name: "Ali K.", Phone: ptr(""), Email: ptr("ali@x.test")})
	if err != nil || upd.Name != "Ali K." || upd.Phone != nil || upd.ManagerContactID != nil {
		t.Fatalf("update: %+v err=%v (empty text clears the field)", upd, err)
	}
	if _, err := contacts.Update(ctx, uuid.New(), store.ContactInput{Name: "x", Email: ptr("x@x.test")}); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("update unknown: err=%v", err)
	}

	// Yöneticinin silinmesi bağlı kişiyi silmez; yalnızca bağı temizler.
	if err := contacts.Delete(ctx, boss.ID); err != nil {
		t.Fatal(err)
	}
	got, err := contacts.GetByID(ctx, dev.ID)
	if err != nil || got.ManagerContactID != nil {
		t.Fatalf("after deleting the manager: %+v err=%v", got, err)
	}
	if err := contacts.Delete(ctx, boss.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("delete twice: err=%v", err)
	}
}

// resolve, alıcıları "kanal:adres" olarak (kanalı kapalıysa sonuna " (kapalı)" eklenerek) sıralı döndürür.
func resolve(t *testing.T, n *store.Notifications, host, org uuid.UUID, level string) string {
	t.Helper()
	rs, err := n.ResolveRecipients(context.Background(), host, org, level)
	if err != nil {
		t.Fatal(err)
	}
	out := []string{}
	for _, r := range rs {
		s := r.Channel + ":" + r.Address
		if r.ChannelOff {
			s += " (kapalı)"
		}
		out = append(out, s)
	}
	sort.Strings(out)
	return strings.Join(out, ", ")
}

func TestNotificationRecipientResolution(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	n := store.NewNotifications(pool)
	contacts := store.NewContacts(pool)
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	check := func(what string, host, org uuid.UUID, level, want string) {
		t.Helper()
		if got := resolve(t, n, host, org, level); got != want {
			t.Errorf("%s (%s):\n got  %s\n want %s", what, level, got, want)
		}
	}

	holding := testdb.Org(t, pool, "holding")
	acme := testdb.ChildOrg(t, pool, "acme", holding)
	child := testdb.ChildOrg(t, pool, "acme-ist", acme)
	other := testdb.Org(t, pool, "other")
	host := testdb.PushHost(t, pool, acme, "web", "h")
	host2 := testdb.PushHost(t, pool, acme, "db", "h")
	childHost := testdb.PushHost(t, pool, child, "web", "h")
	otherHost := testdb.PushHost(t, pool, other, "web", "h")

	testdb.User(t, pool, "root@x.test", "super_admin", "pw")
	holdingAdmin := testdb.User(t, pool, "holding-admin@x.test", "org_admin", "pw")
	acmeAdmin := testdb.User(t, pool, "acme-admin@x.test", "org_admin", "pw")
	childAdmin := testdb.User(t, pool, "child-admin@x.test", "org_admin", "pw")
	testdb.AssignOrg(t, pool, holdingAdmin, holding)
	testdb.AssignOrg(t, pool, acmeAdmin, acme)

	// 1) Varsayılan alıcı yoktur: super_admin ya da org_admin olmak bildirim almak demek değildir.
	check("no owners, no rules", host, acme, model.AlertLevelCritical, "")

	// 2) Sistem sahipleri: her açık kanaldan, kanalın sahip seviyesinden itibaren; sahip o kanaldan almak istemiyorsa almaz.
	exec(`UPDATE notification_channels SET enabled = true WHERE channel = 'email'`) // sahip seviyesi warning
	exec(`INSERT INTO notification_channels (channel, provider, enabled, owner_min_level) VALUES ('sms', 'test', true, 'critical')`)
	exec(`INSERT INTO notification_owners (name, email, phone) VALUES ('Ali', 'ali@x.test', '+905550000001')`)
	exec(`INSERT INTO notification_owners (name, email) VALUES ('NOC', 'noc@x.test')`)
	exec(`INSERT INTO notification_owners (name, email, phone, email_enabled) VALUES ('Veli', 'veli@x.test', '+905550000002', false)`)
	owners := "email:ali@x.test, email:noc@x.test"
	ownersCritical := "email:ali@x.test, email:noc@x.test, sms:+905550000001, sms:+905550000002"
	check("owners", host, acme, model.AlertLevelWarning, owners)
	check("owners", host, acme, model.AlertLevelCritical, ownersCritical)
	check("owners below their level", host, acme, model.AlertLevelInfo, "")
	check("owners, unrelated organization", otherHost, other, model.AlertLevelWarning, owners)

	// 3) Kurallar toplanır: üst organizasyonların, organizasyonun ve sunucunun kuralları birlikte geçerlidir.
	ayse, err := contacts.Create(ctx, acme, store.ContactInput{Name: "Ayşe", Email: ptr("ayse@musteri.test"), Phone: ptr("+905550000003")})
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range []model.NotificationRoute{
		{OrganizationID: &holding, UserID: &holdingAdmin, Channel: model.ChannelEmail, MinLevel: model.AlertLevelWarning},
		{OrganizationID: &acme, ContactID: &ayse.ID, Channel: model.ChannelEmail, MinLevel: model.AlertLevelCritical},
		{OrganizationID: &child, UserID: &childAdmin, Channel: model.ChannelEmail, MinLevel: model.AlertLevelWarning},
		{HostID: &host, UserID: &acmeAdmin, Channel: model.ChannelEmail, MinLevel: model.AlertLevelInfo},
	} {
		if _, err := n.Create(ctx, r); err != nil {
			t.Fatal(err)
		}
	}
	check("host with its own rule", host, acme, model.AlertLevelInfo, "email:acme-admin@x.test")
	check("host with its own rule", host, acme, model.AlertLevelWarning,
		"email:acme-admin@x.test, email:ali@x.test, email:holding-admin@x.test, email:noc@x.test")
	check("host with its own rule", host, acme, model.AlertLevelCritical,
		"email:acme-admin@x.test, email:ali@x.test, email:ayse@musteri.test, email:holding-admin@x.test, email:noc@x.test, sms:+905550000001, sms:+905550000002")
	check("host without its own rule", host2, acme, model.AlertLevelWarning, "email:ali@x.test, email:holding-admin@x.test, email:noc@x.test")
	check("child organization", childHost, child, model.AlertLevelCritical,
		"email:ali@x.test, email:ayse@musteri.test, email:child-admin@x.test, email:holding-admin@x.test, email:noc@x.test, sms:+905550000001, sms:+905550000002")
	check("unrelated organization", otherHost, other, model.AlertLevelCritical, ownersCritical)

	// 4) Aynı kanal ve adres bir kez: sahip olan biri bir kuralda da geçse tek ileti alır (büyük/küçük harf fark etmez).
	nocContact, err := contacts.Create(ctx, acme, store.ContactInput{Name: "NOC masası", Email: ptr("NOC@x.test")})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := n.Create(ctx, model.NotificationRoute{OrganizationID: &acme, ContactID: &nocContact.ID, Channel: model.ChannelEmail, MinLevel: model.AlertLevelWarning}); err != nil {
		t.Fatal(err)
	}
	check("owner also in a rule", host2, acme, model.AlertLevelWarning, "email:ali@x.test, email:holding-admin@x.test, email:noc@x.test")

	// 5) Kapalı kanal: sahipler o kanaldan hiç gelmez; kurallar "kapalı" işaretiyle döner (motor atlar ve loglar).
	if _, err := n.Create(ctx, model.NotificationRoute{HostID: &host2, ContactID: &ayse.ID, Channel: model.ChannelSMS, MinLevel: model.AlertLevelWarning}); err != nil {
		t.Fatal(err)
	}
	check("sms rule", host2, acme, model.AlertLevelWarning, "email:ali@x.test, email:holding-admin@x.test, email:noc@x.test, sms:+905550000003")
	exec(`UPDATE notification_channels SET enabled = false WHERE channel = 'sms'`)
	check("sms channel off", host2, acme, model.AlertLevelCritical,
		"email:ali@x.test, email:ayse@musteri.test, email:holding-admin@x.test, email:noc@x.test, sms:+905550000003 (kapalı)")
	exec(`UPDATE notification_channels SET enabled = false WHERE channel = 'email'`)
	check("every channel off", host2, acme, model.AlertLevelWarning, // sahip gidince kişinin kendi yazımı görünür
		"email:NOC@x.test (kapalı), email:holding-admin@x.test (kapalı), sms:+905550000003 (kapalı)")
}

func TestNotificationRouteConstraints(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	n := store.NewNotifications(pool)
	contacts := store.NewContacts(pool)

	org := testdb.Org(t, pool, "acme")
	host := testdb.PushHost(t, pool, org, "web", "h")
	admin := testdb.User(t, pool, "a@x.test", "org_admin", "pw")
	c, _ := contacts.Create(ctx, org, store.ContactInput{Name: "Ayşe", Email: ptr("ayse@x.test")})

	bad := map[string]model.NotificationRoute{
		"no scope":        {UserID: &admin, Channel: "email", MinLevel: "warning"},
		"two scopes":      {OrganizationID: &org, HostID: &host, UserID: &admin, Channel: "email", MinLevel: "warning"},
		"no recipient":    {OrganizationID: &org, Channel: "email", MinLevel: "warning"},
		"two recipients":  {OrganizationID: &org, UserID: &admin, ContactID: &c.ID, Channel: "email", MinLevel: "warning"},
		"unknown channel": {OrganizationID: &org, UserID: &admin, Channel: "pigeon", MinLevel: "warning"},
		"unknown level":   {OrganizationID: &org, UserID: &admin, Channel: "email", MinLevel: "loud"},
		"unknown org":     {OrganizationID: ptr(uuid.New()), UserID: &admin, Channel: "email", MinLevel: "warning"},
		"unknown user":    {OrganizationID: &org, UserID: ptr(uuid.New()), Channel: "email", MinLevel: "warning"},
		"unknown contact": {OrganizationID: &org, ContactID: ptr(uuid.New()), Channel: "email", MinLevel: "warning"},
	}
	for name, r := range bad {
		if _, err := n.Create(ctx, r); err == nil {
			t.Errorf("%s: the route was accepted", name)
		}
	}

	r, err := n.Create(ctx, model.NotificationRoute{OrganizationID: &org, UserID: &admin, Channel: "email", MinLevel: "warning"})
	if err != nil {
		t.Fatal(err)
	}
	// Aynı kapsam + alıcı + kanal ikinci kez eklenemez.
	if _, err := n.Create(ctx, model.NotificationRoute{OrganizationID: &org, UserID: &admin, Channel: "email", MinLevel: "critical"}); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("duplicate route: err=%v, want ErrConflict", err)
	}
	// Kanal notification_channels'ta tanımlı olmalı: sms'in satırı yokken kural eklenemez, satırı eklenince eklenebilir.
	sms := model.NotificationRoute{OrganizationID: &org, UserID: &admin, Channel: "sms", MinLevel: "critical"}
	if _, err := n.Create(ctx, sms); err == nil {
		t.Fatal("a rule on a channel without a notification_channels row was accepted")
	}
	if _, err := pool.Exec(ctx, `INSERT INTO notification_channels (channel, provider) VALUES ('sms', 'test')`); err != nil {
		t.Fatal(err)
	}
	if _, err := n.Create(ctx, sms); err != nil {
		t.Fatalf("sms rule once the channel exists: %v", err)
	}

	upd, err := n.Update(ctx, r.ID, "email", "critical")
	if err != nil || upd.MinLevel != "critical" {
		t.Fatalf("update: %+v err=%v", upd, err)
	}
	if _, err := n.Update(ctx, uuid.New(), "email", "critical"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("update unknown: err=%v", err)
	}

	list, err := n.ListByOrganization(ctx, org)
	if err != nil || len(list) != 2 || list[0].RecipientName == "" {
		t.Fatalf("list: %+v err=%v (recipient details must be filled in)", list, err)
	}

	// Alıcı (kullanıcı ya da kişi) silinince kuralı da gider; sahipsiz kural kalmaz.
	if err := contacts.Delete(ctx, c.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, admin); err != nil {
		t.Fatal(err)
	}
	if list, _ := n.ListByOrganization(ctx, org); len(list) != 0 {
		t.Fatalf("routes of deleted recipients survived: %+v", list)
	}
}

func TestRecipientCandidatesAreLimitedToTheScope(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	n := store.NewNotifications(pool)
	contacts := store.NewContacts(pool)

	holding := testdb.Org(t, pool, "holding")
	acme := testdb.ChildOrg(t, pool, "acme", holding)
	beta := testdb.ChildOrg(t, pool, "beta", holding)
	host := testdb.PushHost(t, pool, acme, "web", "h")

	root := testdb.User(t, pool, "root@x.test", "super_admin", "pw")
	acmeAdmin := testdb.User(t, pool, "acme-admin@x.test", "org_admin", "pw")
	holdingAdmin := testdb.User(t, pool, "holding-admin@x.test", "org_admin", "pw")
	betaAdmin := testdb.User(t, pool, "beta-admin@x.test", "org_admin", "pw")
	op := testdb.User(t, pool, "op@x.test", "operator", "pw")
	otherOp := testdb.User(t, pool, "op2@x.test", "operator", "pw")
	testdb.AssignOrg(t, pool, acmeAdmin, acme)
	testdb.AssignOrg(t, pool, holdingAdmin, holding)
	testdb.AssignOrg(t, pool, betaAdmin, beta)
	testdb.AssignHost(t, pool, op, host)
	testdb.AssignHost(t, pool, otherOp, testdb.PushHost(t, pool, beta, "x", "h"))
	mine, _ := contacts.Create(ctx, acme, store.ContactInput{Name: "Ayşe", Email: ptr("a@m.test")})
	contacts.Create(ctx, beta, store.ContactInput{Name: "Başkası", Email: ptr("b@m.test")})
	parentContact, _ := contacts.Create(ctx, holding, store.ContactInput{Name: "Genel", Email: ptr("g@m.test")})

	users := func(cs []store.Candidate) map[uuid.UUID]bool {
		m := map[uuid.UUID]bool{}
		for _, c := range cs {
			if c.UserID != nil {
				m[*c.UserID] = true
			}
		}
		return m
	}
	contactIDs := func(cs []store.Candidate) map[uuid.UUID]bool {
		m := map[uuid.UUID]bool{}
		for _, c := range cs {
			if c.ContactID != nil {
				m[*c.ContactID] = true
			}
		}
		return m
	}

	// Organizasyon kapsamı: zincirdeki yöneticiler ve kişiler; kardeş dal ve operatörler (sunucu kapsamı değil) yok.
	cs, err := n.Candidates(ctx, acme, nil)
	if err != nil {
		t.Fatal(err)
	}
	u, c := users(cs), contactIDs(cs)
	if !u[root] || !u[acmeAdmin] || !u[holdingAdmin] || u[betaAdmin] || u[op] || u[otherOp] || len(u) != 3 {
		t.Fatalf("organization users = %v", u)
	}
	if !c[mine.ID] || !c[parentContact.ID] || len(c) != 2 {
		t.Fatalf("organization contacts = %v", c)
	}

	// Sunucu kapsamı: bu sunucuya atanmış operatör de aday olur.
	cs, err = n.Candidates(ctx, acme, &host)
	if err != nil {
		t.Fatal(err)
	}
	if u := users(cs); !u[op] || u[otherOp] || len(u) != 4 {
		t.Fatalf("host users = %v", u)
	}
}
