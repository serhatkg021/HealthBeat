package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"healthbeat-server/internal/migrate"
	"healthbeat-server/internal/testdb"
	"healthbeat-server/migrations"
)

func migrateCmd(t *testing.T, args ...string) (code int, out, errOut string) {
	t.Helper()
	var so, se bytes.Buffer
	code = runMigrateCommand(args, &so, &se)
	return code, so.String(), se.String()
}

func total(t *testing.T) int {
	t.Helper()
	migs, err := migrate.Load(migrations.FS)
	if err != nil {
		t.Fatal(err)
	}
	return len(migs)
}

func TestMigrateCommandUsageErrors(t *testing.T) {
	t.Setenv("DATABASE_URL", "")
	if code, _, se := migrateCmd(t); code != 2 || !strings.Contains(se, "usage:") {
		t.Errorf("no command: code=%d stderr=%q, want 2 and usage", code, se)
	}
	if code, _, se := migrateCmd(t, "status"); code != 1 || !strings.Contains(se, "DATABASE_URL") {
		t.Errorf("no DATABASE_URL: code=%d stderr=%q", code, se)
	}

	t.Setenv("DATABASE_URL", testdb.EmptyURL(t))
	if code, _, se := migrateCmd(t, "explode"); code != 2 || !strings.Contains(se, "usage:") {
		t.Errorf("unknown command: code=%d stderr=%q", code, se)
	}
	for _, args := range [][]string{{"baseline"}, {"baseline", "abc"}, {"baseline", "99999"}, {"baseline", "1", "2"}} {
		if code, _, _ := migrateCmd(t, args...); code == 0 {
			t.Errorf("migrate %v succeeded", args)
		}
	}
}

func TestMigrateStatusUpStatus(t *testing.T) {
	t.Setenv("DATABASE_URL", testdb.EmptyURL(t))
	n := total(t)

	code, out, se := migrateCmd(t, "status")
	if code != 0 || !strings.Contains(out, "pending") || !strings.Contains(out, fmt.Sprintf("%d migration(s), %d pending", n, n)) {
		t.Fatalf("status on an empty database: code=%d\n%s\n%s", code, out, se)
	}
	code, out, se = migrateCmd(t, "up")
	if code != 0 || !strings.Contains(out, fmt.Sprintf("%d migration(s) applied", n)) {
		t.Fatalf("up: code=%d\n%s\n%s", code, out, se)
	}
	if code, out, _ = migrateCmd(t, "status"); code != 0 || !strings.Contains(out, fmt.Sprintf("%d migration(s), 0 pending", n)) || strings.Contains(out, " pending\n000") {
		t.Fatalf("status after up:\n%s", out)
	}
	if code, out, _ = migrateCmd(t, "up"); code != 0 || !strings.Contains(out, "0 migration(s) applied") {
		t.Fatalf("second up: code=%d %q", code, out)
	}
}

func TestMigrateBaselineAdoptsAHandMigratedDatabase(t *testing.T) {
	url := testdb.EmptyURL(t)
	t.Setenv("DATABASE_URL", url)
	n := total(t)

	// Runner öncesi durum: şema var ama geçmiş yok.
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	migs, _ := migrate.Load(migrations.FS)
	for _, m := range migs {
		if _, err := pool.Exec(ctx, m.SQL); err != nil {
			t.Fatal(err)
		}
	}

	for _, cmd := range []string{"status", "up"} {
		code, _, se := migrateCmd(t, cmd)
		if code == 0 || !strings.Contains(se, "no migration history") || !strings.Contains(se, "migrate baseline") {
			t.Fatalf("migrate %s on a hand-migrated database: code=%d stderr=%q, want a refusal that explains baseline", cmd, code, se)
		}
	}
	code, out, se := migrateCmd(t, "baseline", fmt.Sprint(n))
	if code != 0 || !strings.Contains(out, "nothing was executed") {
		t.Fatalf("baseline: code=%d\n%s\n%s", code, out, se)
	}
	if code, out, _ = migrateCmd(t, "status"); code != 0 || !strings.Contains(out, "0 pending") {
		t.Fatalf("status after baseline:\n%s", out)
	}
	if code, _, _ = migrateCmd(t, "baseline", fmt.Sprint(n)); code == 0 {
		t.Fatal("a second baseline succeeded")
	}
}

func TestPrepareSchemaAutoMigrateAndTheStrictMode(t *testing.T) {
	pool := testdb.NewEmpty(t)
	ctx := context.Background()

	// AUTO_MIGRATE=false: geride kalmış bir şemaya karşı çalışmayı reddet.
	if err := prepareSchema(ctx, pool, false); !errors.Is(err, migrate.ErrPending) {
		t.Fatalf("strict mode on an empty database: err=%v, want ErrPending", err)
	}
	// AUTO_MIGRATE=true (varsayılan): güncelle.
	if err := prepareSchema(ctx, pool, true); err != nil {
		t.Fatalf("auto mode: %v", err)
	}
	if err := prepareSchema(ctx, pool, false); err != nil {
		t.Fatalf("strict mode on an up-to-date database: %v", err)
	}
	if err := prepareSchema(ctx, pool, true); err != nil {
		t.Fatalf("auto mode is not idempotent: %v", err)
	}
}
