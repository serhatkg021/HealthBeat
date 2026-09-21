package httpapi

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

// Bu testler bilerek nil havuzlu bir Deps kullanır: vurdukları her yol herhangi bir veritabanı
// erişiminden önce reddedilir; kısılmış ya da hatalı biçimli bir isteğin yapması gereken tam olarak budur.

func newLimitedDeps() *Deps {
	return NewDeps(nil, nil, nil, RateLimits{AuthFailuresPerMinute: 60, IngestPerMinute: 60}, nil)
}

func do(h http.Handler, method, path, remoteAddr string, headers map[string]string, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.RemoteAddr = remoteAddr
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestIngestAuthFailuresAreThrottledPerIP(t *testing.T) {
	h := newLimitedDeps().Router()

	// Hatalı bir X-Host-ID herhangi bir DB aramasından önce reddedilir; ilk authFailureBurst
	// deneme 401 alır ve her biri IP'den düşer.
	for i := 0; i < authFailureBurst; i++ {
		rec := do(h, "POST", "/api/v1/metrics", "203.0.113.7:4000", nil, "{}")
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("attempt %d: status = %d, want 401", i+1, rec.Code)
		}
	}

	rec := do(h, "POST", "/api/v1/metrics", "203.0.113.7:4000", nil, "{}")
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("status after burst = %d, want 429", rec.Code)
	}
	if secs, err := strconv.Atoi(rec.Header().Get("Retry-After")); err != nil || secs < 1 {
		t.Fatalf("Retry-After = %q, want a positive integer", rec.Header().Get("Retry-After"))
	}

	// Farklı bir kaynak IP etkilenmez ve kaynak portu önemsizdir.
	if rec := do(h, "POST", "/api/v1/metrics", "198.51.100.9:4000", nil, "{}"); rec.Code != http.StatusUnauthorized {
		t.Fatalf("other IP: status = %d, want 401", rec.Code)
	}
	if rec := do(h, "POST", "/api/v1/metrics", "203.0.113.7:9999", nil, "{}"); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("same IP, new port: status = %d, want 429", rec.Code)
	}
}

func TestLoginThrottledOnceFailureBudgetSpent(t *testing.T) {
	d := newLimitedDeps()
	h := d.Router()

	for i := 0; i < authFailureBurst; i++ {
		d.loginFailures.Allow("203.0.113.7") // handleLogin'in başarısız deneme başına yaptığı gibi
	}

	rec := do(h, "POST", "/api/v1/auth/login", "203.0.113.7:1234", nil, `{"email":"a@b.c","password":"x"}`)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("login status = %d, want 429", rec.Code)
	}
	rec = do(h, "POST", "/api/v1/auth/refresh", "203.0.113.7:1234", nil, `{"refresh_token":"x"}`)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("refresh status = %d, want 429 (shares the login failure budget)", rec.Code)
	}

	// Kısılmamış bir IP'de doğrulama hataları yine çalışır ve DB'ye dokunmaz.
	rec = do(h, "POST", "/api/v1/auth/login", "198.51.100.9:1234", nil, `{}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("empty login body status = %d, want 400", rec.Code)
	}
}

func TestLoginAndIngestFailureBudgetsAreSeparate(t *testing.T) {
	d := newLimitedDeps()
	h := d.Router()

	for i := 0; i < authFailureBurst; i++ {
		d.ingestFailures.Allow("203.0.113.7")
	}

	// Aynı NAT'ın arkasındaki kötü davranan bir push host, panel kullanıcılarının giriş yapmasını
	// engellememeli.
	rec := do(h, "POST", "/api/v1/auth/login", "203.0.113.7:1234", nil, `{}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("login status = %d, want 400 (not throttled)", rec.Code)
	}
}

func TestOversizedBodyRejected(t *testing.T) {
	h := newLimitedDeps().Router()

	big := `{"email":"` + strings.Repeat("a", maxBodyBytes) + `","password":"x"}`
	rec := do(h, "POST", "/api/v1/auth/login", "198.51.100.9:1234", nil, big)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 for a body over %d bytes", rec.Code, maxBodyBytes)
	}
}
