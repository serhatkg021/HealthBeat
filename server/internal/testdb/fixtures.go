package testdb

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"

	"healthbeat-server/internal/secretbox"
)

// Fixture'lar satırları internal/store üzerinden değil düz SQL ile ekler; böylece store'un (ve
// onun üzerine kurulu kodun) testleri kendi verilerini kurmak için test edilen koda bağlı olmaz.

func Org(t *testing.T, pool *pgxpool.Pool, name string) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	err := pool.QueryRow(context.Background(),
		`INSERT INTO organizations (name) VALUES ($1) RETURNING id`, name).Scan(&id)
	if err != nil {
		t.Fatalf("testdb: insert organization %q: %v", name, err)
	}
	return id
}

// ChildOrg, parentID'nin altında bir organizasyon ekler.
func ChildOrg(t *testing.T, pool *pgxpool.Pool, name string, parentID uuid.UUID) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	err := pool.QueryRow(context.Background(),
		`INSERT INTO organizations (name, parent_organization_id) VALUES ($1, $2) RETURNING id`, name, parentID).Scan(&id)
	if err != nil {
		t.Fatalf("testdb: insert child organization %q: %v", name, err)
	}
	return id
}

// PushHost, api_token_hash'i apiTokenHash olan bir push modu host ekler (token'ı
// kullanılabilir kılmak için authsvc.HashOpaqueSecret(token) geçin). title panel adıdır.
func PushHost(t *testing.T, pool *pgxpool.Pool, orgID uuid.UUID, title, apiTokenHash string) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	err := pool.QueryRow(context.Background(),
		`INSERT INTO hosts (organization_id, title, ip, mode, interval_seconds, api_token_hash)
		 VALUES ($1, $2, '10.0.0.1', 'push', 10, $3) RETURNING id`,
		orgID, title, apiTokenHash).Scan(&id)
	if err != nil {
		t.Fatalf("testdb: insert host %q: %v", title, err)
	}
	for _, q := range []string{`INSERT INTO host_status (host_id) VALUES ($1)`, `INSERT INTO host_inventory (host_id) VALUES ($1)`} {
		if _, err := pool.Exec(context.Background(), q, id); err != nil {
			t.Fatalf("testdb: insert host companion rows %q: %v", title, err)
		}
	}
	return id
}

// User, şifrenin gerçek (minimum maliyetli) bir bcrypt hash'iyle bir kullanıcı ekler.
func User(t *testing.T, pool *pgxpool.Pool, email, role, password string) uuid.UUID {
	t.Helper()
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	var id uuid.UUID
	err = pool.QueryRow(context.Background(),
		`INSERT INTO users (email, password_hash, role) VALUES ($1, $2, $3) RETURNING id`,
		email, string(hash), role).Scan(&id)
	if err != nil {
		t.Fatalf("testdb: insert user %q: %v", email, err)
	}
	return id
}

func AssignOrg(t *testing.T, pool *pgxpool.Pool, userID, orgID uuid.UUID) {
	t.Helper()
	if _, err := pool.Exec(context.Background(),
		`INSERT INTO user_organizations (user_id, organization_id) VALUES ($1, $2)`, userID, orgID); err != nil {
		t.Fatalf("testdb: assign org: %v", err)
	}
}

func AssignHost(t *testing.T, pool *pgxpool.Pool, userID, hostID uuid.UUID) {
	t.Helper()
	if _, err := pool.Exec(context.Background(),
		`INSERT INTO user_hosts (user_id, host_id) VALUES ($1, $2)`, userID, hostID); err != nil {
		t.Fatalf("testdb: assign host: %v", err)
	}
}

// Threshold bir eşik satırı ekler: hostID verilirse o sunucuya özel (host_custom_thresholds), verilmezse varsayılan
// (threshold_defaults; nil orgID global demektir).
func Threshold(t *testing.T, pool *pgxpool.Pool, orgID, hostID *uuid.UUID, metric string, warn, crit float64) {
	t.Helper()
	var err error
	if hostID != nil {
		_, err = pool.Exec(context.Background(),
			`INSERT INTO host_custom_thresholds (host_id, metric_type, warning_level, critical_level)
			 VALUES ($1, $2, $3, $4)`, *hostID, metric, warn, crit)
	} else {
		_, err = pool.Exec(context.Background(),
			`INSERT INTO threshold_defaults (organization_id, metric_type, warning_level, critical_level)
			 VALUES ($1, $2, $3, $4)`, orgID, metric, warn, crit)
	}
	if err != nil {
		t.Fatalf("testdb: insert threshold: %v", err)
	}
}

// SubjectThreshold, host'ın bir metriği için subject'e (mount yolu / container adı) özel eşik ekler.
func SubjectThreshold(t *testing.T, pool *pgxpool.Pool, hostID uuid.UUID, metric, subject string, warn, crit float64) {
	t.Helper()
	if _, err := pool.Exec(context.Background(),
		`INSERT INTO host_custom_thresholds (host_id, metric_type, subject, warning_level, critical_level)
		 VALUES ($1, $2, $3, $4, $5)`, hostID, metric, subject, warn, crit); err != nil {
		t.Fatalf("testdb: insert subject threshold: %v", err)
	}
}

// MountThreshold, host'ın tek bir mount'u için disk eşiği ekler.
func MountThreshold(t *testing.T, pool *pgxpool.Pool, hostID uuid.UUID, mount string, warn, crit float64) {
	t.Helper()
	SubjectThreshold(t, pool, hostID, "disk", mount, warn, crit)
}

// SecretBox, test için sabit bir anahtarla bir Box döndürür.
func SecretBox(t *testing.T) *secretbox.Box {
	t.Helper()
	box, err := secretbox.New([]byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		t.Fatal(err)
	}
	return box
}
