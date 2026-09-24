package alertengine_test

import (
	"context"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"healthbeat-server/internal/alertengine"
	"healthbeat-server/internal/model"
	"healthbeat-server/internal/notify"
	"healthbeat-server/internal/store"
	"healthbeat-server/internal/testdb"
	"healthbeat-server/internal/testsmtp"
)

type env struct {
	ctx    context.Context
	pool   *pgxpool.Pool
	engine *alertengine.Engine
	alerts *store.Alerts
	smtp   *testsmtp.Server
	org    uuid.UUID
	host   uuid.UUID
	admin  uuid.UUID // super_admin; alert'leri onaylayan kullanıcı olarak kullanılır
}

// newEnv, migration'ları uygulanmış bir şema üzerinde gerçek bir engine kurar: bir org, bir push
// host, global cpu eşiği 80 (uyarı) / 95 (kritik), bir super_admin alıcı ve e-postalar
// denetlenebilsin diye gerçek bir SMTP dinleyicisi.
func newEnv(t *testing.T) *env {
	t.Helper()
	return newEnvWithPanel(t, "https://panel.test")
}

func newEnvWithPanel(t *testing.T, panelBaseURL string) *env {
	t.Helper()
	pool := testdb.New(t)
	smtp := testsmtp.Start(t)
	mailer := notify.New(notify.Config{Host: smtp.Host, Port: smtp.Port, From: "hb@x.test"})

	org := testdb.Org(t, pool, "acme")
	host := testdb.PushHost(t, pool, org, "web-1", "h")
	testdb.Threshold(t, pool, nil, nil, "cpu", 80, 95)
	admin := testdb.User(t, pool, "root@x.test", "super_admin", "pw") // her zaman bildirilir

	return &env{
		ctx: context.Background(), pool: pool, smtp: smtp, org: org, host: host, admin: admin,
		engine: alertengine.New(pool, mailer, panelBaseURL),
		alerts: store.NewAlerts(pool),
	}
}

func (e *env) feedCPU(cpu float64) {
	e.engine.EvaluateMetrics(e.ctx, e.host, e.org, cpu, 10, nil)
}

// messages, kuyruktaki alert e-postalarının teslimini bekler, sonra onları döndürür.
func (e *env) messages() []testsmtp.Message {
	e.engine.Flush()
	return e.smtp.Messages()
}

func (e *env) rows(t *testing.T, metric string) []model.Alert {
	t.Helper()
	all, _, err := e.alerts.List(e.ctx, "", store.ListParams{})
	if err != nil {
		t.Fatal(err)
	}
	var out []model.Alert
	for _, a := range all {
		if a.AlertType == metric {
			out = append(out, a)
		}
	}
	return out
}

func TestNoAlertBelowWarning(t *testing.T) {
	e := newEnv(t)
	e.feedCPU(79.9)
	if n := len(e.rows(t, "cpu")); n != 0 {
		t.Fatalf("%d alerts below the warning level", n)
	}
	if n := len(e.messages()); n != 0 {
		t.Fatalf("%d emails below the warning level", n)
	}
}

func TestLevelBoundaries(t *testing.T) {
	// değer >= seviye tetikler, sınırın tam üzeri dahil.
	for _, c := range []struct {
		cpu  float64
		want string
	}{{80, "warning"}, {94.99, "warning"}, {95, "critical"}, {100, "critical"}} {
		e := newEnv(t)
		e.feedCPU(c.cpu)
		rows := e.rows(t, "cpu")
		if len(rows) != 1 || rows[0].Level != c.want {
			t.Errorf("cpu=%v: alerts=%+v, want one %s", c.cpu, rows, c.want)
		}
	}
}

func TestDedupEscalateResolveRecur(t *testing.T) {
	e := newEnv(t)

	e.feedCPU(85)
	e.feedCPU(86)
	e.feedCPU(87)
	rows := e.rows(t, "cpu")
	if len(rows) != 1 || rows[0].Level != model.AlertLevelWarning || rows[0].Status != model.AlertStatusOpen {
		t.Fatalf("after 3 warning readings: %+v, want a single open warning", rows)
	}
	if n := len(e.messages()); n != 1 {
		t.Fatalf("%d emails for one incident, want 1 (dedup)", n)
	}
	firstID := rows[0].ID

	e.feedCPU(97) // ikinci bir alert açmak yerine aynı alert'i yükseltir
	rows = e.rows(t, "cpu")
	if len(rows) != 1 || rows[0].ID != firstID || rows[0].Level != model.AlertLevelCritical {
		t.Fatalf("after escalation: %+v, want the same alert now critical", rows)
	}
	// Yükselme kendi e-postasını gönderir: uyarıda "vaktim var" diyen biri kritiğe geçtiğinde
	// bundan habersiz kalmamalı — daha önce bilgilendirilmiş olsa bile tekrar yazılır.
	msgs := e.messages()
	if len(msgs) != 2 {
		t.Fatalf("%d emails total after escalation, want 2 (open + escalated)", len(msgs))
	}
	if !strings.Contains(msgs[1].Text(), "Seviye: KRİTİK") {
		t.Errorf("escalation email missing Seviye: KRİTİK:\n%s", msgs[1].Text())
	}

	e.feedCPU(20) // toparlanma kendiliğinden çözer
	rows = e.rows(t, "cpu")
	if len(rows) != 1 || rows[0].Status != model.AlertStatusResolved || rows[0].ResolvedAt == nil {
		t.Fatalf("after recovery: %+v, want resolved", rows)
	}
	msgs = e.messages() // çözülme kendi e-postasını gönderir
	if len(msgs) != 3 {
		t.Fatalf("%d emails total after recovery, want 3 (open + escalated + resolved)", len(msgs))
	}
	if !strings.Contains(msgs[2].Text(), "ÇÖZÜLDÜ") {
		t.Errorf("recovery email missing ÇÖZÜLDÜ:\n%s", msgs[2].Text())
	}
	// Regresyon: çözülme e-postası GERÇEK çözülme okumasını göstermeli, alert'in son yükseltildiği
	// (hâlâ eşik üstü) eski okumayı değil — yoksa "Değer: %97 (eşik: %80)" gibi eşiğin hâlâ
	// aşılıyormuş görünen, çözülmeyle çelişen bir mail çıkar.
	if !strings.Contains(msgs[2].Text(), "Değer: %20,00 (eşik: %80,00)") {
		t.Errorf("resolved email must show the resolving reading (20,00), not the stale escalated one:\n%s", msgs[2].Text())
	}

	e.feedCPU(90) // yeni bir olay yeni bir alert ve yeni bir e-postadır
	rows = e.rows(t, "cpu")
	if len(rows) != 2 {
		t.Fatalf("after recurrence: %d alerts, want 2", len(rows))
	}
	if n := len(e.messages()); n != 4 {
		t.Fatalf("%d emails total after recurrence, want 4 (open + escalated + resolved + new open)", n)
	}
}

// ---- onay: "gördüm, sustur ama izle" (docs/MIMARI.md bölüm 8) ---------------------------------------------------

func (e *env) acknowledge(t *testing.T, a model.Alert) {
	t.Helper()
	if _, err := e.alerts.Acknowledge(e.ctx, a.ID, e.admin); err != nil {
		t.Fatal(err)
	}
}

func (e *env) activeCPU(t *testing.T) model.Alert {
	t.Helper()
	a, err := e.alerts.GetActive(e.ctx, e.host, "cpu")
	if err != nil {
		t.Fatal(err)
	}
	return a
}

