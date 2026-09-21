package migrate_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/fstest"

	"github.com/jackc/pgx/v5/pgxpool"

	"healthbeat-server/internal/migrate"
	"healthbeat-server/internal/testdb"
	"healthbeat-server/migrations"
)

func files(kv map[string]string) fstest.MapFS {
	m := fstest.MapFS{}
	for name, body := range kv {
		m[name] = &fstest.MapFile{Data: []byte(body)}
	}
	return m
}

func runner(t *testing.T, pool *pgxpool.Pool, fsys fs.FS) *migrate.Runner {
	t.Helper()
	r, err := migrate.New(pool, fsys)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func tableExists(t *testing.T, pool *pgxpool.Pool, name string) bool {
	t.Helper()
	var ok bool
	if err := pool.QueryRow(context.Background(), `SELECT to_regclass(current_schema() || '.' || $1) IS NOT NULL`, name).Scan(&ok); err != nil {
		t.Fatal(err)
	}
	return ok
}

func historyVersions(t *testing.T, pool *pgxpool.Pool) []int64 {
	t.Helper()
	if !tableExists(t, pool, "healthbeat_migrations") {
		return nil
	}
	rows, err := pool.Query(context.Background(), `SELECT version FROM healthbeat_migrations ORDER BY version`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []int64
	for rows.Next() {
		var v int64
		rows.Scan(&v)
		out = append(out, v)
	}
	return out
}

var threeMigrations = map[string]string{
	"000001_users.up.sql":   `CREATE TABLE users (id INT PRIMARY KEY);`,
	"000001_users.down.sql": `DROP TABLE users;`,
	"000002_orders.up.sql":  `CREATE TABLE orders (id INT PRIMARY KEY, user_id INT REFERENCES users (id)); INSERT INTO users VALUES (1);`,
	"000003_extra.up.sql":   `ALTER TABLE orders ADD COLUMN note TEXT;`,
	"000003_extra.down.sql": `ALTER TABLE orders DROP COLUMN note;`,
}

// ---- Load (veritabanı yok) ----------------------------------------------------------------------

func TestLoadOrdersByVersionAndIgnoresDownFiles(t *testing.T) {
	migs, err := migrate.Load(files(map[string]string{
		"000010_late.up.sql": "SELECT 1;", "000002_early.up.sql": "SELECT 2;", "000002_early.down.sql": "SELECT 0;",
		"README.md": "not a migration", "embed.go": "package x",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if len(migs) != 2 || migs[0].Version != 2 || migs[0].Name != "early" || migs[1].Version != 10 {
		t.Fatalf("migrations = %+v", migs)
	}
	if migs[0].Checksum == "" || migs[0].Checksum == migs[1].Checksum {
		t.Fatalf("checksums = %q / %q", migs[0].Checksum, migs[1].Checksum)
	}
	if migs[0].SQL != "SELECT 2;" {
		t.Fatalf("SQL = %q", migs[0].SQL)
	}
}

func TestLoadRejectsMalformedNamesAndDuplicateVersions(t *testing.T) {
	for name, kv := range map[string]map[string]string{
		"no version prefix":  {"users.up.sql": "SELECT 1;"},
		"short version":      {"01_users.up.sql": "SELECT 1;"},
		"uppercase name":     {"000001_Users.up.sql": "SELECT 1;"},
		"missing direction":  {"000001_users.sql": "SELECT 1;"},
		"duplicate versions": {"000001_a.up.sql": "SELECT 1;", "000001_b.up.sql": "SELECT 2;"},
	} {
		if _, err := migrate.Load(files(kv)); err == nil {
			t.Errorf("%s: Load accepted %v", name, kv)
		}
	}
	if migs, err := migrate.Load(files(nil)); err != nil || len(migs) != 0 {
		t.Errorf("empty source: %v %v", migs, err)
	}
}

// ---- Up ----------------------------------------------------------------------------------------

func TestUpAppliesInOrderRecordsHistoryAndIsIdempotent(t *testing.T) {
	pool := testdb.NewEmpty(t)
	ctx := context.Background()
	r := runner(t, pool, files(threeMigrations))

	applied, err := r.Up(ctx)
	if err != nil {
		t.Fatalf("Up: %v", err)
	}
	if len(applied) != 3 || applied[0].Version != 1 || applied[2].Version != 3 {
		t.Fatalf("applied = %+v", applied)
	}
	// Sıra önemliydi: orders, users'a başvurur ve migration 2 ona veri ekledi.
	var n int
	pool.QueryRow(ctx, `SELECT count(*) FROM orders o JOIN users u ON u.id = 1`).Scan(&n)
	if !tableExists(t, pool, "orders") || fmt.Sprint(historyVersions(t, pool)) != "[1 2 3]" {
		t.Fatalf("history = %v", historyVersions(t, pool))
	}

	var name, sum string
	pool.QueryRow(ctx, `SELECT name, checksum FROM healthbeat_migrations WHERE version = 2`).Scan(&name, &sum)
	migs := r.Migrations()
	if name != "orders" || sum != migs[1].Checksum {
		t.Fatalf("history row = %q %q, want orders / %q", name, sum, migs[1].Checksum)
	}

	again, err := r.Up(ctx)
	if err != nil || len(again) != 0 {
		t.Fatalf("second Up = %v, %v; want nothing to do", again, err)
	}

	st, err := r.Status(ctx)
	if err != nil || len(st) != 3 {
		t.Fatalf("Status: %v %v", st, err)
	}
	for _, s := range st {
		if !s.Applied || s.AppliedAt.IsZero() {
			t.Errorf("status %+v not applied", s)
		}
	}
}

func TestOnlyPendingMigrationsRunWhenOneIsAdded(t *testing.T) {
	pool := testdb.NewEmpty(t)
	ctx := context.Background()
	runner(t, pool, files(threeMigrations)).Up(ctx)

	more := map[string]string{"000004_more.up.sql": `CREATE TABLE more (id INT);`}
	for k, v := range threeMigrations {
		more[k] = v
	}
	applied, err := runner(t, pool, files(more)).Up(ctx)
	if err != nil || len(applied) != 1 || applied[0].Version != 4 {
		t.Fatalf("applied = %+v err=%v, want only version 4", applied, err)
	}
}

func TestFailedMigrationLeavesNoTraceOfItself(t *testing.T) {
	pool := testdb.NewEmpty(t)
	ctx := context.Background()
	bad := map[string]string{
		"000001_ok.up.sql": `CREATE TABLE good (id INT);`,
		// İlk ifade başarılı, ikincisi başarısız: tüm migration geri alınmalı.
		"000002_broken.up.sql": `CREATE TABLE half_done (id INT); INSERT INTO table_that_does_not_exist VALUES (1);`,
		"000003_after.up.sql":  `CREATE TABLE never (id INT);`,
	}
	applied, err := runner(t, pool, files(bad)).Up(ctx)
	if err == nil || !strings.Contains(err.Error(), "000002_broken") {
		t.Fatalf("err = %v, want it to name migration 2", err)
	}
	if len(applied) != 1 || applied[0].Version != 1 {
		t.Fatalf("applied = %+v, want only the first", applied)
	}
	if tableExists(t, pool, "half_done") {
		t.Fatal("a failed migration left half of its changes behind")
	}
	if tableExists(t, pool, "never") {
		t.Fatal("migrations after a failure were run")
	}
	if fmt.Sprint(historyVersions(t, pool)) != "[1]" {
		t.Fatalf("history = %v, want only version 1 recorded", historyVersions(t, pool))
	}

	// Dosyayı düzeltip yeniden çalıştırmak, başarısız migration'dan devam eder.
	bad["000002_broken.up.sql"] = `CREATE TABLE half_done (id INT);`
	applied, err = runner(t, pool, files(bad)).Up(ctx)
	if err != nil {
		t.Fatalf("resume: %v", err)
	}
	if len(applied) != 2 || applied[0].Version != 2 || !tableExists(t, pool, "half_done") || !tableExists(t, pool, "never") {
		t.Fatalf("resume applied %+v", applied)
	}
}

// ---- güvensiz durumları reddetmek ------------------------------------------------------------------

func TestEditedMigrationIsRefused(t *testing.T) {
	pool := testdb.NewEmpty(t)
	ctx := context.Background()
	runner(t, pool, files(threeMigrations)).Up(ctx)

	edited := map[string]string{}
	for k, v := range threeMigrations {
		edited[k] = v
	}
	edited["000002_orders.up.sql"] += " -- quietly changed after it was applied"
	edited["000004_new.up.sql"] = `CREATE TABLE shouldnt_run (id INT);`

	applied, err := runner(t, pool, files(edited)).Up(ctx)
	if !errors.Is(err, migrate.ErrChecksumMismatch) || !strings.Contains(err.Error(), "000002_orders") {
		t.Fatalf("err = %v, want ErrChecksumMismatch naming migration 2", err)
	}
	if len(applied) != 0 || tableExists(t, pool, "shouldnt_run") {
		t.Fatal("new migrations ran on top of a tampered history")
	}
	if _, err := runner(t, pool, files(edited)).Status(ctx); !errors.Is(err, migrate.ErrChecksumMismatch) {
		t.Fatalf("Status err = %v", err)
	}
	if err := runner(t, pool, files(edited)).RequireUpToDate(ctx); !errors.Is(err, migrate.ErrChecksumMismatch) {
		t.Fatalf("RequireUpToDate err = %v", err)
	}
}

func TestPendingMigrationOlderThanAppliedOneIsRefused(t *testing.T) {
	pool := testdb.NewEmpty(t)
	ctx := context.Background()
	runner(t, pool, files(map[string]string{
		"000001_a.up.sql": `CREATE TABLE a (id INT);`, "000003_c.up.sql": `CREATE TABLE c (id INT);`,
	})).Up(ctx)

	// Bir dal, 3 zaten uygulanmışken 2 numaralı bir migration'ı birleştirdi.
	_, err := runner(t, pool, files(map[string]string{
		"000001_a.up.sql": `CREATE TABLE a (id INT);`, "000002_b.up.sql": `CREATE TABLE b (id INT);`, "000003_c.up.sql": `CREATE TABLE c (id INT);`,
	})).Up(ctx)
	if !errors.Is(err, migrate.ErrOutOfOrder) || tableExists(t, pool, "b") {
		t.Fatalf("err = %v (table b exists: %v), want ErrOutOfOrder and nothing applied", err, tableExists(t, pool, "b"))
	}
}

func TestDatabaseNewerThanTheBinaryIsRefused(t *testing.T) {
	pool := testdb.NewEmpty(t)
	ctx := context.Background()
	runner(t, pool, files(threeMigrations)).Up(ctx)

	old := map[string]string{}
	for k, v := range threeMigrations {
		if !strings.HasPrefix(k, "000003") {
			old[k] = v
		}
	}
	if _, err := runner(t, pool, files(old)).Up(ctx); !errors.Is(err, migrate.ErrDatabaseNewer) {
		t.Fatalf("Up err = %v, want ErrDatabaseNewer (an older binary must not run on a newer schema)", err)
	}
	if err := runner(t, pool, files(old)).RequireUpToDate(ctx); !errors.Is(err, migrate.ErrDatabaseNewer) {
		t.Fatalf("RequireUpToDate err = %v", err)
	}
}

func TestRequireUpToDate(t *testing.T) {
	pool := testdb.NewEmpty(t)
	ctx := context.Background()
	r := runner(t, pool, files(threeMigrations))

	if err := r.RequireUpToDate(ctx); !errors.Is(err, migrate.ErrPending) || !strings.Contains(err.Error(), "migrate up") {
		t.Fatalf("fresh database: err = %v, want ErrPending pointing at `migrate up`", err)
	}
	if tableExists(t, pool, "users") || tableExists(t, pool, "healthbeat_migrations") {
		t.Fatal("RequireUpToDate changed the database")
	}
	r.Up(ctx)
	if err := r.RequireUpToDate(ctx); err != nil {
		t.Fatalf("after Up: %v", err)
	}
}

// ---- elle migrate edilmiş veritabanlarını benimsemek -------------------------------------------

func TestLegacyDatabaseIsNeverTreatedAsEmpty(t *testing.T) {
	pool := testdb.NewEmpty(t)
	ctx := context.Background()
	// Eski `psql -f` yönteminin geride bıraktığı: tablolar var, geçmiş yok.
	if _, err := pool.Exec(ctx, `CREATE TABLE organizations (id INT); CREATE TABLE users (id INT PRIMARY KEY); CREATE TABLE orders (id INT PRIMARY KEY, user_id INT)`); err != nil { // 2. sürümde duran bir veritabanı
		t.Fatal(err)
	}
	r := runner(t, pool, files(threeMigrations))

	if _, err := r.Up(ctx); !errors.Is(err, migrate.ErrLegacyDatabase) {
		t.Fatalf("Up err = %v, want ErrLegacyDatabase (re-running CREATE TABLE on live data must never happen)", err)
	}
	if tableExists(t, pool, "healthbeat_migrations") {
		t.Fatal("the refusal still created a history table")
	}
	if err := r.RequireUpToDate(ctx); !errors.Is(err, migrate.ErrLegacyDatabase) {
		t.Fatalf("RequireUpToDate err = %v", err)
	}
	if _, err := r.Status(ctx); !errors.Is(err, migrate.ErrLegacyDatabase) {
		t.Fatalf("Status err = %v", err)
	}

	if _, err := r.Baseline(ctx, 99); err == nil {
		t.Fatal("baseline accepted an unknown version")
	}
	marked, err := r.Baseline(ctx, 2)
	if err != nil || len(marked) != 2 {
		t.Fatalf("Baseline(2) = %+v, %v", marked, err)
	}
	if fmt.Sprint(historyVersions(t, pool)) != "[1 2]" {
		t.Fatalf("history = %v", historyVersions(t, pool))
	}
	if _, err := r.Baseline(ctx, 3); err == nil {
		t.Fatal("baseline ran on a database that already has history")
	}

	// Bundan sonra normal bir veritabanıdır: yalnızca migration 3 bekliyor ve çalışır.
	applied, err := r.Up(ctx)
	if err != nil || len(applied) != 1 || applied[0].Version != 3 {
		t.Fatalf("Up after baseline = %+v, %v", applied, err)
	}
}

// ---- eşzamanlılık -----------------------------------------------------------------------------

// Aynı anda tek bir veritabanına karşı açılan sekiz server: her migration tam bir kez çalışır,
// hiç kimse "table already exists" hatası almaz ve şema tamamlanmadan kimse ilerlemez.
func TestConcurrentRunnersApplyEachMigrationExactlyOnce(t *testing.T) {
	pool := testdb.NewEmpty(t)
	ctx := context.Background()
	kv := map[string]string{}
	for i := 1; i <= 6; i++ {
		// "IF NOT EXISTS" değil: ikinci bir uygulama başarısız olurdu. Uyku yarış penceresini genişletir.
		kv[fmt.Sprintf("%06d_t%d.up.sql", i, i)] = fmt.Sprintf(`SELECT pg_sleep(0.03); CREATE TABLE t%d (id INT);`, i)
	}

	var wg sync.WaitGroup
	var total atomic.Int32
	errs := make(chan error, 8)
	start := make(chan struct{})
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r, err := migrate.New(pool, files(kv))
			if err != nil {
				errs <- err
				return
			}
			<-start
			applied, err := r.Up(ctx)
			total.Add(int32(len(applied)))
			if err == nil {
				// Kim önce dönerse, uygulamış da olsa beklemiş de olsa şemanın tamamını bulmalı.
				err = r.RequireUpToDate(ctx)
			}
			errs <- err
		}()
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("a concurrent runner failed: %v", err)
		}
	}
	if total.Load() != 6 {
		t.Fatalf("%d migrations applied across all runners, want exactly 6", total.Load())
	}
	if fmt.Sprint(historyVersions(t, pool)) != "[1 2 3 4 5 6]" {
		t.Fatalf("history = %v", historyVersions(t, pool))
	}
}

// ---- gerçek, gömülü migration'lar -------------------------------------------------------------

func TestEmbeddedMigrationsAreWellFormed(t *testing.T) {
	migs, err := migrate.Load(migrations.FS)
	if err != nil {
		t.Fatal(err)
	}
	if len(migs) < 1 {
		t.Fatalf("only %d migrations embedded", len(migs))
	}
	for i, m := range migs {
		if m.Version != int64(i+1) {
			t.Fatalf("migration #%d has version %d: versions must count up from 1 without gaps", i+1, m.Version)
		}
		if strings.TrimSpace(m.SQL) == "" {
			t.Errorf("migration %06d_%s is empty", m.Version, m.Name)
		}
		down := fmt.Sprintf("%06d_%s.down.sql", m.Version, m.Name)
		if body, err := fs.ReadFile(migrations.FS, down); err != nil || strings.TrimSpace(string(body)) == "" {
			t.Errorf("migration %06d_%s has no usable %s", m.Version, m.Name, down)
		}
	}
}

// Runner'dan önce oluşturulmuş her veritabanı için benimseme yolu (geliştiricinin kendisininki
// dahil): her şeyi eskisi gibi elle uygula, sonra en son sürümde baseline al.
func TestAdoptingAHandMigratedDatabase(t *testing.T) {
	pool := testdb.NewEmpty(t)
	ctx := context.Background()
	migs, err := migrate.Load(migrations.FS)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range migs { // eski yöntem: psql -f, dosya dosya
		if _, err := pool.Exec(ctx, m.SQL); err != nil {
			t.Fatalf("by hand: %06d_%s: %v", m.Version, m.Name, err)
		}
	}
	r := runner(t, pool, migrations.FS)

	if _, err := r.Up(ctx); !errors.Is(err, migrate.ErrLegacyDatabase) {
		t.Fatalf("Up err = %v, want ErrLegacyDatabase", err)
	}
	if _, err := r.Baseline(ctx, migs[len(migs)-1].Version); err != nil {
		t.Fatalf("Baseline: %v", err)
	}
	if err := r.RequireUpToDate(ctx); err != nil {
		t.Fatalf("after baseline: %v", err)
	}
	if applied, err := r.Up(ctx); err != nil || len(applied) != 0 {
		t.Fatalf("Up after baseline = %v, %v; nothing may be re-run on a live database", applied, err)
	}

	// Gelecekteki bir migration bekleyen olarak alınır ve yalnızca o çalışır.
	future := fstest.MapFS{}
	entries, _ := fs.ReadDir(migrations.FS, ".")
	for _, e := range entries {
		body, _ := fs.ReadFile(migrations.FS, e.Name())
		future[e.Name()] = &fstest.MapFile{Data: body}
	}
	future["000999_future_change.up.sql"] = &fstest.MapFile{Data: []byte(`CREATE TABLE future_change (id INT);`)}
	applied, err := runner(t, pool, future).Up(ctx)
	if err != nil || len(applied) != 1 || applied[0].Version != 999 || !tableExists(t, pool, "future_change") {
		t.Fatalf("future migration: %+v, %v", applied, err)
	}
}

// Bir migration ve geçmiş satırı birlikte commit edilir. Kaydı başarısız olursa migration'ın
// kendi değişiklikleri kalmamalı — yoksa sonraki açılışta kendi etkilerinin üstüne yeniden çalışırdı.
func TestMigrationAndItsHistoryRowAreOneTransaction(t *testing.T) {
	pool := testdb.NewEmpty(t)
	ctx := context.Background()
	first := map[string]string{"000001_a.up.sql": `CREATE TABLE a (id INT);`}
	if _, err := runner(t, pool, files(first)).Up(ctx); err != nil {
		t.Fatal(err)
	}
	// SQL'i başarılı olduktan sonra 2. sürümü kaydetmeyi imkânsız kıl.
	if _, err := pool.Exec(ctx, `ALTER TABLE healthbeat_migrations ADD CONSTRAINT refuse_two CHECK (version <> 2)`); err != nil {
		t.Fatal(err)
	}

	both := map[string]string{"000001_a.up.sql": first["000001_a.up.sql"], "000002_b.up.sql": `CREATE TABLE b (id INT);`}
	applied, err := runner(t, pool, files(both)).Up(ctx)
	if err == nil {
		t.Fatal("expected the failed history insert to fail the migration")
	}
	if len(applied) != 0 {
		t.Fatalf("applied = %+v", applied)
	}
	if tableExists(t, pool, "b") {
		t.Fatal("migration 2's changes were committed although recording it failed; a retry would run it twice")
	}
}

// ---- yorumu değişen migration'lar --------------------------------------------------------------

func sum(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}
