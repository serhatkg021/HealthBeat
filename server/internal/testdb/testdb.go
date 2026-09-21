// Package testdb, testlere yalıtılmış, tamamen migrate edilmiş bir PostgreSQL şeması verir.
//
// Veritabanı gereken testler testdb.New(t) çağırır. TEST_DATABASE_URL ayarlı değilse testi
// atlar; böylece `go test ./...` Postgres'i olmayan bir makinede de yeşil kalır. Her çağrı o
// veritabanında geçici bir şema oluşturur, her migrations/*.up.sql'i oraya uygular ve test
// bitince siler — böylece testler public şemasındaki tablolara asla dokunmaz ve CREATEDB
// yetkisi gerekmez. Yine de TEST_DATABASE_URL'i bir geliştirme veritabanına yöneltin, asla
// üretim veritabanına değil.
package testdb

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"healthbeat-server/internal/migrate"
	"healthbeat-server/migrations"
)

// New, tam uygulama şemasını ve seed'lenmiş role_permissions matrisini içeren taze bir şemayı
// search_path'i olan bir havuz döndürür; testler kullanıcısız başlar.
func New(t *testing.T) *pgxpool.Pool {
	t.Helper()
	pool := NewEmpty(t)
	applyMigrations(t, context.Background(), pool)
	if _, err := pool.Exec(context.Background(), "DELETE FROM users"); err != nil {
		t.Fatalf("testdb: clear users: %v", err)
	}
	return pool
}

// NewEmpty, taze, BOŞ bir şemada (hiç tablo yok) bir havuz döndürür — migration runner'ın
// kendi testleri için. New gibi TEST_DATABASE_URL olmadan atlar ve test bitince şemayı siler.
func NewEmpty(t *testing.T) *pgxpool.Pool {
	t.Helper()
	pool, _ := newEmpty(t)
	return pool
}

// EmptyURL, kendi bağlantılarını açan kod (test edilen bir komut satırı aracı) için
// NewEmpty'dir: search_path'i taze, boş şema olan bir bağlantı URL'i döndürür.
func EmptyURL(t *testing.T) string {
	t.Helper()
	_, url := newEmpty(t)
	return url
}

func newEmpty(t *testing.T) (*pgxpool.Pool, string) {
	t.Helper()

	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping database-backed test")
	}
	ctx := context.Background()

	admin, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatalf("testdb: connect: %v", err)
	}

	var suffix [6]byte
	if _, err := rand.Read(suffix[:]); err != nil {
		t.Fatalf("testdb: random schema name: %v", err)
	}
	schema := "hbtest_" + hex.EncodeToString(suffix[:])

	if _, err := admin.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		admin.Close()
		t.Fatalf("testdb: create schema: %v", err)
	}

	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		admin.Close()
		t.Fatalf("testdb: parse url: %v", err)
	}
	// public yalnızca orada kurulu eklenti fonksiyonları (pgcrypto) çözülebilsin diye yolda kalır;
	// migration'ların oluşturduğu her tablo/fonksiyon ilk girdiye düşer.
	cfg.ConnConfig.RuntimeParams["search_path"] = schema + ", public"
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		admin.Close()
		t.Fatalf("testdb: connect to schema: %v", err)
	}

	t.Cleanup(func() {
		pool.Close()
		if _, err := admin.Exec(context.Background(), "DROP SCHEMA "+schema+" CASCADE"); err != nil {
			t.Errorf("testdb: drop schema %s: %v", schema, err)
		}
		admin.Close()
	})

	sep := "?"
	if strings.Contains(url, "?") {
		sep = "&"
	}
	return pool, url + sep + "search_path=" + schema
}

func applyMigrations(t *testing.T, ctx context.Context, pool *pgxpool.Pool) {
	t.Helper()
	// Server'ın kullandığı aynı runner, aynı gömülü dosyalarla: bozuk bir migration her veritabanı
	// testini başarısız kılar ve runner'ın kendisi hepsi tarafından sınanır.
	r, err := migrate.New(pool, migrations.FS)
	if err != nil {
		t.Fatalf("testdb: load migrations: %v", err)
	}
	if _, err := r.Up(ctx); err != nil {
		t.Fatalf("testdb: apply migrations: %v", err)
	}
}