// Onaylanan alert, metrik eşiğin üstünde kaldıkça yeni bir alert ya da e-posta doğurmaz; eşik altına inince çözülür
// ve "ÇÖZÜLDÜ" e-postası gider.
func TestAcknowledgedAlertIsSilencedButWatched(t *testing.T) {
	e := newEnv(t)
	e.feedCPU(90)
	a := e.activeCPU(t)
	e.acknowledge(t, a)

	e.feedCPU(90)
	e.feedCPU(91)
	rows := e.rows(t, "cpu")
	if len(rows) != 1 || rows[0].Status != model.AlertStatusAcknowledged {
		t.Fatalf("alerts after acknowledge = %+v, want the single acknowledged alert", rows)
	}
	if n := len(e.messages()); n != 1 {
		t.Fatalf("%d emails, want 1: acknowledging must silence the same event", n)
	}

	e.feedCPU(10)
	got, _ := e.alerts.GetByID(e.ctx, a.ID)
	if got.Status != model.AlertStatusResolved || got.ResolvedAt == nil {
		t.Fatalf("status after recovery = %q, want resolved", got.Status)
	}
	msgs := e.messages()
	if len(msgs) != 2 || !strings.Contains(msgs[1].Text(), "ÇÖZÜLDÜ") {
		t.Fatalf("emails = %d, want the open one and a ÇÖZÜLDÜ one", len(msgs))
	}
}

// Onaylanmış bir alert yükselirse (uyarı → kritik) durum ciddileşmiştir: e-posta gider ve onay kalkar. Düşüşte
// e-posta yine gider ama onay korunur.
func TestEscalationReopensAcknowledgedAlert(t *testing.T) {
	e := newEnv(t)
	e.feedCPU(85) // uyarı
	e.acknowledge(t, e.activeCPU(t))

	e.feedCPU(97) // kritik
	a := e.activeCPU(t)
	if a.Level != model.AlertLevelCritical || a.Status != model.AlertStatusOpen || a.AcknowledgedAt != nil || a.AcknowledgedBy != nil {
		t.Fatalf("after escalation = %+v, want critical, open and no acknowledgement", a)
	}
	if n := len(e.messages()); n != 2 {
		t.Fatalf("%d emails, want 2 (open + escalation)", n)
	}

	e.acknowledge(t, a)
	e.feedCPU(85) // yeniden uyarı
	a = e.activeCPU(t)
	if a.Level != model.AlertLevelWarning || a.Status != model.AlertStatusAcknowledged || a.AcknowledgedBy == nil {
		t.Fatalf("after de-escalation = %+v, want warning and still acknowledged", a)
	}
	if n := len(e.messages()); n != 3 {
		t.Fatalf("%d emails, want 3 (level changes are always notified)", n)
	}
	if rows := e.rows(t, "cpu"); len(rows) != 1 {
		t.Fatalf("%d cpu alerts, want the same single alert throughout", len(rows))
	}
}

func TestAcknowledgedOfflineAlertResolvesWhenHostReturns(t *testing.T) {
	e := newEnv(t)
	e.engine.RaiseOffline(e.ctx, e.host, e.org)
	a, err := e.alerts.GetActive(e.ctx, e.host, model.AlertTypeHostOffline)
	if err != nil {
		t.Fatal(err)
	}
	e.acknowledge(t, a)

	e.engine.RaiseOffline(e.ctx, e.host, e.org) // hâlâ sessiz
	if rows := e.rows(t, model.AlertTypeHostOffline); len(rows) != 1 {
		t.Fatalf("%d offline alerts, want 1: the acknowledged one still covers it", len(rows))
	}

	e.engine.ResolveOffline(e.ctx, e.host, e.org)
	if got, _ := e.alerts.GetByID(e.ctx, a.ID); got.Status != model.AlertStatusResolved {
		t.Fatalf("status = %q, want resolved once the host reports again", got.Status)
	}
	if n := len(e.messages()); n != 2 {
		t.Fatalf("%d emails, want 2 (offline + back online)", n)
	}
}

func TestAcknowledgedDiskAlertResolvesWhenMountDisappears(t *testing.T) {
	e := newEnv(t)
	e.diskEnv(t)
	e.feedDisks(map[string]float64{"/": 90, "/data": 10})
	a, err := e.alerts.GetActiveSubject(e.ctx, e.host, model.MetricTypeDisk, "/")
	if err != nil {
		t.Fatal(err)
	}
	e.acknowledge(t, a)

	e.feedDisks(map[string]float64{"/data": 10}) // "/" artık raporlanmıyor
	if got, _ := e.alerts.GetByID(e.ctx, a.ID); got.Status != model.AlertStatusResolved {
		t.Fatalf("status = %q, want resolved when the mount is gone", got.Status)
	}
}

func TestOverridePrecedenceDrivesAlerts(t *testing.T) {
	e := newEnv(t)
	// Host düzeyindeki geçersiz kılma, global 80/95'ten çok daha katıdır.
	testdb.Threshold(t, e.pool, nil, &e.host, "cpu", 30, 50)

	e.feedCPU(55)
	rows := e.rows(t, "cpu")
	if len(rows) != 1 || rows[0].Level != model.AlertLevelCritical {
		t.Fatalf("alerts = %+v, want critical under the host override", rows)
	}
}

func TestNoThresholdNoAlert(t *testing.T) {
	e := newEnv(t)
	e.engine.EvaluateMetrics(e.ctx, e.host, e.org, 1, 100, nil) // ram'in hiç eşiği yok
	if rows := e.rows(t, "ram"); len(rows) != 0 {
		t.Fatalf("ram alerts = %+v with no ram threshold configured", rows)
	}
}

func TestEmailGoesToSuperAdminsAndThatOrgsAdminsOnly(t *testing.T) {
	e := newEnv(t)
	otherOrg := testdb.Org(t, e.pool, "other")
	adminHere := testdb.User(t, e.pool, "admin-acme@x.test", "org_admin", "pw")
	adminElsewhere := testdb.User(t, e.pool, "admin-other@x.test", "org_admin", "pw")
	operator := testdb.User(t, e.pool, "op@x.test", "operator", "pw")
	testdb.AssignOrg(t, e.pool, adminHere, e.org)
	testdb.AssignOrg(t, e.pool, adminElsewhere, otherOrg)
	testdb.AssignHost(t, e.pool, operator, e.host)

	e.feedCPU(97)

	msgs := e.messages()
	if len(msgs) != 1 {
		t.Fatalf("%d emails, want 1", len(msgs))
	}
	to := append([]string(nil), msgs[0].To...)
	sort.Strings(to)
	if strings.Join(to, ",") != "admin-acme@x.test,root@x.test" {
		t.Fatalf("recipients = %v", to)
	}
	for _, want := range []string{"CPU kullanım uyarısı", "Organizasyon: acme", "Title: web-1", "Seviye: KRİTİK", "Panel: https://panel.test/hosts/" + e.host.String()} {
		if !strings.Contains(msgs[0].Text(), want) {
			t.Errorf("email missing %q:\n%s", want, msgs[0].Text())
		}
	}
}

// panelBaseURL boşsa (kurulumda ayarlanmamışsa) e-postaya asla yarım/geçersiz bir bağlantı
// eklenmemeli; satır tamamen atlanır.
func TestEmailOmitsPanelLinkWhenNotConfigured(t *testing.T) {
	e := newEnvWithPanel(t, "")
	e.feedCPU(97)

	msgs := e.messages()
	if len(msgs) != 1 {
		t.Fatalf("%d emails, want 1", len(msgs))
	}
	if strings.Contains(msgs[0].Text(), "Panel:") {
		t.Errorf("email must not contain a Panel: line when panelBaseURL is empty:\n%s", msgs[0].Text())
	}
}

// Bildirim kuralı olan kapsamda yalnızca kuralın alıcıları (panel kullanıcısı ya da iletişim kişisi) bilgilendirilir;
// varsayılan alıcılar (super_admin, org_admin) devre dışı kalır.
func TestNotificationRuleReplacesTheDefaultRecipients(t *testing.T) {
	e := newEnv(t)
	contact, err := store.NewContacts(e.pool).Create(e.ctx, e.org, store.ContactInput{Name: "Ayşe", Email: ptr("ayse@musteri.test")})
	if err != nil {
		t.Fatal(err)
	}
	routes := store.NewNotifications(e.pool)
	if _, err := routes.Create(e.ctx, model.NotificationRoute{
		OrganizationID: &e.org, ContactID: &contact.ID, Channel: model.ChannelEmail, MinLevel: model.AlertLevelCritical,
	}); err != nil {
		t.Fatal(err)
	}

	e.feedCPU(85) // warning: kuralın en düşük seviyesinin altında, kimse bilgilendirilmez (root da değil)
	if n := len(e.messages()); n != 0 {
		t.Fatalf("%d emails for a warning below the rule's level, want 0", n)
	}
	e.feedCPU(97) // aynı alert kritik olur
	msgs := e.messages()
	if len(msgs) != 1 || len(msgs[0].To) != 1 || msgs[0].To[0] != "ayse@musteri.test" {
		t.Fatalf("emails = %+v, want exactly one, to the contact only", msgs)
	}
}

