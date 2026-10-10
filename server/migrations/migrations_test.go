package migrations_test

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/google/uuid"

	"healthbeat-server/internal/migrate"
	"healthbeat-server/internal/testdb"
	"healthbeat-server/migrations"
)

// upTo, gömülü migration'lardan yalnızca version'a kadar olanları içeren bir dosya sistemi döndürür.
func upTo(t *testing.T, version string) fs.FS {
	t.Helper()
	out := fstest.MapFS{}
	entries, err := fs.ReadDir(migrations.FS, ".")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.Name() <= version+"_~" { // "000001_…" ≤ "000001_~"
			b, err := fs.ReadFile(migrations.FS, e.Name())
			if err != nil {
				t.Fatal(err)
			}
			out[e.Name()] = &fstest.MapFile{Data: b}
		}
	}
	return out
}

// 000002: onaylanmış alert'ler aktif sayılır. Eski davranış aynı olay için onaylanmış + yeniden açılmış kopyalar
// bırakmış olabilir; migration en yenisi dışındakileri çözmeli ki "tek aktif alert" index'i kurulabilsin.
func TestAlertAcknowledgedActiveMigration(t *testing.T) {
	ctx := context.Background()
	pool := testdb.NewEmpty(t)

	r, err := migrate.New(pool, upTo(t, "000001"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.Up(ctx); err != nil {
		t.Fatalf("migrate to 000001: %v", err)
	}

	host := testdb.PushHost(t, pool, testdb.Org(t, pool, "o"), "h", "hash")
	insert := func(alertType, subject, status, age string) (id string) {
		t.Helper()
		var subj any
		if subject != "" {
			subj = subject
		}
		if err := pool.QueryRow(ctx,
			`INSERT INTO alerts (host_id, alert_type, subject, level, status, created_at, acknowledged_at, resolved_at)
			 VALUES ($1, $2, $3, 'critical', $4, now() - $5::interval,
			         CASE WHEN $4 = 'acknowledged' THEN now() ELSE NULL END,
			         CASE WHEN $4 = 'resolved' THEN now() ELSE NULL END)
			 RETURNING id::text`, host, alertType, subj, status, age).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	oldAck := insert("cpu", "", "acknowledged", "10 minutes") // eski davranışın bıraktığı kopya
	reopened := insert("cpu", "", "open", "5 minutes")        // aynı olay için yeniden açılan
	loneAck := insert("disk", "/", "acknowledged", "3 minutes")
	insert("docker_restart", "web", "resolved", "1 hour")
	webOpen := insert("docker_restart", "web", "open", "1 minute")

	r, err = migrate.New(pool, migrations.FS)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.Up(ctx); err != nil {
		t.Fatalf("migrate to latest: %v", err)
	}

	status := func(id string) string {
		t.Helper()
		var s string
		if err := pool.QueryRow(ctx, `SELECT status || CASE WHEN resolved_at IS NULL THEN '' ELSE '+resolved_at' END FROM alerts WHERE id = $1`, id).Scan(&s); err != nil {
			t.Fatal(err)
		}
		return s
	}
	for id, want := range map[string]string{
		oldAck:   "resolved+resolved_at", // kopyanın eskisi çözüldü
		reopened: "open",                 // en yenisi kaldı
		loneAck:  "acknowledged",         // kopyası olmayan onaylanmış alert olduğu gibi
		webOpen:  "open",
	} {
		if got := status(id); got != want {
			t.Errorf("alert %s: %s, want %s", id, got, want)
		}
	}

	var indexes []string
	rows, err := pool.Query(ctx, `SELECT indexname FROM pg_indexes WHERE tablename = 'alerts' AND indexname LIKE 'alerts_one_%' AND schemaname = current_schema()`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var n string
		rows.Scan(&n)
		indexes = append(indexes, n)
	}
	rows.Close()
	if len(indexes) != 1 || indexes[0] != "alerts_one_active_uidx" {
		t.Fatalf("alert uniqueness indexes = %v, want only alerts_one_active_uidx", indexes)
	}

	// Onaylanmış alert aktif sayılır: aynı disk/"/" için ikinci bir aktif alert veritabanınca reddedilir.
	if _, err := pool.Exec(ctx, `INSERT INTO alerts (host_id, alert_type, subject, level) VALUES ($1, 'disk', '/', 'warning')`, host); err == nil {
		t.Fatal("a second active alert next to an acknowledged one was accepted")
	}
}

// docs/DISTRIBUTION.md §8.3'teki geri dönüş yolu: eski binary (yalnızca 000001'i bilen) 000002 uygulanmış veritabanıyla
// açılmaz; 000002'nin .down.sql'i uygulanıp geçmiş satırı silinince açılır ve yeni sürüme yeniden geçilebilir.
func TestAlertAcknowledgedActiveMigrationRollsBackWithDownFile(t *testing.T) {
	ctx := context.Background()
	pool := testdb.NewEmpty(t)
	runner := func(fsys fs.FS) *migrate.Runner {
		t.Helper()
		r, err := migrate.New(pool, fsys)
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	latest, old := runner(upTo(t, "000002")), runner(upTo(t, "000001"))

	if _, err := latest.Up(ctx); err != nil {
		t.Fatal(err)
	}
	if err := old.RequireUpToDate(ctx); !errors.Is(err, migrate.ErrDatabaseNewer) {
		t.Fatalf("old binary on the new schema: err=%v, want ErrDatabaseNewer (it must refuse to start)", err)
	}

	down, err := fs.ReadFile(migrations.FS, "000002_alert_acknowledged_active.down.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, string(down)); err != nil {
		t.Fatalf("down migration: %v", err)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM healthbeat_migrations WHERE version = 2`); err != nil {
		t.Fatal(err)
	}
	if err := old.RequireUpToDate(ctx); err != nil {
		t.Fatalf("old binary after the down migration: %v, want it to start", err)
	}
	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM pg_indexes WHERE indexname = 'alerts_one_open_uidx' AND schemaname = current_schema()`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("alerts_one_open_uidx after rollback: count=%d err=%v, want it back", n, err)
	}

	if applied, err := latest.Up(ctx); err != nil || len(applied) != 1 {
		t.Fatalf("upgrading again: applied=%d err=%v, want 000002 re-applied", len(applied), err)
	}
}

// 000003: bildirim kuyruğu tablosu. Eski binary yeni şemayla açılmaz; .down.sql tabloyu kaldırır ve eski binary yeniden
// açılır; tekrar yükseltme tabloyu geri getirir.
func TestNotificationOutboxMigrationRollsBackWithDownFile(t *testing.T) {
	ctx := context.Background()
	pool := testdb.NewEmpty(t)
	runner := func(fsys fs.FS) *migrate.Runner {
		t.Helper()
		r, err := migrate.New(pool, fsys)
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	latest, old := runner(upTo(t, "000003")), runner(upTo(t, "000002"))

	if _, err := latest.Up(ctx); err != nil {
		t.Fatal(err)
	}
	tableExists := func() bool {
		t.Helper()
		var n int
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM pg_tables WHERE tablename = 'notification_outbox' AND schemaname = current_schema()`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n == 1
	}
	if !tableExists() {
		t.Fatal("notification_outbox missing after 000003")
	}
	if err := old.RequireUpToDate(ctx); !errors.Is(err, migrate.ErrDatabaseNewer) {
		t.Fatalf("old binary on the new schema: err=%v, want ErrDatabaseNewer (it must refuse to start)", err)
	}

	// Bekleyen satırın tam olarak bir gövdesi olmalı; bitmiş satır şifreli gövde tutamaz.
	for _, bad := range []string{
		`INSERT INTO notification_outbox (id, kind, channel, recipients, subject) VALUES (gen_random_uuid(), 'alert', 'email', '{a@x}', 's')`,
		`INSERT INTO notification_outbox (id, kind, channel, recipients, subject, body, body_sealed) VALUES (gen_random_uuid(), 'alert', 'email', '{a@x}', 's', 'b', 'enc')`,
		`INSERT INTO notification_outbox (id, kind, channel, recipients, subject, body_sealed, sent_at) VALUES (gen_random_uuid(), 'password_reset', 'email', '{a@x}', 's', 'enc', now())`,
		// Alert bildiriminin olayı ve seviyesi olmalı; hesap e-postasının olmamalı.
		`INSERT INTO notification_outbox (id, kind, channel, recipients, subject, body) VALUES (gen_random_uuid(), 'alert', 'email', '{a@x}', 's', 'b')`,
		`INSERT INTO notification_outbox (id, kind, channel, recipients, subject, body, alert_event, alert_level) VALUES (gen_random_uuid(), 'password_changed', 'email', '{a@x}', 's', 'b', 'opened', 'warning')`,
	} {
		if _, err := pool.Exec(ctx, bad); err == nil {
			t.Errorf("accepted an invalid row: %s", bad)
		}
	}

	if _, err := pool.Exec(ctx, `INSERT INTO notification_outbox (id, kind, channel, recipients, subject, body, alert_event, alert_level)
		VALUES (gen_random_uuid(), 'alert', 'email', '{a@x}', 's', 'b', 'resolved', 'critical')`); err != nil {
		t.Fatalf("a valid alert notification row was rejected: %v", err)
	}

	down, err := fs.ReadFile(migrations.FS, "000003_notification_outbox.down.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, string(down)); err != nil {
		t.Fatalf("down migration: %v", err)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM healthbeat_migrations WHERE version = 3`); err != nil {
		t.Fatal(err)
	}
	if err := old.RequireUpToDate(ctx); err != nil {
		t.Fatalf("old binary after the down migration: %v, want it to start", err)
	}
	if tableExists() {
		t.Fatal("notification_outbox still there after the down migration")
	}
	if applied, err := latest.Up(ctx); err != nil || len(applied) != 1 || !tableExists() {
		t.Fatalf("upgrading again: applied=%d err=%v, want 000003 re-applied", len(applied), err)
	}
}

// 000004: çözülmüş alert'lerin saklama temizliği için indeks. Yalnızca indeks eklenir; .down.sql onu kaldırır ve eski
// binary yeniden açılır.
func TestResolvedAlertsIndexMigrationRollsBackWithDownFile(t *testing.T) {
	ctx := context.Background()
	pool := testdb.NewEmpty(t)
	runner := func(fsys fs.FS) *migrate.Runner {
		t.Helper()
		r, err := migrate.New(pool, fsys)
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	latest, old := runner(upTo(t, "000004")), runner(upTo(t, "000003"))
	if _, err := latest.Up(ctx); err != nil {
		t.Fatal(err)
	}
	indexExists := func() bool {
		t.Helper()
		var n int
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM pg_indexes WHERE indexname = 'alerts_resolved_at_idx' AND schemaname = current_schema()`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n == 1
	}
	if !indexExists() {
		t.Fatal("alerts_resolved_at_idx missing after 000004")
	}
	if err := old.RequireUpToDate(ctx); !errors.Is(err, migrate.ErrDatabaseNewer) {
		t.Fatalf("old binary on the new schema: err=%v, want ErrDatabaseNewer", err)
	}

	down, err := fs.ReadFile(migrations.FS, "000004_alerts_resolved_at_idx.down.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, string(down)); err != nil {
		t.Fatalf("down migration: %v", err)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM healthbeat_migrations WHERE version = 4`); err != nil {
		t.Fatal(err)
	}
	if err := old.RequireUpToDate(ctx); err != nil || indexExists() {
		t.Fatalf("after the down migration: old binary err=%v, index still there=%v", err, indexExists())
	}
	if applied, err := latest.Up(ctx); err != nil || len(applied) != 1 || !indexExists() {
		t.Fatalf("upgrading again: applied=%d err=%v, want 000004 re-applied", len(applied), err)
	}
}

// 000005: bekleyen çok alıcılı alert bildirimleri alıcı başına satırlara bölünür (her alıcı ayrı ileti alır);
// gönderilmiş satırlar geçmiş olarak kalır, mevcut e-posta kuralları yeni kanal tablosuna bağlanır.
func TestRuntimeSettingsMigrationSplitsPendingAlertNotifications(t *testing.T) {
	ctx := context.Background()
	pool := testdb.NewEmpty(t)
	r, err := migrate.New(pool, upTo(t, "000004"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.Up(ctx); err != nil {
		t.Fatalf("migrate to 000004: %v", err)
	}

	org := testdb.Org(t, pool, "o")
	user := testdb.User(t, pool, "u@example.com", "org_admin", "Passw0rd!long")
	var routeID string
	if err := pool.QueryRow(ctx, `INSERT INTO notification_routes (organization_id, user_id, channel) VALUES ($1, $2, 'email') RETURNING id::text`,
		org, user).Scan(&routeID); err != nil {
		t.Fatal(err)
	}
	insert := func(recipients []string, sent bool) (id string) {
		t.Helper()
		if err := pool.QueryRow(ctx,
			`INSERT INTO notification_outbox (id, kind, channel, recipients, subject, body, alert_event, alert_level, attempts, sent_at)
			 VALUES (gen_random_uuid(), 'alert', 'email', $1, 'konu', 'gövde', 'opened', 'critical', 2,
			         CASE WHEN $2 THEN now() END)
			 RETURNING id::text`, recipients, sent).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	pending := insert([]string{"a@example.com", "b@example.com"}, false)
	sent := insert([]string{"c@example.com", "d@example.com"}, true)
	single := insert([]string{"e@example.com"}, false)

	r, err = migrate.New(pool, migrations.FS)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.Up(ctx); err != nil {
		t.Fatalf("migrate to latest: %v", err)
	}

	type row struct {
		id, recipients, subject, body string
		attempts                      int
		sent                          bool
	}
	rows, err := pool.Query(ctx, `SELECT id::text, array_to_string(recipients, ','), subject, body, attempts, sent_at IS NOT NULL
		FROM notification_outbox ORDER BY array_to_string(recipients, ',')`)
	if err != nil {
		t.Fatal(err)
	}
	var got []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.id, &r.recipients, &r.subject, &r.body, &r.attempts, &r.sent); err != nil {
			t.Fatal(err)
		}
		got = append(got, r)
	}
	rows.Close()
	if len(got) != 4 {
		t.Fatalf("outbox rows = %+v, want 4 (a, b split; c+d sent; e)", got)
	}
	for i, want := range []string{"a@example.com", "b@example.com", "c@example.com,d@example.com", "e@example.com"} {
		if got[i].recipients != want {
			t.Errorf("row %d recipients = %q, want %q", i, got[i].recipients, want)
		}
	}
	for _, r := range got[:2] {
		if r.id == pending || r.subject != "konu" || r.body != "gövde" || r.attempts != 2 || r.sent {
			t.Errorf("split row %+v: want a new id with the original content and attempts", r)
		}
	}
	if got[2].id != sent || !got[2].sent {
		t.Errorf("sent row changed: %+v", got[2])
	}
	if got[3].id != single {
		t.Errorf("single-recipient row got a new id: %+v", got[3])
	}

	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM notification_routes WHERE id = $1`, routeID).Scan(&n); err != nil || n != 1 {
		t.Fatalf("email route after the migration: count=%d err=%v, want it kept", n, err)
	}
}

// 000005: e-posta dışındaki bir kanala bağlı kural (API bunu hiç kabul etmedi) sessizce silinmez; migration açık bir
// hatayla durur ve veritabanı değişmez.
func TestRuntimeSettingsMigrationRefusesRoutesOnUnavailableChannels(t *testing.T) {
	ctx := context.Background()
	pool := testdb.NewEmpty(t)
	r, err := migrate.New(pool, upTo(t, "000004"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.Up(ctx); err != nil {
		t.Fatalf("migrate to 000004: %v", err)
	}
	org := testdb.Org(t, pool, "o")
	user := testdb.User(t, pool, "u@example.com", "org_admin", "Passw0rd!long")
	for _, ch := range []string{"sms", "slack", "sms"} {
		host := testdb.PushHost(t, pool, org, "h-"+ch+uuid.NewString(), "hash")
		if _, err := pool.Exec(ctx, `INSERT INTO notification_routes (host_id, user_id, channel) VALUES ($1, $2, $3)`, host, user, ch); err != nil {
			t.Fatal(err)
		}
	}

	r, err = migrate.New(pool, migrations.FS)
	if err != nil {
		t.Fatal(err)
	}
	_, err = r.Up(ctx)
	if err == nil || !strings.Contains(err.Error(), "3 notification rules use a channel that is not available (slack: 1, sms: 2)") {
		t.Fatalf("err = %v, want the unavailable-channel error with counts", err)
	}

	var tables int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM pg_tables WHERE tablename = 'app_settings' AND schemaname = current_schema()`).Scan(&tables); err != nil {
		t.Fatal(err)
	}
	var version int
	if err := pool.QueryRow(ctx, `SELECT max(version) FROM healthbeat_migrations`).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if tables != 0 || version != 4 {
		t.Fatalf("after the failed migration: app_settings tables=%d, version=%d; want the database unchanged (0, 4)", tables, version)
	}
}

// 000005: ayarların varsayılanları ve sınırları veritabanındadır; kanal, sahip ve izin kısıtları.
func TestRuntimeSettingsSchema(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)

	defaults := `SELECT latest_agent_version, min_supported_agent_version IS NULL, metrics_retention_days, audit_retention_days,
		resolved_alert_retention_days, access_token_ttl_seconds, refresh_token_ttl_seconds, rate_limit_auth_failures_per_minute,
		rate_limit_ingest_per_minute, panel_base_url, log_level, log_error_body_bytes, log_file_max_age_days,
		log_file_max_total_mb FROM app_settings`
	const wantDefaults = "1.0.0 true 30 0 0 900 604800 10 120  info 4096 14 1024"
	readSettings := func() string {
		t.Helper()
		var latest, url, level string
		var minNull bool
		var n [11]int
		if err := pool.QueryRow(ctx, defaults).Scan(&latest, &minNull, &n[0], &n[1], &n[2], &n[3], &n[4], &n[5], &n[6],
			&url, &level, &n[7], &n[8], &n[9]); err != nil {
			t.Fatal(err)
		}
		return fmt.Sprintf("%s %v %d %d %d %d %d %d %d %s %s %d %d %d", latest, minNull, n[0], n[1], n[2], n[3], n[4], n[5], n[6],
			url, level, n[7], n[8], n[9])
	}
	if got := readSettings(); got != wantDefaults {
		t.Fatalf("app_settings defaults = %q, want %q", got, wantDefaults)
	}

	for _, bad := range []string{
		`INSERT INTO app_settings (id) VALUES (2)`,
		`INSERT INTO app_settings DEFAULT VALUES`,
		`UPDATE app_settings SET latest_agent_version = '1.2'`,
		`UPDATE app_settings SET min_supported_agent_version = 'v1.0.0'`,
		`UPDATE app_settings SET metrics_retention_days = -1`,
		`UPDATE app_settings SET access_token_ttl_seconds = 10`,
		`UPDATE app_settings SET access_token_ttl_seconds = 86401`,
		`UPDATE app_settings SET refresh_token_ttl_seconds = 60`,
		`UPDATE app_settings SET access_token_ttl_seconds = 7200, refresh_token_ttl_seconds = 3600`,
		`UPDATE app_settings SET rate_limit_ingest_per_minute = -1`,
		`UPDATE app_settings SET log_level = 'trace'`,
		`UPDATE app_settings SET log_error_body_bytes = 1048577`,
		`UPDATE app_settings SET log_file_max_age_days = 0`,
		`UPDATE app_settings SET log_file_max_total_mb = 0`,
	} {
		if _, err := pool.Exec(ctx, bad); err == nil {
			t.Errorf("accepted: %s", bad)
		}
	}

	// Geçerli değişiklik ve "varsayılana dön".
	if _, err := pool.Exec(ctx, `UPDATE app_settings SET latest_agent_version = '1.1.0-rc.1', min_supported_agent_version = '1.0.0',
		metrics_retention_days = 0, log_level = 'debug', rate_limit_ingest_per_minute = 0`); err != nil {
		t.Fatalf("a valid update was rejected: %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE app_settings SET latest_agent_version = DEFAULT, min_supported_agent_version = DEFAULT,
		metrics_retention_days = DEFAULT, log_level = DEFAULT, rate_limit_ingest_per_minute = DEFAULT`); err != nil {
		t.Fatal(err)
	}
	if got := readSettings(); got != wantDefaults {
		t.Fatalf("after SET … = DEFAULT: %q, want %q", got, wantDefaults)
	}

	// E-posta kanalı "ayar gerekli" durumunda gelir; kurallar yalnızca tanımlı kanallara bağlanabilir.
	var enabled bool
	var provider, config, ownerLevel string
	if err := pool.QueryRow(ctx, `SELECT provider, enabled, config::text, owner_min_level FROM notification_channels WHERE channel = 'email'`).
		Scan(&provider, &enabled, &config, &ownerLevel); err != nil {
		t.Fatal(err)
	}
	if provider != "smtp" || enabled || config != `{"port": 587}` || ownerLevel != "warning" {
		t.Fatalf("email channel = %s enabled=%v config=%s owner_min_level=%s", provider, enabled, config, ownerLevel)
	}
	for _, bad := range []string{
		`UPDATE notification_channels SET owner_min_level = 'debug'`,
		`UPDATE notification_channels SET config = '[]'`,
	} {
		if _, err := pool.Exec(ctx, bad); err == nil {
			t.Errorf("accepted: %s", bad)
		}
	}
	org := testdb.Org(t, pool, "o")
	user := testdb.User(t, pool, "u@example.com", "org_admin", "Passw0rd!long")
	if _, err := pool.Exec(ctx, `INSERT INTO notification_routes (organization_id, user_id, channel) VALUES ($1, $2, 'sms')`, org, user); err == nil {
		t.Error("a rule on a channel that does not exist was accepted")
	}
	if _, err := pool.Exec(ctx, `INSERT INTO notification_routes (organization_id, user_id, channel) VALUES ($1, $2, 'email')`, org, user); err != nil {
		t.Errorf("an email rule was rejected: %v", err)
	}

	// Sistem sahipleri: ulaşılabilir olmalı, e-posta küçük harf ve benzersiz.
	if _, err := pool.Exec(ctx, `INSERT INTO notification_owners (name, email, phone) VALUES ('Ali', 'ali@example.com', '+905550000000'), ('NOC', 'noc@example.com', NULL), ('Veli', NULL, '+905551111111')`); err != nil {
		t.Fatalf("valid owners were rejected: %v", err)
	}
	for _, bad := range []string{
		`INSERT INTO notification_owners (name) VALUES ('Adressiz')`,
		`INSERT INTO notification_owners (name, email) VALUES ('Büyük', 'Ayse@example.com')`,
		`INSERT INTO notification_owners (name, email) VALUES ('Kopya', 'ali@example.com')`,
	} {
		if _, err := pool.Exec(ctx, bad); err == nil {
			t.Errorf("accepted: %s", bad)
		}
	}

	// Yeni izinler yalnızca super_admin'de.
	rows, err := pool.Query(ctx, `SELECT role || ':' || permission_key FROM role_permissions WHERE permission_key LIKE 'settings.%' ORDER BY 1`)
	if err != nil {
		t.Fatal(err)
	}
	var perms []string
	for rows.Next() {
		var p string
		rows.Scan(&p)
		perms = append(perms, p)
	}
	rows.Close()
	if strings.Join(perms, ",") != "super_admin:settings.manage,super_admin:settings.view" {
		t.Fatalf("settings permissions = %v, want only super_admin", perms)
	}
}

// 000005: .down.sql yeni tabloları ve izinleri kaldırır, kanal kısıtını geri koyar; eski binary yeniden açılır ve
// tekrar yükseltilebilir.
func TestRuntimeSettingsMigrationRollsBackWithDownFile(t *testing.T) {
	ctx := context.Background()
	pool := testdb.NewEmpty(t)
	runner := func(fsys fs.FS) *migrate.Runner {
		t.Helper()
		r, err := migrate.New(pool, fsys)
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	latest, old := runner(upTo(t, "000005")), runner(upTo(t, "000004"))
	if _, err := latest.Up(ctx); err != nil {
		t.Fatal(err)
	}
	if err := old.RequireUpToDate(ctx); !errors.Is(err, migrate.ErrDatabaseNewer) {
		t.Fatalf("old binary on the new schema: err=%v, want ErrDatabaseNewer", err)
	}

	down, err := fs.ReadFile(migrations.FS, "000005_runtime_settings_notification_channels.down.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, string(down)); err != nil {
		t.Fatalf("down migration: %v", err)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM healthbeat_migrations WHERE version = 5`); err != nil {
		t.Fatal(err)
	}
	if err := old.RequireUpToDate(ctx); err != nil {
		t.Fatalf("old binary after the down migration: %v, want it to start", err)
	}
	var tables, perms int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM pg_tables WHERE schemaname = current_schema()
		AND tablename IN ('app_settings', 'notification_channels', 'notification_owners')`).Scan(&tables); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM permissions WHERE key LIKE 'settings.%'`).Scan(&perms); err != nil {
		t.Fatal(err)
	}
	if tables != 0 || perms != 0 {
		t.Fatalf("after the down migration: tables=%d settings permissions=%d, want 0", tables, perms)
	}
	org := testdb.Org(t, pool, "o")
	user := testdb.User(t, pool, "u@example.com", "org_admin", "Passw0rd!long")
	if _, err := pool.Exec(ctx, `INSERT INTO notification_routes (organization_id, user_id, channel) VALUES ($1, $2, 'webhook')`, org, user); err == nil {
		t.Fatal("the channel CHECK is not back after the down migration")
	}

	if applied, err := latest.Up(ctx); err != nil || len(applied) != 1 {
		t.Fatalf("upgrading again: applied=%d err=%v, want 000005 re-applied", len(applied), err)
	}
}

// 000006: Sistem Araçları izinleri yalnızca super_admin'e verilir; .down.sql onları kaldırır, eski binary yeniden açılır
// ve tekrar yükseltilebilir.
func TestSystemToolsPermissionsMigrationAndRollback(t *testing.T) {
	ctx := context.Background()
	pool := testdb.NewEmpty(t)
	runner := func(fsys fs.FS) *migrate.Runner {
		t.Helper()
		r, err := migrate.New(pool, fsys)
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	latest, old := runner(upTo(t, "000006")), runner(upTo(t, "000005"))
	if _, err := latest.Up(ctx); err != nil {
		t.Fatal(err)
	}
	grants := func() string {
		t.Helper()
		rows, err := pool.Query(ctx, `SELECT role || ':' || permission_key FROM role_permissions WHERE permission_key LIKE 'system.%' ORDER BY 1`)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		var out []string
		for rows.Next() {
			var p string
			if err := rows.Scan(&p); err != nil {
				t.Fatal(err)
			}
			out = append(out, p)
		}
		return strings.Join(out, ",")
	}
	if got := grants(); got != "super_admin:system.cache.view,super_admin:system.logs.view,super_admin:system.queue.view" {
		t.Fatalf("system permissions = %q, want the three of them on super_admin only", got)
	}
	if err := old.RequireUpToDate(ctx); !errors.Is(err, migrate.ErrDatabaseNewer) {
		t.Fatalf("old binary on the new schema: err=%v, want ErrDatabaseNewer", err)
	}

	down, err := fs.ReadFile(migrations.FS, "000006_system_tools_permissions.down.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, string(down)); err != nil {
		t.Fatalf("down migration: %v", err)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM healthbeat_migrations WHERE version = 6`); err != nil {
		t.Fatal(err)
	}
	if err := old.RequireUpToDate(ctx); err != nil {
		t.Fatalf("old binary after the down migration: %v, want it to start", err)
	}
	var perms int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM permissions WHERE key LIKE 'system.%'`).Scan(&perms); err != nil {
		t.Fatal(err)
	}
	if perms != 0 || grants() != "" {
		t.Fatalf("after the down migration: permissions=%d grants=%q, want none", perms, grants())
	}
	if applied, err := latest.Up(ctx); err != nil || len(applied) != 1 {
		t.Fatalf("upgrading again: applied=%d err=%v, want 000006 re-applied", len(applied), err)
	}
}

// 000007: protokol 4. Yeni alert ve eşik türleri CHECK'lerce kabul edilir, yeni sütunların kısıtları çalışır; .down.sql
// yeni türdeki satırları siler, şemayı geri alır ve eski binary yeniden açılır; tekrar yükseltilebilir.
func TestProtocol4MigrationAndRollback(t *testing.T) {
	ctx := context.Background()
	pool := testdb.NewEmpty(t)
	runner := func(fsys fs.FS) *migrate.Runner {
		t.Helper()
		r, err := migrate.New(pool, fsys)
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	latest, old := runner(upTo(t, "000007")), runner(upTo(t, "000006"))
	if _, err := latest.Up(ctx); err != nil {
		t.Fatal(err)
	}

	org := testdb.Org(t, pool, "o")
	host := testdb.PushHost(t, pool, org, "h", "hash")
	for _, ok := range []string{
		`INSERT INTO alerts (host_id, alert_type, subject, level) VALUES ($1, 'service_failed', 'nginx.service', 'critical')`,
		`INSERT INTO alerts (host_id, alert_type, level) VALUES ($1, 'security_updates', 'info')`,
		`INSERT INTO host_custom_thresholds (host_id, metric_type, subject, warning_level, critical_level, duration_seconds) VALUES ($1, 'disk_latency', 'nvme0n1', 30, 50, 600)`,
		`INSERT INTO host_custom_thresholds (host_id, metric_type, warning_level, critical_level) VALUES ($1, 'time_offset', 100, 1000)`,
		`INSERT INTO host_services (host_id, name, active, restarts) VALUES ($1, 'nginx.service', 'failed', 3)`,
		`INSERT INTO host_watched_services (host_id, name) VALUES ($1, 'nginx.service')`,
		`INSERT INTO alert_pending (host_id, alert_type, subject, level) VALUES ($1, 'disk_latency', 'nvme0n1', 'warning')`,
		`INSERT INTO docker_containers (host_id, name, image, status, cpu_pct, ram_mb, health, health_failing_streak, exit_code, oom_killed)
		 VALUES ($1, 'api', 'i', 'running', 0, 0, 'unhealthy', 2, 137, true)`,
		`UPDATE host_status SET system_state = '{"oom_kills": 1}' WHERE host_id = $1`,
		`UPDATE host_services SET restart_history = '[[1, 3]]' WHERE host_id = $1`,
		`INSERT INTO status_alert_rules (host_id, rule, level, duration_seconds) VALUES ($1, 'service_failed', 'critical', 60)`,
		`INSERT INTO status_alert_rules (rule, level) SELECT 'reboot_required', 'off' WHERE $1::uuid IS NOT NULL`,
	} {
		if _, err := pool.Exec(ctx, ok, host); err != nil {
			t.Fatalf("rejected: %s: %v", ok, err)
		}
	}
	if _, err := pool.Exec(ctx, `INSERT INTO threshold_defaults (metric_type, warning_level, critical_level) VALUES ('temperature', 80, 90)`); err != nil {
		t.Fatalf("a temperature default was rejected: %v", err)
	}
	for _, bad := range []string{
		`INSERT INTO alerts (host_id, alert_type, level) VALUES ($1, 'gpu', 'info')`,
		`INSERT INTO host_custom_thresholds (host_id, metric_type, subject, warning_level, critical_level) VALUES ($1, 'cpu', 'x', 1, 2)`,
		`INSERT INTO host_custom_thresholds (host_id, metric_type, subject, warning_level, critical_level) VALUES ($1, 'time_offset', 'x', 1, 2)`,
		`INSERT INTO host_custom_thresholds (host_id, metric_type, warning_level, critical_level, duration_seconds) VALUES ($1, 'ram', 1, 2, 0)`,
		`INSERT INTO docker_containers (host_id, name, image, status, cpu_pct, ram_mb, health) VALUES ($1, 'x', 'i', 'running', 0, 0, 'sick')`,
		`INSERT INTO alert_pending (host_id, alert_type, subject, level) VALUES ($1, 'disk_latency', 'nvme0n1', 'critical')`, // aynı anahtar
		`INSERT INTO host_services (host_id, name, active, restarts) VALUES ($1, 'x.service', 'active', -1)`,
		`INSERT INTO status_alert_rules (host_id, rule, level) VALUES ($1, 'gpu_hot', 'info')`,
		`INSERT INTO status_alert_rules (host_id, rule, level) VALUES ($1, 'oom_kill', 'loud')`,
		`INSERT INTO status_alert_rules (host_id, rule, level, duration_seconds) VALUES ($1, 'fs_readonly', 'critical', 60)`,
		`INSERT INTO status_alert_rules (host_id, rule, level, duration_seconds) VALUES ($1, 'oom_kill', 'warning', 0)`,
		`INSERT INTO status_alert_rules (host_id, rule, level) VALUES ($1, 'service_failed', 'warning')`,           // aynı kapsam + kural
		`INSERT INTO status_alert_rules (rule, level) SELECT 'reboot_required', 'info' WHERE $1::uuid IS NOT NULL`, // genel, ikinci kez
		`INSERT INTO status_alert_rules (organization_id, host_id, rule, level) SELECT o.id, $1, 'oom_kill', 'info' FROM organizations o LIMIT 1`,
	} {
		if _, err := pool.Exec(ctx, bad, host); err == nil {
			t.Errorf("accepted: %s", bad)
		}
	}
	if err := old.RequireUpToDate(ctx); !errors.Is(err, migrate.ErrDatabaseNewer) {
		t.Fatalf("old binary on the new schema: err=%v, want ErrDatabaseNewer", err)
	}

	down, err := fs.ReadFile(migrations.FS, "000007_protocol4_health_performance.down.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, string(down)); err != nil {
		t.Fatalf("down migration: %v", err)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM healthbeat_migrations WHERE version = 7`); err != nil {
		t.Fatal(err)
	}
	if err := old.RequireUpToDate(ctx); err != nil {
		t.Fatalf("old binary after the down migration: %v, want it to start", err)
	}
	var alerts, thresholds, tables, columns int
	if err := pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM alerts), (SELECT count(*) FROM host_custom_thresholds) + (SELECT count(*) FROM threshold_defaults),
		(SELECT count(*) FROM pg_tables WHERE schemaname = current_schema() AND tablename IN ('host_services', 'host_watched_services', 'alert_pending', 'status_alert_rules')),
		(SELECT count(*) FROM information_schema.columns WHERE table_schema = current_schema()
		    AND column_name IN ('system_json', 'disk_io_json', 'net_io_json', 'system_state', 'health', 'health_failing_streak', 'exit_code', 'oom_killed', 'duration_seconds'))`).
		Scan(&alerts, &thresholds, &tables, &columns); err != nil {
		t.Fatal(err)
	}
	if alerts != 0 || thresholds != 0 || tables != 0 || columns != 0 {
		t.Fatalf("after the down migration: alerts=%d thresholds=%d tables=%d columns=%d, want all 0", alerts, thresholds, tables, columns)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO alerts (host_id, alert_type, level) VALUES ($1, 'oom_kill', 'warning')`, host); err == nil {
		t.Fatal("the alert_type CHECK is not back after the down migration")
	}
	if applied, err := latest.Up(ctx); err != nil || len(applied) != 1 {
		t.Fatalf("upgrading again: applied=%d err=%v, want 000007 re-applied", len(applied), err)
	}
}

// 000008: bakım pencereleri. İzinler rollere dağıtılır, pencere kısıtları çalışır; .down.sql tabloları, sütunları ve
// izinleri geri alır, eski binary yeniden açılır ve tekrar yükseltilebilir.
func TestMaintenanceWindowsMigrationAndRollback(t *testing.T) {
	ctx := context.Background()
	pool := testdb.NewEmpty(t)
	runner := func(fsys fs.FS) *migrate.Runner {
		t.Helper()
		r, err := migrate.New(pool, fsys)
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	latest, old := runner(upTo(t, "000008")), runner(upTo(t, "000007"))
	if _, err := latest.Up(ctx); err != nil {
		t.Fatal(err)
	}

	var grants string
	if err := pool.QueryRow(ctx, `SELECT string_agg(role || ':' || permission_key, ',' ORDER BY role, permission_key)
		FROM role_permissions WHERE permission_key LIKE 'maintenance.%'`).Scan(&grants); err != nil {
		t.Fatal(err)
	}
	if want := "operator:maintenance.view,org_admin:maintenance.manage,org_admin:maintenance.view,super_admin:maintenance.manage,super_admin:maintenance.view"; grants != want {
		t.Fatalf("maintenance grants = %q, want %q", grants, want)
	}

	host := testdb.PushHost(t, pool, testdb.Org(t, pool, "o"), "h", "hash")
	var weekly uuid.UUID
	if _, err := pool.Exec(ctx, `INSERT INTO maintenance_windows (title, recurrence, starts_at, ends_at)
		VALUES ('x', 'once', now(), now() + interval '1 hour')`); err != nil {
		t.Fatalf("a one-off window was rejected: %v", err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO maintenance_windows (title, recurrence, start_minute, duration_minutes, repeat_every, weekdays, valid_from)
		VALUES ('y', 'weekly', 120, 90, 2, 10, current_date) RETURNING id`).Scan(&weekly); err != nil {
		t.Fatalf("a weekly window was rejected: %v", err)
	}
	for _, ok := range []string{
		`INSERT INTO maintenance_window_hosts (window_id, host_id) VALUES ($1, $2)`,
		`INSERT INTO maintenance_occurrence_overrides (window_id, occurrence_start) VALUES ($1, now())`,
	} {
		args := []any{weekly}
		if strings.Contains(ok, "host_id") {
			args = append(args, host)
		}
		if _, err := pool.Exec(ctx, ok, args...); err != nil {
			t.Fatalf("rejected: %s: %v", ok, err)
		}
	}
	for _, bad := range []string{
		`INSERT INTO maintenance_windows (title, recurrence, starts_at, ends_at) VALUES ('x', 'yearly', now(), now() + interval '1 hour')`,
		`INSERT INTO maintenance_windows (title, recurrence, starts_at, ends_at) VALUES ('x', 'once', now(), now())`,
		`INSERT INTO maintenance_windows (title, recurrence, start_minute, duration_minutes, valid_from) VALUES ('x', 'daily', 1440, 60, current_date)`,
		`INSERT INTO maintenance_windows (title, recurrence, start_minute, duration_minutes, valid_from) VALUES ('x', 'weekly', 0, 60, current_date)`,
		`INSERT INTO maintenance_windows (title, recurrence, start_minute, duration_minutes, valid_from) VALUES ('x', 'monthly', 0, 60, current_date)`,
		`INSERT INTO maintenance_windows (title, recurrence, start_minute, duration_minutes, valid_from, month_week, month_weekday) VALUES ('x', 'monthly', 0, 60, current_date, 5, 1)`,
		`INSERT INTO maintenance_windows (title, recurrence, start_minute, duration_minutes, repeat_every, valid_from) VALUES ('x', 'daily', 0, 60, 31, current_date)`,
		`INSERT INTO maintenance_windows (title, recurrence, start_minute, duration_minutes, valid_from) VALUES ('x', 'weekly', 0, 10081, current_date)`,
		`UPDATE app_settings SET timezone = repeat('x', 65)`,
	} {
		if _, err := pool.Exec(ctx, bad); err == nil {
			t.Errorf("accepted: %s", bad)
		}
	}
	if _, err := pool.Exec(ctx, `UPDATE alerts SET notify_pending = true`); err != nil {
		t.Fatal(err)
	}
	if err := old.RequireUpToDate(ctx); !errors.Is(err, migrate.ErrDatabaseNewer) {
		t.Fatalf("old binary on the new schema: err=%v, want ErrDatabaseNewer", err)
	}

	down, err := fs.ReadFile(migrations.FS, "000008_maintenance_windows.down.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, string(down)); err != nil {
		t.Fatalf("down migration: %v", err)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM healthbeat_migrations WHERE version = 8`); err != nil {
		t.Fatal(err)
	}
	if err := old.RequireUpToDate(ctx); err != nil {
		t.Fatalf("old binary after the down migration: %v, want it to start", err)
	}
	var tables, columns, perms int
	if err := pool.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM pg_tables WHERE schemaname = current_schema() AND tablename LIKE 'maintenance_%'),
		(SELECT count(*) FROM information_schema.columns WHERE table_schema = current_schema()
		    AND ((table_name = 'alerts' AND column_name = 'notify_pending') OR (table_name = 'app_settings' AND column_name = 'timezone'))),
		(SELECT count(*) FROM permissions WHERE key LIKE 'maintenance.%')`).Scan(&tables, &columns, &perms); err != nil {
		t.Fatal(err)
	}
	if tables != 0 || columns != 0 || perms != 0 {
		t.Fatalf("after the down migration: tables=%d columns=%d permissions=%d, want all 0", tables, columns, perms)
	}
	if applied, err := latest.Up(ctx); err != nil || len(applied) != 1 {
		t.Fatalf("upgrading again: applied=%d err=%v, want 000008 re-applied", len(applied), err)
	}
}
