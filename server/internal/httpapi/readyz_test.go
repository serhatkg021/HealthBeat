package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Veritabanına ulaşılamayınca /readyz 503 döner; /healthz süreç ayakta olduğu için 200 kalır.
func TestReadyzReportsAnUnreachableDatabase(t *testing.T) {
	pool, err := pgxpool.New(context.Background(), "postgres://hb@127.0.0.1:1/hb?connect_timeout=1") // bağlantıyı ilk kullanımda açar
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	h := NewDeps(pool, nil, nil, RateLimits{}, nil).Router()

	rec := do(h, "GET", "/readyz", "10.0.0.1:1234", nil, "")
	var body struct{ Code string }
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if rec.Code != http.StatusServiceUnavailable || body.Code != errorCodeDatabaseUnavailable {
		t.Fatalf("/readyz = %d %s, want 503 %s", rec.Code, rec.Body, errorCodeDatabaseUnavailable)
	}
	if rec := do(h, "GET", "/healthz", "10.0.0.1:1234", nil, ""); rec.Code != http.StatusOK {
		t.Fatalf("/healthz = %d, want 200 (the process is up)", rec.Code)
	}
}