// Sunucunun kendi kuralı organizasyonunkini ezer.
func TestHostNotificationRuleOverridesTheOrganizationRule(t *testing.T) {
	e := newEnv(t)
	orgAdmin := testdb.User(t, e.pool, "org@x.test", "org_admin", "pw")
	hostAdmin := testdb.User(t, e.pool, "host@x.test", "org_admin", "pw")
	testdb.AssignOrg(t, e.pool, orgAdmin, e.org)
	testdb.AssignOrg(t, e.pool, hostAdmin, e.org)
	routes := store.NewNotifications(e.pool)
	for _, r := range []model.NotificationRoute{
		{OrganizationID: &e.org, UserID: &orgAdmin, Channel: model.ChannelEmail, MinLevel: model.AlertLevelWarning},
		{HostID: &e.host, UserID: &hostAdmin, Channel: model.ChannelEmail, MinLevel: model.AlertLevelWarning},
	} {
		if _, err := routes.Create(e.ctx, r); err != nil {
			t.Fatal(err)
		}
	}
	e.feedCPU(90)
	msgs := e.messages()
	if len(msgs) != 1 || len(msgs[0].To) != 1 || msgs[0].To[0] != "host@x.test" {
		t.Fatalf("emails = %+v, want one to the host rule's recipient only", msgs)
	}
}

// Alert seviyesi değiştiğinde (warning->critical YA DA critical->warning, alert hâlâ açıkken),
// o anki seviyenin TÜM alıcılarına gider — daha önce başka seviyede bilgilendirilmiş olan da dahil:
// kimse durumun kötüleştiğinden ya da iyileştiğinden habersiz kalmamalı. Yalnızca AYNI seviyede
// kalmak yeni e-posta üretmez.
func TestEscalationNotifiesTheFullNewLevelAudience(t *testing.T) {
	e := newEnv(t)
	early := testdb.User(t, e.pool, "early@x.test", "org_admin", "pw")
	late := testdb.User(t, e.pool, "late@x.test", "org_admin", "pw")
	testdb.AssignOrg(t, e.pool, early, e.org)
	testdb.AssignOrg(t, e.pool, late, e.org)
	routes := store.NewNotifications(e.pool)
	for _, r := range []model.NotificationRoute{
		{OrganizationID: &e.org, UserID: &early, Channel: model.ChannelEmail, MinLevel: model.AlertLevelWarning},
		{OrganizationID: &e.org, UserID: &late, Channel: model.ChannelEmail, MinLevel: model.AlertLevelCritical},
	} {
		if _, err := routes.Create(e.ctx, r); err != nil {
			t.Fatal(err)
		}
	}

	e.feedCPU(85)
	msgs := e.messages()
	if len(msgs) != 1 || len(msgs[0].To) != 1 || msgs[0].To[0] != "early@x.test" {
		t.Fatalf("warning emails = %+v, want one to early@ only", msgs)
	}
	e.feedCPU(97)
	msgs = e.messages()
	if len(msgs) != 2 {
		t.Fatalf("%d emails total after escalation, want 2", len(msgs))
	}
	to := append([]string(nil), msgs[1].To...)
	sort.Strings(to)
	if strings.Join(to, ",") != "early@x.test,late@x.test" {
		t.Fatalf("escalation recipients = %v, want both early@ and late@ (full critical-level audience)", to)
	}
	e.feedCPU(98) // aynı seviyede kalmak yeni e-posta üretmez
	if n := len(e.messages()); n != 2 {
		t.Fatalf("%d emails in total, want still 2 (same level)", n)
	}

	e.feedCPU(85) // seviye düşer (kritikten uyarıya, alert hâlâ açık) — bu da kendi e-postasını gönderir
	msgs = e.messages()
	if len(msgs) != 3 {
		t.Fatalf("%d emails total after de-escalation, want 3", len(msgs))
	}
	to = append([]string(nil), msgs[2].To...)
	sort.Strings(to)
	if strings.Join(to, ",") != "early@x.test" {
		t.Fatalf("de-escalation recipients = %v, want only early@ (late@'s min_level is critical)", to)
	}
	if !strings.Contains(msgs[2].Text(), "Seviye: UYARI") {
		t.Errorf("de-escalation email missing Seviye: UYARI:\n%s", msgs[2].Text())
	}
}

// Regresyon (canlıda yakalandı): kritiğe yükselip sonra uyarıya düşen ve en son eşiğin
// tamamen altına inerek çözülen bir alert'te, çözülme e-postası GERÇEK çözülme okumasını
// göstermeli — kritikten uyarıya düşüldüğü andaki eski okumayı değil. Aksi hâlde "Değer" eşiğin
// hâlâ üstündeymiş gibi görünür ve okuyan "eşik üstüyken nasıl çözüldü?" diye kafası karışır.
func TestResolvedEmailShowsTheResolvingReadingNotTheLastEscalatedOne(t *testing.T) {
	e := newEnv(t)

	e.feedCPU(85) // uyarı açılır
	e.feedCPU(97) // kritiğe yükselir
	e.feedCPU(85) // uyarıya düşer (hâlâ açık, eski okuması burada 85 olurdu)
	e.feedCPU(20) // eşiğin tamamen altına iner: çözülür

	rows := e.rows(t, "cpu")
	if len(rows) != 1 || rows[0].Status != model.AlertStatusResolved {
		t.Fatalf("after full recovery: %+v, want resolved", rows)
	}
	msgs := e.messages()
	if len(msgs) != 4 { // açık, kritiğe yükseldi, uyarıya düştü, çözüldü
		t.Fatalf("%d emails total, want 4", len(msgs))
	}
	// Teslimat 2 paralel işçiyle yapılır: kuyruğa alma sırası korunsa da, e-postaların
	// SMTP'ye ulaşma sırası garantili değildir — ÇÖZÜLDÜ olanı konuma göre değil içeriğe
	// göre bulunur.
	var resolvedEmail *testsmtp.Message
	for i := range msgs {
		if strings.Contains(msgs[i].Text(), "ÇÖZÜLDÜ") {
			resolvedEmail = &msgs[i]
			break
		}
	}
	if resolvedEmail == nil {
		t.Fatalf("no resolved (ÇÖZÜLDÜ) email among %d messages", len(msgs))
	}
	if !strings.Contains(resolvedEmail.Text(), "Değer: %20,00 (eşik: %80,00)") {
		t.Errorf("resolved email must show the resolving reading (20,00), not the stale de-escalated one (85):\n%s", resolvedEmail.Text())
	}
}

// Henüz uygulanmamış bir kanaldaki kural mail yerine sessizce yutulmaz: alert yine açılır, e-posta gitmez.
func TestRuleWithAnUnimplementedChannelSendsNoEmail(t *testing.T) {
	e := newEnv(t)
	admin := testdb.User(t, e.pool, "org@x.test", "org_admin", "pw")
	testdb.AssignOrg(t, e.pool, admin, e.org)
	if _, err := store.NewNotifications(e.pool).Create(e.ctx, model.NotificationRoute{
		OrganizationID: &e.org, UserID: &admin, Channel: model.ChannelSMS, MinLevel: model.AlertLevelWarning,
	}); err != nil {
		t.Fatal(err)
	}
	e.feedCPU(90)
	if n := len(e.messages()); n != 0 {
		t.Fatalf("%d emails through an sms rule, want none", n)
	}
	if rows := e.rows(t, "cpu"); len(rows) != 1 || rows[0].Status != model.AlertStatusOpen {
		t.Fatalf("the alert itself must still open: %+v", rows)
	}
}

