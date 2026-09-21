package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"healthbeat-server/internal/config"
	"healthbeat-server/internal/db"
	"healthbeat-server/internal/migrate"
	"healthbeat-server/migrations"
)

// prepareSchema, başka hiçbir şey dokunmadan önce veritabanını bu binary'nin beklediği şemaya
// getirir: bekleyen migration'ları uygular; AUTO_MIGRATE kapalıysa geride kalmış bir şemaya
// karşı çalışmayı reddeder.
func prepareSchema(ctx context.Context, pool *pgxpool.Pool, autoMigrate bool) error {
	r, err := migrate.New(pool, migrations.FS)
	if err != nil {
		return err
	}
	r.Logf = log.Printf

	if !autoMigrate {
		return explain(r.RequireUpToDate(ctx))
	}
	applied, err := r.Up(ctx)
	if err != nil {
		return explain(err)
	}
	if len(applied) > 0 {
		log.Printf("database migrated: %d migration(s) applied (now at %06d)", len(applied), applied[len(applied)-1].Version)
	}
	return nil
}

// explain, operatörün üzerinde işlem yapabileceği hatalara bir sonraki adımı ekler.
func explain(err error) error {
	if errors.Is(err, migrate.ErrLegacyDatabase) {
		return fmt.Errorf("%w. It was probably set up by hand with psql. Find the last migration whose changes are present, then run `healthbeat-server migrate baseline <version>` once (see docs/DEPLOYMENT.md)", err)
	}
	return err
}

const migrateUsage = `usage: healthbeat-server migrate <command>

  status           list every migration and whether it has been applied
  up               apply all pending migrations
  baseline <N>     adopt a database that was migrated by hand: record migrations up to and
                   including version N as applied WITHOUT running them

Uses DATABASE_URL (from the environment or .env).`

// runMigrateCommand, migrate alt komutlarını uygular ve çıkış kodunu döndürür.
func runMigrateCommand(args []string, stdout, stderr io.Writer) int {
	fail := func(format string, a ...any) int {
		fmt.Fprintf(stderr, "migrate: "+format+"\n", a...)
		return 1
	}
	if len(args) == 0 {
		fmt.Fprintln(stderr, migrateUsage)
		return 2
	}

	url, err := config.DatabaseURLOnly()
	if err != nil {
		return fail("%v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	pool, err := db.NewPool(ctx, url)
	if err != nil {
		return fail("%v", err)
	}
	defer pool.Close()
	r, err := migrate.New(pool, migrations.FS)
	if err != nil {
		return fail("%v", err)
	}
	r.Logf = func(format string, a ...any) { fmt.Fprintf(stdout, format+"\n", a...) }

	switch args[0] {
	case "status":
		st, err := r.Status(ctx)
		if err != nil {
			return fail("%v", explain(err))
		}
		pending := 0
		for _, s := range st {
			mark := "pending"
			if s.Applied {
				mark = "applied " + s.AppliedAt.Format("2006-01-02 15:04")
			} else {
				pending++
			}
			fmt.Fprintf(stdout, "%06d  %-45s %s\n", s.Version, s.Name, mark)
		}
		fmt.Fprintf(stdout, "%d migration(s), %d pending\n", len(st), pending)
	case "up":
		applied, err := r.Up(ctx)
		if err != nil {
			return fail("%v", explain(err))
		}
		fmt.Fprintf(stdout, "%d migration(s) applied\n", len(applied))
	case "baseline":
		if len(args) != 2 {
			return fail("baseline needs a version: healthbeat-server migrate baseline <N>")
		}
		version, err := strconv.ParseInt(args[1], 10, 64)
		if err != nil {
			return fail("invalid version %q", args[1])
		}
		marked, err := r.Baseline(ctx, version)
		if err != nil {
			return fail("%v", err)
		}
		fmt.Fprintf(stdout, "recorded %d migration(s) as already applied (up to %06d); nothing was executed\n", len(marked), version)
	default:
		fmt.Fprintln(stderr, migrateUsage)
		return 2
	}
	return 0
}
