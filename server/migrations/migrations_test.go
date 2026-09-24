package migrations_test

import (
	"context"
	"errors"
	"io/fs"
	"testing"
	"testing/fstest"

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
	latest, old := runner(migrations.FS), runner(upTo(t, "000001"))

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