// Alert kaydı tetikleyen değeri ve eşiği taşır (panel "%97,0 (eşik %95,0)" gösterebilsin).
func TestAlertRecordsTheValueAndThreshold(t *testing.T) {
	e := newEnv(t)
	e.feedCPU(97.5)
	rows := e.rows(t, "cpu")
	if len(rows) != 1 || rows[0].Value == nil || *rows[0].Value != 97.5 || rows[0].Threshold == nil || *rows[0].Threshold != 95 {
		t.Fatalf("alert = %+v, want value 97.5 and threshold 95 (critical level)", rows)
	}
}

func ptr[T any](v T) *T { return &v }

func TestOfflineAlertLifecycle(t *testing.T) {
	e := newEnv(t)

	e.engine.RaiseOffline(e.ctx, e.host, e.org)
	e.engine.RaiseOffline(e.ctx, e.host, e.org) // monitor hâlâ sessizken yeniden tetiklenir
	rows := e.rows(t, model.AlertTypeHostOffline)
	if len(rows) != 1 || rows[0].Level != model.AlertLevelCritical {
		t.Fatalf("offline alerts = %+v, want one critical", rows)
	}
	if n := len(e.messages()); n != 1 {
		t.Fatalf("%d emails for one outage, want 1", n)
	}

	e.engine.ResolveOffline(e.ctx, e.host, e.org) // host yeniden rapor verdi
	rows = e.rows(t, model.AlertTypeHostOffline)
	if rows[0].Status != model.AlertStatusResolved {
		t.Fatalf("status = %q, want resolved", rows[0].Status)
	}
	msgs := e.messages() // kümülatif: kesinti + toparlanma
	if len(msgs) != 2 {
		t.Fatalf("%d emails total after recovery, want 2 (outage + recovery)", len(msgs))
	}
	for _, want := range []string{"ÇÖZÜLDÜ", "sunucu tekrar çevrimiçi", "Çözülme:", "Çözüm Süresi:"} {
		if !strings.Contains(msgs[1].Text(), want) {
			t.Errorf("recovery email missing %q:\n%s", want, msgs[1].Text())
		}
	}

	e.engine.RaiseOffline(e.ctx, e.host, e.org) // yeniden sessizleşir: yeni kesinti
	if rows = e.rows(t, model.AlertTypeHostOffline); len(rows) != 2 {
		t.Fatalf("%d offline alerts, want 2", len(rows))
	}
}

// Regresyon: bir host için eşzamanlı alımlar eskiden "açık var mı?" denetimini hep birlikte
// geçip her biri kendi alert'ini ekliyordu (ölçüldü: 20 eşzamanlı okumadan 10 alert ve 10
// e-posta).
func TestConcurrentEvaluationsOpenExactlyOneAlertAndSendOneEmail(t *testing.T) {
	e := newEnv(t)

	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			e.feedCPU(97)
		}()
	}
	close(start)
	wg.Wait()

	if rows := e.rows(t, "cpu"); len(rows) != 1 || rows[0].Status != model.AlertStatusOpen {
		t.Fatalf("%d cpu alerts after 20 concurrent readings, want exactly 1 open", len(rows))
	}
	if n := len(e.messages()); n != 1 {
		t.Fatalf("%d emails for one incident, want 1", n)
	}
}

func TestConcurrentOfflineDetectionOpensExactlyOneAlert(t *testing.T) {
	e := newEnv(t)

	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			e.engine.RaiseOffline(e.ctx, e.host, e.org)
		}()
	}
	close(start)
	wg.Wait()

	if rows := e.rows(t, model.AlertTypeHostOffline); len(rows) != 1 {
		t.Fatalf("%d offline alerts, want 1", len(rows))
	}
	if n := len(e.messages()); n != 1 {
		t.Fatalf("%d emails, want 1", n)
	}
}

// ---- docker_restart: container başına bir alert ------------------------------------------------

func containers(counts map[string]int) []model.DockerContainerReport {
	var out []model.DockerContainerReport
	for name, n := range counts {
		out = append(out, model.DockerContainerReport{Name: name, Image: name + ":1", Status: "running", RestartCount: n})
	}
	return out
}

func (e *env) dockerAlerts(t *testing.T) map[string]model.Alert {
	t.Helper()
	open, err := e.alerts.ListActive(e.ctx, e.host, model.MetricTypeDockerRestart)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]model.Alert{}
	for _, a := range open {
		out[a.Subject] = a
	}
	return out
}

func (e *env) feedDocker(counts map[string]int) {
	e.engine.EvaluateDocker(e.ctx, e.host, e.org, containers(counts))
}

func TestDockerRestartAlertsAreOnePerContainer(t *testing.T) {
	e := newEnv(t)
	testdb.Threshold(t, e.pool, nil, nil, "docker_restart", 3, 10)

	e.feedDocker(map[string]int{"web": 2, "db": 0}) // uyarı seviyesinin altında
	if got := e.dockerAlerts(t); len(got) != 0 {
		t.Fatalf("alerts below the threshold: %v", got)
	}
	if n := len(e.messages()); n != 0 {
		t.Fatalf("%d emails below the threshold", n)
	}

	e.feedDocker(map[string]int{"web": 3, "db": 5, "cache": 1}) // web ve db onu birbirinden bağımsız geçer
	got := e.dockerAlerts(t)
	if len(got) != 2 || got["web"].Level != model.AlertLevelWarning || got["db"].Level != model.AlertLevelWarning {
		t.Fatalf("alerts = %+v, want separate warnings for web and db (not cache)", got)
	}
	if got["web"].ID == got["db"].ID {
		t.Fatal("two containers share one alert")
	}

	// Tekrarlanan raporlar alert'leri ya da e-postaları çoğaltmaz.
	e.feedDocker(map[string]int{"web": 3, "db": 5, "cache": 1})
	e.feedDocker(map[string]int{"web": 4, "db": 5, "cache": 1})
	if got := e.dockerAlerts(t); len(got) != 2 {
		t.Fatalf("%d alerts after repeated reports, want 2", len(got))
	}
	msgs := e.messages()
	if len(msgs) != 2 {
		t.Fatalf("%d emails, want one per container alert", len(msgs))
	}

	// E-posta container'ı adıyla söyler; böylece okuyan hangisine bakacağını bilir.
	joined := msgs[0].Text() + msgs[1].Text()
	for _, want := range []string{"container restart uyarısı (web)", "container restart uyarısı (db)", "Container: web", "Container: db", "docker restart"} {
		if !strings.Contains(joined, want) {
			t.Errorf("emails do not mention %q:\n%s", want, joined)
		}
	}

	// Bir container'ın yükselmesi diğerinin alert'ine dokunmaz.
	dbID := got["db"].ID
	e.feedDocker(map[string]int{"web": 12, "db": 5})
	got = e.dockerAlerts(t)
	if got["web"].Level != model.AlertLevelCritical || got["db"].Level != model.AlertLevelWarning || got["db"].ID != dbID {
		t.Fatalf("after web escalated: %+v", got)
	}
}

func TestDockerRestartAlertResolvesWhenTheContainerIsRecreatedOrRemoved(t *testing.T) {
	e := newEnv(t)
	testdb.Threshold(t, e.pool, nil, nil, "docker_restart", 3, 10)

	e.feedDocker(map[string]int{"web": 5, "db": 5, "old": 5})
	if len(e.dockerAlerts(t)) != 3 {
		t.Fatal("precondition: three open alerts")
	}

	// web yeniden oluşturulur (sayaç 0'a döner); eskisi rapordan kaybolur; db salınmaya devam eder.
	e.feedDocker(map[string]int{"web": 0, "db": 6})
	got := e.dockerAlerts(t)
	if len(got) != 1 || got["db"].Subject != "db" {
		t.Fatalf("open alerts after recreate/remove = %v, want only db", got)
	}

	// Çözülmüş bir alert, olay yeniden olursa YENİ bir alert (ve yeni bir e-posta) olarak geri gelebilir.
	before := len(e.messages())
	e.feedDocker(map[string]int{"web": 4, "db": 6})
	if got := e.dockerAlerts(t); len(got) != 2 {
		t.Fatalf("recurrence did not reopen an alert: %v", got)
	}
	if n := len(e.messages()); n != before+1 {
		t.Fatalf("%d emails after recurrence, want %d", n, before+1)
	}
}

