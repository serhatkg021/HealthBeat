package httpapi

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

var okHandler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusTeapot) // CORS'un kendi yazdığı hiçbir şeyle karıştırılamaz
})

func corsDo(h http.Handler, method, origin string, extra map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, "/api/v1/me", nil)
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	for k, v := range extra {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestCORSPreflightFromAllowedOrigin(t *testing.T) {
	h := WithCORS(okHandler, []string{"https://panel.example.com"})
	rec := corsDo(h, "OPTIONS", "https://panel.example.com", map[string]string{
		"Access-Control-Request-Method":  "POST",
		"Access-Control-Request-Headers": "authorization,content-type",
	})
	if rec.Code != http.StatusNoContent {
		t.Fatalf("preflight status = %d, want 204 (answered without reaching the router)", rec.Code)
	}
	for k, want := range map[string]string{
		"Access-Control-Allow-Origin":  "https://panel.example.com",
		"Access-Control-Allow-Methods": "GET, POST, PUT, DELETE",
		"Access-Control-Allow-Headers": "Authorization, Content-Type",
	} {
		if got := rec.Header().Get(k); got != want {
			t.Errorf("%s = %q, want %q", k, got, want)
		}
	}
	if rec.Header().Get("Access-Control-Allow-Credentials") != "" {
		t.Error("credentialed CORS enabled; the API uses bearer tokens, not cookies")
	}
}

func TestCORSActualRequestFromAllowedOrigin(t *testing.T) {
	h := WithCORS(okHandler, []string{"https://panel.example.com"})
	rec := corsDo(h, "GET", "https://panel.example.com", nil)
	if rec.Code != http.StatusTeapot {
		t.Fatalf("status = %d, request should reach the handler", rec.Code)
	}
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "https://panel.example.com" {
		t.Errorf("Allow-Origin = %q", got)
	}
	if got := rec.Header().Get("Access-Control-Expose-Headers"); got != "Retry-After, X-Total-Count" {
		t.Errorf("Expose-Headers = %q", got)
	}
}

func TestCORSDisallowedOriginGetsNoHeaders(t *testing.T) {
	h := WithCORS(okHandler, []string{"https://panel.example.com"})

	for _, origin := range []string{
		"https://evil.example.net",
		"http://panel.example.com",       // yanlış şema
		"https://panel.example.com:8443", // yanlış port
		"https://panel.example.com.evil.net",
		"null",
	} {
		rec := corsDo(h, "GET", origin, nil)
		if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
			t.Errorf("origin %q was granted access (%q)", origin, got)
		}
		if rec.Header().Get("Vary") == "" {
			t.Errorf("origin %q: missing Vary: Origin on a response that depends on it", origin)
		}
	}

	// İzin verilmeyen bir origin'den gelen preflight preflight olarak yanıtlanmaz.
	rec := corsDo(h, "OPTIONS", "https://evil.example.net", map[string]string{"Access-Control-Request-Method": "POST"})
	if rec.Code == http.StatusNoContent || rec.Header().Get("Access-Control-Allow-Methods") != "" {
		t.Fatalf("preflight from a disallowed origin was granted: %d %v", rec.Code, rec.Header())
	}
}

func TestCORSLeavesNonBrowserTrafficAlone(t *testing.T) {
	h := WithCORS(okHandler, []string{"https://panel.example.com"})
	rec := corsDo(h, "GET", "", nil) // agent'lar Origin başlığı göndermez
	if rec.Code != http.StatusTeapot || len(rec.Header()) != 0 {
		t.Fatalf("no-Origin request: %d %v", rec.Code, rec.Header())
	}
}

func TestCORSEmptyAllowlistIsPassthrough(t *testing.T) {
	h := WithCORS(okHandler, nil)
	rec := corsDo(h, "OPTIONS", "https://panel.example.com", map[string]string{"Access-Control-Request-Method": "POST"})
	if rec.Code != http.StatusTeapot || rec.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Fatalf("empty allowlist altered the request: %d %v", rec.Code, rec.Header())
	}
}