// Agent, Docker toplaması başarısız olduğunda da boş container listesi yollar. Bunu "her
// container kayboldu" diye okumak tüm alert'leri çözüp sonraki iyi raporda yeniden bildirirdi.
func TestEmptyDockerReportLeavesAlertsAlone(t *testing.T) {
	e := newEnv(t)
	testdb.Threshold(t, e.pool, nil, nil, "docker_restart", 3, 10)
	e.feedDocker(map[string]int{"web": 5})
	first := e.dockerAlerts(t)["web"]
	sent := len(e.messages())

	e.engine.EvaluateDocker(e.ctx, e.host, e.org, nil)                             // toplayıcıda aksama
	e.engine.EvaluateDocker(e.ctx, e.host, e.org, []model.DockerContainerReport{}) // container yok
	if got := e.dockerAlerts(t); len(got) != 1 || got["web"].ID != first.ID {
		t.Fatalf("an empty report changed the alerts: %v", got)
	}

	e.feedDocker(map[string]int{"web": 5}) // Docker geri geldi
	if got := e.dockerAlerts(t); got["web"].ID != first.ID {
		t.Fatal("the alert was replaced by a new one after a hiccup")
	}
	if n := len(e.messages()); n != sent {
		t.Fatalf("%d emails after the hiccup, want no new one (%d)", n, sent)
	}
}

func TestDockerRestartRespectsThresholdScopeAndAbsence(t *testing.T) {
	e := newEnv(t)
	e.feedDocker(map[string]int{"web": 999}) // hiç docker_restart eşiği yapılandırılmamış
	if got := e.dockerAlerts(t); len(got) != 0 {
		t.Fatalf("alerts without any threshold: %v", got)
	}

	testdb.Threshold(t, e.pool, nil, nil, "docker_restart", 100, 200)
	testdb.Threshold(t, e.pool, nil, &e.host, "docker_restart", 1, 2) // bu host daha katı
	e.feedDocker(map[string]int{"web": 2})
	if got := e.dockerAlerts(t); got["web"].Level != model.AlertLevelCritical {
		t.Fatalf("host-level override not used: %v", got)
	}
}

// Aynı container üzerinde yarışan iki alım yine de tam olarak bir alert ve bir e-posta vermeli.
func TestConcurrentDockerEvaluationsOpenOneAlertPerContainer(t *testing.T) {
	e := newEnv(t)
	testdb.Threshold(t, e.pool, nil, nil, "docker_restart", 3, 10)

	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			e.feedDocker(map[string]int{"web": 5, "db": 5})
		}()
	}
	close(start)
	wg.Wait()

	if got := e.dockerAlerts(t); len(got) != 2 {
		t.Fatalf("%d open alerts after 20 concurrent reports of 2 containers, want 2", len(got))
	}
	if n := len(e.messages()); n != 2 {
		t.Fatalf("%d emails, want 2", n)
	}
}

// ---- mount başına disk alert'leri ---------------------------------------------------------------------

func mounts(pcts map[string]float64) []model.DiskUsage {
	var out []model.DiskUsage
	for m, p := range pcts {
		out = append(out, model.DiskUsage{Mount: m, UsedPct: p, Total: 100, Free: 100 - int64(p)})
	}
	return out
}

func (e *env) diskEnv(t *testing.T) {
	t.Helper()
	testdb.Threshold(t, e.pool, nil, nil, "disk", 85, 95)
}

func (e *env) selectMounts(t *testing.T, selection []string) { // nil = raporlanan her mount
	t.Helper()
	if err := store.NewHosts(e.pool, nil).SetDiskAlertMounts(e.ctx, e.host, selection == nil, selection); err != nil {
		t.Fatal(err)
	}
}

func (e *env) feedDisks(pcts map[string]float64) {
	e.engine.EvaluateMetrics(e.ctx, e.host, e.org, 1, 1, mounts(pcts))
}

func (e *env) diskAlerts(t *testing.T) map[string]model.Alert {
	t.Helper()
	open, err := e.alerts.ListActive(e.ctx, e.host, model.MetricTypeDisk)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]model.Alert{}
	for _, a := range open {
		out[a.Subject] = a
	}
	return out
}

func TestDiskAlertsArePerMountAndNameTheMount(t *testing.T) {
	e := newEnv(t)
	e.diskEnv(t)

	// Regresyon: eski davranış en kötü mount'tan, hangisi olduğunu söylemeden TEK bir alert üretirdi.
	e.feedDisks(map[string]float64{"/": 40, "/data": 96, "/boot": 90})
	got := e.diskAlerts(t)
	if len(got) != 2 || got["/data"].Level != model.AlertLevelCritical || got["/boot"].Level != model.AlertLevelWarning {
		t.Fatalf("alerts = %+v, want a critical for /data and a warning for /boot (and none for /)", got)
	}
	msgs := e.messages()
	if len(msgs) != 2 {
		t.Fatalf("%d emails, want one per alerting mount", len(msgs))
	}
	joined := msgs[0].Text() + msgs[1].Text()
	for _, want := range []string{"disk kullanım uyarısı (/data)", "disk kullanım uyarısı (/boot)", "Mount: /data", "Mount: /boot"} {
		if !strings.Contains(joined, want) {
			t.Errorf("emails do not say which mount is affected (%q missing):\n%s", want, joined)
		}
	}

	// Bağımsız yaşam döngüleri: /data toparlanırken /boot yükselir; /boot'la ilgili hiçbir şey kaybolmaz.
	bootID := got["/boot"].ID
	e.feedDisks(map[string]float64{"/": 40, "/data": 50, "/boot": 97})
	got = e.diskAlerts(t)
	if len(got) != 1 || got["/boot"].ID != bootID || got["/boot"].Level != model.AlertLevelCritical {
		t.Fatalf("after /data recovered and /boot escalated: %+v", got)
	}
	// Art arda dolan iki mount her biri kendi bildirimini üretir.
	e.feedDisks(map[string]float64{"/": 91, "/data": 50, "/boot": 97})
	if got := e.diskAlerts(t); len(got) != 2 {
		t.Fatalf("a second mount filling up did not get its own alert: %+v", got)
	}
	// Kümülatif: /data açık + /boot açık + /data çözüldü + /boot yükseldi (kritiğe geçiş kendi
	// e-postasını gönderir, alıcı zaten warning'de bilgilendirilmiş olsa bile) + / açık.
	if n := len(e.messages()); n != 5 {
		t.Fatalf("%d emails, want 5 (/data open, /boot open, /data resolved, /boot escalated, / open)", n)
	}
}

// Özelliğin amacı: sahibi önemli mount'ları seçer; gerisi asla alert üretmez.
func TestOnlySelectedMountsRaiseDiskAlerts(t *testing.T) {
	e := newEnv(t)
	e.diskEnv(t)
	e.selectMounts(t, []string{"/", "/data"})

	e.feedDisks(map[string]float64{"/": 90, "/data": 96, "/var/lib/docker": 99, "/mnt/backup": 100, "/boot": 91})
	got := e.diskAlerts(t)
	if len(got) != 2 || got["/"].Subject != "/" || got["/data"].Level != model.AlertLevelCritical {
		t.Fatalf("alerts = %+v, want only / and /data (the other three mounts are over the threshold but not selected)", got)
	}
	if n := len(e.messages()); n != 2 {
		t.Fatalf("%d emails, want 2: unselected mounts must not notify anyone", n)
	}
}

func TestDeselectingAMountResolvesItsOpenAlert(t *testing.T) {
	e := newEnv(t)
	e.diskEnv(t)
	e.feedDisks(map[string]float64{"/": 90, "/data": 96, "/mnt/scratch": 99}) // hiçbir şey seçili değil: üçü de alert üretir
	if len(e.diskAlerts(t)) != 3 {
		t.Fatal("precondition: three open alerts")
	}

	e.selectMounts(t, []string{"/data"}) // "Yalnızca /data'yı önemsiyorum"
	e.feedDisks(map[string]float64{"/": 90, "/data": 96, "/mnt/scratch": 99})
	got := e.diskAlerts(t)
	if len(got) != 1 || got["/data"].Subject != "/data" {
		t.Fatalf("open alerts after narrowing the selection = %+v, want only /data", got)
	}
	var resolved int
	e.pool.QueryRow(e.ctx, `SELECT count(*) FROM alerts WHERE alert_type = 'disk' AND status = 'resolved'`).Scan(&resolved)
	if resolved != 2 {
		t.Fatalf("%d resolved disk alerts, want 2 (they must not linger open with nothing left to close them)", resolved)
	}
}

func TestEmptySelectionSwitchesDiskAlertsOff(t *testing.T) {
	e := newEnv(t)
	e.diskEnv(t)
	e.feedDisks(map[string]float64{"/": 99})
	if len(e.diskAlerts(t)) != 1 {
		t.Fatal("precondition")
	}
	e.selectMounts(t, []string{}) // [] NULL'dan farklıdır: hiçbiri, tümü değil
	e.feedDisks(map[string]float64{"/": 99, "/data": 99})
	if got := e.diskAlerts(t); len(got) != 0 {
		t.Fatalf("alerts with an empty selection: %+v", got)
	}
}

func TestSelectedMountThatIsNotReportedDoesNothing(t *testing.T) {
	e := newEnv(t)
	e.diskEnv(t)
	e.selectMounts(t, []string{"/data"})
	e.feedDisks(map[string]float64{"/": 99}) // agent yalnızca / raporluyor
	if got := e.diskAlerts(t); len(got) != 0 || len(e.messages()) != 0 {
		t.Fatalf("alerts %+v / emails %d for a mount that was never reported", got, len(e.messages()))
	}
}

func TestVanishedMountResolvesButAnEmptyDiskReportDoesNot(t *testing.T) {
	e := newEnv(t)
	e.diskEnv(t)
	e.feedDisks(map[string]float64{"/": 96, "/data": 96})
	first := e.diskAlerts(t)["/"]
	sent := len(e.messages())

	// Bir toplama hatası hiç disk göndermez: hiçbir şeyi çözmemeli (ve sonra yeniden açmamalı).
	e.engine.EvaluateMetrics(e.ctx, e.host, e.org, 1, 1, nil)
	e.engine.EvaluateMetrics(e.ctx, e.host, e.org, 1, 1, []model.DiskUsage{})
	e.feedDisks(map[string]float64{"/": 96, "/data": 96})
	if got := e.diskAlerts(t); len(got) != 2 || got["/"].ID != first.ID {
		t.Fatalf("an empty report disturbed the alerts: %+v", got)
	}
	if n := len(e.messages()); n != sent {
		t.Fatalf("%d emails after the hiccup, want no new one (%d)", n, sent)
	}

	// Gerçek bir değişiklik — /data boş olmayan bir rapordan kayboldu — alert'ini çözer.
	e.feedDisks(map[string]float64{"/": 96})
	if got := e.diskAlerts(t); len(got) != 1 || got["/data"].Subject == "/data" {
		t.Fatalf("alerts after /data disappeared: %+v", got)
	}
}

func TestConcurrentDiskEvaluationsOpenOneAlertPerMount(t *testing.T) {
	e := newEnv(t)
	e.diskEnv(t)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			e.feedDisks(map[string]float64{"/": 96, "/data": 97})
		}()
	}
	close(start)
	wg.Wait()
	if got := e.diskAlerts(t); len(got) != 2 {
		t.Fatalf("%d open alerts after 20 concurrent reports of 2 mounts, want 2", len(got))
	}
	if n := len(e.messages()); n != 2 {
		t.Fatalf("%d emails, want 2", n)
	}
}

// "/" 70/90'a kadar dolabilir ama "/storage" 90/95'e kadar sorun değildir: her mount kendi
// eşiğine, diğerleri varsayılana göre değerlendirilir.
func TestEachMountIsJudgedAgainstItsOwnThreshold(t *testing.T) {
	e := newEnv(t)
	e.diskEnv(t) // varsayılan 85/95
	testdb.MountThreshold(t, e.pool, e.host, "/", 70, 90)
	testdb.MountThreshold(t, e.pool, e.host, "/storage", 90, 95)

	// %75, "/"nin kendi 70'inin üstünde; %88 ise varsayılan 85'in üstünde olsa da /storage'ın kendi 90'ının altında.
	e.feedDisks(map[string]float64{"/": 75, "/storage": 88, "/data": 80})
	got := e.diskAlerts(t)
	if len(got) != 1 || got["/"].Level != model.AlertLevelWarning {
		t.Fatalf("alerts = %+v, want only a warning for / (/storage is under its own 90, /data under the default 85)", got)
	}

	e.feedDisks(map[string]float64{"/": 75, "/storage": 92, "/data": 90})
	got = e.diskAlerts(t)
	if len(got) != 3 || got["/storage"].Level != model.AlertLevelWarning || got["/data"].Level != model.AlertLevelWarning {
		t.Fatalf("alerts = %+v, want warnings for /, /storage (92 >= its 90) and /data (90 >= default 85)", got)
	}

	e.feedDisks(map[string]float64{"/": 91, "/storage": 94, "/data": 90})
	got = e.diskAlerts(t)
	if got["/"].Level != model.AlertLevelCritical {
		t.Errorf("/ at 91%% is over its critical 90: %+v", got["/"])
	}
	if got["/storage"].Level != model.AlertLevelWarning {
		t.Errorf("/storage at 94%% is over its warning 90 but under its critical 95: %+v", got["/storage"])
	}
	e.feedDisks(map[string]float64{"/": 91, "/storage": 96, "/data": 90})
	if got := e.diskAlerts(t); got["/storage"].Level != model.AlertLevelCritical {
		t.Errorf("/storage at 96%% is over its critical 95: %+v", got["/storage"])
	}
}

func TestMountThresholdWorksWithoutAnyDefault(t *testing.T) {
	e := newEnv(t) // hiç disk varsayılanı yok
	testdb.MountThreshold(t, e.pool, e.host, "/storage", 90, 95)

	e.feedDisks(map[string]float64{"/": 99, "/storage": 96})
	got := e.diskAlerts(t)
	if len(got) != 1 || got["/storage"].Level != model.AlertLevelCritical {
		t.Fatalf("alerts = %+v, want only /storage: / has no threshold anywhere, so it is never judged", got)
	}
}

func TestHostDiskThresholdBeatsTheDefaultAndAMountBeatsBoth(t *testing.T) {
	e := newEnv(t)
	e.diskEnv(t)                                              // varsayılan 85/95
	testdb.Threshold(t, e.pool, nil, &e.host, "disk", 60, 80) // bu host'ın disk eşiği
	testdb.MountThreshold(t, e.pool, e.host, "/storage", 90, 95)

	e.feedDisks(map[string]float64{"/": 65, "/data": 70, "/storage": 65})
	got := e.diskAlerts(t)
	if len(got) != 2 || got["/"].Level != model.AlertLevelWarning || got["/data"].Level != model.AlertLevelWarning {
		t.Fatalf("alerts = %+v, want warnings for / and /data (host threshold 60) and none for /storage (own 90)", got)
	}
}

func TestRemovingAMountThresholdChangesWhatItIsJudgedBy(t *testing.T) {
	e := newEnv(t)
	e.diskEnv(t) // varsayılan 85/95
	testdb.MountThreshold(t, e.pool, e.host, "/storage", 50, 60)

	e.feedDisks(map[string]float64{"/storage": 55})
	if got := e.diskAlerts(t); len(got) != 1 {
		t.Fatalf("precondition: /storage over its own 50: %+v", got)
	}
	// Kendi eşiği olmadan /storage varsayılan 85'i izler: %55 sorun değil, bu yüzden alert kapanır.
	if _, err := e.pool.Exec(e.ctx, `DELETE FROM host_custom_thresholds WHERE subject = '/storage'`); err != nil {
		t.Fatal(err)
	}
	e.feedDisks(map[string]float64{"/storage": 55})
	if got := e.diskAlerts(t); len(got) != 0 {
		t.Fatalf("alerts = %+v, want none after the mount fell back to the default", got)
	}
}

func TestMountLosingItsOnlyThresholdClosesItsAlert(t *testing.T) {
	e := newEnv(t) // varsayılan yok
	testdb.MountThreshold(t, e.pool, e.host, "/storage", 50, 60)
	e.feedDisks(map[string]float64{"/": 40, "/storage": 55})
	if len(e.diskAlerts(t)) != 1 {
		t.Fatal("precondition: an alert for /storage")
	}
	testdb.MountThreshold(t, e.pool, e.host, "/other", 10, 20) // host "yapılandırılmış" kalsın ki değerlendirme çalışmaya devam etsin
	if _, err := e.pool.Exec(e.ctx, `DELETE FROM host_custom_thresholds WHERE subject = '/storage'`); err != nil {
		t.Fatal(err)
	}
	e.feedDisks(map[string]float64{"/": 40, "/storage": 55})
	if got := e.diskAlerts(t); len(got) != 0 {
		t.Fatalf("alerts = %+v, want the /storage alert closed: nothing judges it any more", got)
	}
}

// Mount başına satırlar düz eşik arayan metriklere asla sızmamalı.
func TestMountThresholdsDoNotAffectOtherLookups(t *testing.T) {
	e := newEnv(t)
	testdb.MountThreshold(t, e.pool, e.host, "/storage", 1, 2)
	_, found, err := store.NewThresholds(e.pool).Resolve(e.ctx, e.host, e.org, model.MetricTypeDisk)
	if err != nil || found {
		t.Fatalf("Resolve(disk) = found %v (err %v): a per-mount row must not act as the host's disk threshold", found, err)
	}
}

// ---- disk_missing: beklenen bir mount raporlarda görünmez olur ----------------------

// report, bir raporu ingest'in yaptığı gibi kaydeder (alert engine kayıtlı geçmişi okur) ve
// değerlendirir.
func (e *env) report(t *testing.T, pcts map[string]float64) {
	t.Helper()
	disks := mounts(pcts)
	if err := store.NewMetrics(e.pool).Insert(e.ctx, e.host, 1, 1, disks); err != nil {
		t.Fatal(err)
	}
	e.engine.EvaluateMetrics(e.ctx, e.host, e.org, 1, 1, disks)
}

func (e *env) missingAlerts(t *testing.T) map[string]model.Alert {
	t.Helper()
	open, err := e.alerts.ListActive(e.ctx, e.host, model.AlertTypeDiskMissing)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]model.Alert{}
	for _, a := range open {
		out[a.Subject] = a
	}
	return out
}

func TestSelectedMountThatStopsBeingReportedRaisesAMissingDiskAlertAfterThreeReports(t *testing.T) {
	e := newEnv(t)
	e.selectMounts(t, []string{"/", "/data"})
	e.report(t, map[string]float64{"/": 10, "/data": 10})

	e.report(t, map[string]float64{"/": 10})
	e.report(t, map[string]float64{"/": 10})
	if got := e.missingAlerts(t); len(got) != 0 {
		t.Fatalf("after 2 reports without /data: %+v, want none yet (one hiccup must not alert)", got)
	}
	e.report(t, map[string]float64{"/": 10})
	got := e.missingAlerts(t)
	if len(got) != 1 || got["/data"].Level != model.AlertLevelCritical {
		t.Fatalf("after 3 reports without /data: %+v, want one critical alert for /data", got)
	}

	// E-posta hangi mount'ın kayıp olduğunu söyler; sonraki kayıp raporlar hiçbir şeyi değiştirmez.
	msgs := e.messages()
	if len(msgs) != 1 || !strings.Contains(msgs[0].Text(), "disk kayboldu") || !strings.Contains(msgs[0].Text(), "Mount: /data") || !strings.Contains(msgs[0].Text(), "son 3 raporda görünmedi") {
		t.Fatalf("emails = %+v, want one that names /data and says it is missing", msgs)
	}
	e.report(t, map[string]float64{"/": 10})
	e.report(t, map[string]float64{"/": 10})
	if got := e.missingAlerts(t); len(got) != 1 {
		t.Fatalf("a continuing outage must stay one alert: %+v", got)
	}
	if n := len(e.messages()); n != 1 {
		t.Fatalf("%d emails, want 1 for the whole outage", n)
	}
}

func TestMissingDiskAlertResolvesTheMomentTheMountIsBack(t *testing.T) {
	e := newEnv(t)
	e.selectMounts(t, []string{"/data"})
	for i := 0; i < 3; i++ {
		e.report(t, map[string]float64{"/": 10})
	}
	if len(e.missingAlerts(t)) != 1 {
		t.Fatal("precondition: /data is reported missing")
	}
	e.report(t, map[string]float64{"/": 10, "/data": 10})
	if got := e.missingAlerts(t); len(got) != 0 {
		t.Fatalf("alerts = %+v, want it resolved as soon as /data is reported again", got)
	}
	var resolved int
	e.pool.QueryRow(e.ctx, `SELECT count(*) FROM alerts WHERE alert_type = 'disk_missing' AND status = 'resolved'`).Scan(&resolved)
	if resolved != 1 {
		t.Fatalf("%d resolved rows, want 1", resolved)
	}
	// Yeniden kaybolmak, üç rapor daha sonra kendi alert'i ve bildirimiyle yeni bir olaydır.
	for i := 0; i < 3; i++ {
		e.report(t, map[string]float64{"/": 10})
	}
	// Kümülatif: kayıp + çözüldü + yeniden kayıp.
	if len(e.missingAlerts(t)) != 1 || len(e.messages()) != 3 {
		t.Fatal("a second disappearance must raise a new alert and email")
	}
}

func TestMissingDiskNeedsThreeConsecutiveReportsAndEnoughHistory(t *testing.T) {
	e := newEnv(t)
	e.selectMounts(t, []string{"/data"})

	// Yalnızca iki raporu olan yepyeni bir host hiçbir şeyi kanıtlamaz.
	e.report(t, map[string]float64{"/": 10})
	e.report(t, map[string]float64{"/": 10})
	if len(e.missingAlerts(t)) != 0 {
		t.Fatal("two reports are not enough history")
	}
	// Bir aksama: yok, yok, var, yok, yok — hiçbir zaman üst üste üç değil.
	e.report(t, map[string]float64{"/": 10, "/data": 10})
	e.report(t, map[string]float64{"/": 10})
	e.report(t, map[string]float64{"/": 10})
	if len(e.missingAlerts(t)) != 0 {
		t.Fatalf("a mount that came back in between must reset the count: %+v", e.missingAlerts(t))
	}
	e.report(t, map[string]float64{"/": 10})
	if len(e.missingAlerts(t)) != 1 {
		t.Fatal("three in a row now")
	}
}

func TestEmptyReportsNeitherCountNorResetTheMissingDiskCount(t *testing.T) {
	e := newEnv(t)
	e.selectMounts(t, []string{"/data"})
	e.report(t, map[string]float64{"/": 10})
	e.report(t, map[string]float64{}) // agent'ın disk toplaması başarısız oldu: /data hakkında hiçbir şey söylemez
	e.report(t, map[string]float64{"/": 10})
	e.report(t, map[string]float64{})
	if len(e.missingAlerts(t)) != 0 {
		t.Fatal("two real reports plus empty ones: not enough")
	}
	e.report(t, map[string]float64{"/": 10})
	if len(e.missingAlerts(t)) != 1 {
		t.Fatal("three real reports without /data, empty ones in between ignored: want the alert")
	}
	// Boş bir rapor onu asla çözmez; yoksa sonraki raporda yeniden açılır (ve yeniden bildirilirdi).
	e.report(t, map[string]float64{})
	if len(e.missingAlerts(t)) != 1 {
		t.Fatal("an empty report resolved the alert")
	}
}

func TestOnlyExpectedMountsCanGoMissing(t *testing.T) {
	e := newEnv(t) // hiçbir şey seçili değil, mount eşiği yok: hiçbir şey beklenmez
	for i := 0; i < 5; i++ {
		e.report(t, map[string]float64{"/": 10})
	}
	e.report(t, map[string]float64{"/": 10, "/data": 10})
	for i := 0; i < 5; i++ {
		e.report(t, map[string]float64{"/": 10})
	}
	if got := e.missingAlerts(t); len(got) != 0 {
		t.Fatalf("with \"every reported mount\" and no thresholds nothing is expected, got %+v", got)
	}

	// Kendi eşiği olan bir mount, "raporlanan her mount" modunda bile beklenir...
	testdb.MountThreshold(t, e.pool, e.host, "/storage", 90, 95)
	for i := 0; i < 3; i++ {
		e.report(t, map[string]float64{"/": 10})
	}
	got := e.missingAlerts(t)
	if len(got) != 1 || got["/storage"].Subject != "/storage" {
		t.Fatalf("alerts = %+v, want /storage (it has its own threshold)", got)
	}
	// ...ve eşik gidince beklenmez olur, bu da alert'i kapatır.
	if _, err := e.pool.Exec(e.ctx, `DELETE FROM host_custom_thresholds WHERE subject = '/storage'`); err != nil {
		t.Fatal(err)
	}
	e.report(t, map[string]float64{"/": 10})
	if got := e.missingAlerts(t); len(got) != 0 {
		t.Fatalf("alerts = %+v, want it closed: /storage is no longer expected", got)
	}
}

func TestDeselectingAMissingMountClosesItsAlert(t *testing.T) {
	e := newEnv(t)
	e.selectMounts(t, []string{"/", "/gone", "/also-gone"})
	for i := 0; i < 3; i++ {
		e.report(t, map[string]float64{"/": 10})
	}
	if len(e.missingAlerts(t)) != 2 {
		t.Fatalf("precondition: two missing mounts, got %+v", e.missingAlerts(t))
	}
	e.selectMounts(t, []string{"/", "/gone"}) // "Artık /also-gone umurumda değil"
	e.report(t, map[string]float64{"/": 10})
	got := e.missingAlerts(t)
	if len(got) != 1 || got["/gone"].Subject != "/gone" {
		t.Fatalf("alerts = %+v, want only /gone", got)
	}
	e.selectMounts(t, []string{}) // disk alert'leri kapalı
	e.report(t, map[string]float64{"/": 10})
	if len(e.missingAlerts(t)) != 0 {
		t.Fatal("with no mounts selected nothing may stay missing")
	}
}

func TestMissingDiskAlertAndThresholdAlertAreIndependent(t *testing.T) {
	e := newEnv(t)
	e.diskEnv(t)
	e.selectMounts(t, []string{"/", "/data"})
	for i := 0; i < 3; i++ {
		e.report(t, map[string]float64{"/": 96})
	}
	if got := e.diskAlerts(t); len(got) != 1 || got["/"].Level != model.AlertLevelCritical {
		t.Fatalf("threshold alerts = %+v", got)
	}
	if got := e.missingAlerts(t); len(got) != 1 || got["/data"].Subject != "/data" {
		t.Fatalf("missing alerts = %+v", got)
	}
	// /data geri gelir: yalnızca kendi alert'i kapanır; / eşiği üzerinden alert vermeye devam eder.
	e.report(t, map[string]float64{"/": 96, "/data": 10})
	if len(e.missingAlerts(t)) != 0 || len(e.diskAlerts(t)) != 1 {
		t.Fatalf("missing=%+v threshold=%+v", e.missingAlerts(t), e.diskAlerts(t))
	}
}

// Bir mount, onsuz birkaç rapor zaten kaydedildikten sonra "beklenen" olur. Hemen sonraki
// değerlendirme gerçek bir rapor beklemeli: boş rapor (başarısız toplama) ne yönde de kanıt değildir.
func TestNewlyExpectedMountIsNotDeclaredMissingByAnEmptyReport(t *testing.T) {
	e := newEnv(t)
	for i := 0; i < 3; i++ {
		e.report(t, map[string]float64{"/": 10})
	}
	e.selectMounts(t, []string{"/", "/data"}) // /data bu raporlarda hiç yoktu
	e.report(t, map[string]float64{})
	if got := e.missingAlerts(t); len(got) != 0 {
		t.Fatalf("an empty report declared /data missing: %+v", got)
	}
	e.report(t, map[string]float64{"/": 10})
	if got := e.missingAlerts(t); len(got) != 1 {
		t.Fatalf("the next real report should: %+v", got)
	}
}

// ---- docker_restart: container başına eşik ------------------------------------------------

// "web" 3/10'da uyarır ama "batch" 50/100'e kadar rahatça yeniden başlayabilir.
func TestEachContainerIsJudgedAgainstItsOwnRestartThreshold(t *testing.T) {
	e := newEnv(t)
	testdb.Threshold(t, e.pool, nil, nil, "docker_restart", 3, 10) // varsayılan
	testdb.SubjectThreshold(t, e.pool, e.host, "docker_restart", "batch", 50, 100)
	testdb.SubjectThreshold(t, e.pool, e.host, "docker_restart", "web", 1, 2)

	// 5 restart: web'in kendi 2'sinin üstünde (critical), batch'in kendi 50'sinin altında (varsayılan 3
	// olsaydı uyarırdı), db varsayılan 3'ün üstünde (warning).
	e.feedDocker(map[string]int{"web": 5, "batch": 5, "db": 5})
	got := e.dockerAlerts(t)
	if len(got) != 2 || got["web"].Level != model.AlertLevelCritical || got["db"].Level != model.AlertLevelWarning {
		t.Fatalf("alerts = %+v, want web critical and db warning, none for batch", got)
	}
	e.feedDocker(map[string]int{"web": 5, "batch": 60, "db": 5})
	if got := e.dockerAlerts(t); got["batch"].Level != model.AlertLevelWarning {
		t.Fatalf("batch at 60 is over its own warning 50: %+v", got)
	}
}

func TestContainerThresholdWorksWithoutAnyDefault(t *testing.T) {
	e := newEnv(t)
	testdb.SubjectThreshold(t, e.pool, e.host, "docker_restart", "web", 1, 2)
	e.feedDocker(map[string]int{"web": 3, "db": 999})
	got := e.dockerAlerts(t)
	if len(got) != 1 || got["web"].Level != model.AlertLevelCritical {
		t.Fatalf("alerts = %+v, want only web: db has no threshold anywhere", got)
	}
}

func TestContainerLosingItsOnlyThresholdClosesItsAlert(t *testing.T) {
	e := newEnv(t)
	testdb.SubjectThreshold(t, e.pool, e.host, "docker_restart", "web", 1, 2)
	testdb.SubjectThreshold(t, e.pool, e.host, "docker_restart", "other", 1, 2) // istemci "yapılandırılmış" kalsın
	e.feedDocker(map[string]int{"web": 3})
	if len(e.dockerAlerts(t)) != 1 {
		t.Fatal("precondition: an alert for web")
	}
	if _, err := e.pool.Exec(e.ctx, `DELETE FROM host_custom_thresholds WHERE subject = 'web'`); err != nil {
		t.Fatal(err)
	}
	e.feedDocker(map[string]int{"web": 3})
	if got := e.dockerAlerts(t); len(got) != 0 {
		t.Fatalf("alerts = %+v, want the web alert closed: nothing judges it any more", got)
	}
}

// Container eşikleri disk eşiklerinden ve birbirinden ayrıdır.
func TestContainerAndMountSubjectsDoNotInterfere(t *testing.T) {
	e := newEnv(t)
	e.diskEnv(t)
	testdb.Threshold(t, e.pool, nil, nil, "docker_restart", 3, 10)
	testdb.SubjectThreshold(t, e.pool, e.host, "docker_restart", "/", 100, 200) // "/" adlı bir container
	testdb.MountThreshold(t, e.pool, e.host, "/", 99, 100)

	e.feedDocker(map[string]int{"/": 5})
	if got := e.dockerAlerts(t); len(got) != 0 {
		t.Fatalf("a mount threshold leaked into docker_restart, or the container's own 100 was ignored: %+v", got)
	}
	e.feedDisks(map[string]float64{"/": 96})
	if got := e.diskAlerts(t); len(got) != 0 {
		t.Fatalf("a container threshold leaked into disk, or the mount's own 99 was ignored: %+v", got)
	}
}
