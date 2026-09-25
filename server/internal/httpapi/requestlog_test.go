package httpapi

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"healthbeat-server/internal/logging"
)

// logRecords, testin süresince varsayılan logger'ı JSON'a çevirir ve yazılan kayıtları döndüren bir fonksiyon verir.
func logRecords(t *testing.T) func() []map[string]any {
	t.Helper()
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(logging.New(&buf, slog.LevelDebug, logging.FormatJSON))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return func() []map[string]any {
		var recs []map[string]any
		for _, line := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
			if line == "" {
				continue
			}
			var rec map[string]any
			if err := json.Unmarshal([]byte(line), &rec); err != nil {
				t.Fatalf("log line is not JSON: %q", line)
			}
			recs = append(recs, rec)
		}
		return recs
	}
}

// serveLogged, h'yi Router'daki istek kimliği + istek logu zinciriyle çalıştırır.
func serveLogged(d *Deps, h http.Handler, req *http.Request) *httptest.ResponseRecorder {
	rr := httptest.NewRecorder()
	withRequestID(d.resolveClientIP(d.requestLog(h))).ServeHTTP(rr, req)
	return rr
}

func requestRecord(t *testing.T, recs []map[string]any) map[string]any {
	t.Helper()
	for _, r := range recs {
		if r["msg"] == "request" || r["msg"] == "request failed" {
			return r
		}
	}
	t.Fatalf("no request log record in %v", recs)
	return nil
}

func TestRequestIDIsGeneratedKeptOrReplaced(t *testing.T) {
	logRecords(t)
	d := newLimitedDeps()
	var seen string
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { seen = logging.RequestID(r.Context()) })

	rr := serveLogged(d, h, httptest.NewRequest(http.MethodGet, "/x", nil))
	if got := rr.Header().Get(HeaderRequestID); got == "" || got != seen {
		t.Fatalf("generated id: header %q, context %q", got, seen)
	}

	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	req.Header.Set(HeaderRequestID, "proxy-abc.123")
	if got := serveLogged(d, h, req).Header().Get(HeaderRequestID); got != "proxy-abc.123" {
		t.Fatalf("valid incoming id not kept: %q", got)
	}

	req = httptest.NewRequest(http.MethodGet, "/x", nil)
	req.Header.Set(HeaderRequestID, "bad id\nwith newline")
	if got := serveLogged(d, h, req).Header().Get(HeaderRequestID); got == "" || strings.ContainsAny(got, " \n") {
		t.Fatalf("invalid incoming id not replaced: %q", got)
	}
}

func TestPanicReturnsJSON500AndLogsStack(t *testing.T) {
	records := logRecords(t)
	d := newLimitedDeps()
	h := http.HandlerFunc(func(http.ResponseWriter, *http.Request) { panic("boom") })

	rr := serveLogged(d, h, httptest.NewRequest(http.MethodGet, "/api/v1/explode", nil))
	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rr.Code)
	}
	var body map[string]string
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("body is not JSON: %s", rr.Body.String())
	}
	id := rr.Header().Get(HeaderRequestID)
	if body["request_id"] != id || body["error"] == "" || body["code"] != errorCodeInternal {
		t.Fatalf("body = %v, want error and request_id %q", body, id)
	}

	recs := records()
	var panicRec map[string]any
	for _, r := range recs {
		if r["msg"] == "panic recovered" {
			panicRec = r
		}
	}
	if panicRec == nil || panicRec["request_id"] != id || !strings.Contains(panicRec["stack"].(string), "requestlog_test.go") {
		t.Fatalf("panic not logged with request_id and stack: %v", panicRec)
	}
	if req := requestRecord(t, recs); req["status"] != float64(500) || req["level"] != "ERROR" {
		t.Fatalf("request record = %v, want status 500 at ERROR", req)
	}
}

func TestFailedRequestLogsMaskedDetails(t *testing.T) {
	records := logRecords(t)
	d := newLimitedDeps()
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var v map[string]any
		_ = json.NewDecoder(r.Body).Decode(&v)
		writeError(w, http.StatusBadRequest, "pull_port geçersiz")
	})

	body := `{"title":"web-01","pull_port":"abc","pull_secret":"s3cr3t-value","nested":{"new_password":"hunter2"},"list":[{"api_token":"tok-1"}]}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/hosts?q=web&token=qs-secret", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer jwt-secret")
	req.Header.Set("Cookie", "session=cookie-secret")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "test-agent")
	serveLogged(d, h, req)

	rec := requestRecord(t, records())
	line, _ := json.Marshal(rec)
	for _, secret := range []string{"s3cr3t-value", "hunter2", "tok-1", "qs-secret", "jwt-secret", "cookie-secret"} {
		if strings.Contains(string(line), secret) {
			t.Fatalf("secret %q leaked into the log: %s", secret, line)
		}
	}
	if rec["level"] != "WARN" || rec["msg"] != "request failed" {
		t.Fatalf("want WARN 'request failed', got %s", line)
	}
	if !strings.Contains(rec["req_body"].(string), `"pull_port":"abc"`) || !strings.Contains(rec["req_body"].(string), redacted) {
		t.Fatalf("request body not logged with masking: %s", line)
	}
	if !strings.Contains(rec["resp_body"].(string), "pull_port geçersiz") {
		t.Fatalf("response body not logged: %s", line)
	}
	headers, _ := rec["req_headers"].(map[string]any)
	if headers["User-Agent"] != "test-agent" || headers["Authorization"] != nil || headers["Cookie"] != nil {
		t.Fatalf("headers = %v, want only allow-listed ones", headers)
	}
	if q := rec["query"].(string); q != "q=web&token=[REDACTED]" {
		t.Fatalf("query = %q", q)
	}
}

func TestFailedRequestWithoutBodyOmitsBodyField(t *testing.T) {
	records := logRecords(t)
	d := newLimitedDeps()
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { writeError(w, http.StatusNotFound, "yok") })
	serveLogged(d, h, httptest.NewRequest(http.MethodGet, "/x", nil))
	serveLogged(d, h, httptest.NewRequest(http.MethodPost, "/x", strings.NewReader("")))
	for _, rec := range records() {
		if _, ok := rec["req_body"]; ok {
			t.Fatalf("empty request body logged: %v", rec)
		}
	}
}

func TestFailedRequestLogsUnreadBody(t *testing.T) {
	records := logRecords(t)
	d := newLimitedDeps()
	// Kimlik doğrulamada reddedilen istek gibi: handler gövdeyi hiç okumaz.
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeError(w, http.StatusUnauthorized, "Bearer token eksik")
	})
	serveLogged(d, h, httptest.NewRequest(http.MethodPost, "/api/v1/metrics", strings.NewReader(`{"cpu":12.5}`)))

	if got := requestRecord(t, records())["req_body"]; got != `{"cpu":12.5}` {
		t.Fatalf("req_body = %v", got)
	}
}

func TestSuccessfulRequestLogsNoBodyAndIngestIsDebug(t *testing.T) {
	records := logRecords(t)
	d := newLimitedDeps()
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		writeJSON(w, http.StatusOK, map[string]string{"access_token": "tok"})
	})

	serveLogged(d, h, httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(`{"password":"p"}`)))
	serveLogged(d, h, httptest.NewRequest(http.MethodPost, "/api/v1/metrics", strings.NewReader(`{}`)))

	recs := records()
	if len(recs) != 2 {
		t.Fatalf("got %d records, want 2: %v", len(recs), recs)
	}
	for _, r := range recs {
		if _, ok := r["req_body"]; ok {
			t.Fatalf("successful request logged a body: %v", r)
		}
	}
	if recs[0]["level"] != "INFO" || recs[1]["level"] != "DEBUG" {
		t.Fatalf("levels = %v, %v; want INFO, DEBUG", recs[0]["level"], recs[1]["level"])
	}
}

func TestErrorBodyLoggingCanBeDisabledAndTruncates(t *testing.T) {
	records := logRecords(t)
	d := newLimitedDeps()
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		writeError(w, http.StatusBadRequest, "geçersiz")
	})

	d.SetErrorBodyLogging(0)
	serveLogged(d, h, httptest.NewRequest(http.MethodPost, "/x", strings.NewReader(`{"a":1}`)))
	if rec := requestRecord(t, records()); rec["req_body"] != nil || rec["resp_body"] != nil {
		t.Fatalf("bodies logged although disabled: %v", rec)
	}

	records = logRecords(t)
	d.SetErrorBodyLogging(35) // ilk 35 bayt: {"email":"a@b.c","password":"very-l
	// Kesme parolanın ortasına denk gelir: ayrıştırılamayan JSON'da da değer maskelenmeli.
	serveLogged(d, h, httptest.NewRequest(http.MethodPost, "/x", strings.NewReader(`{"email":"a@b.c","password":"very-long-secret-value"}`)))
	rec := requestRecord(t, records())
	if rec["req_body_truncated"] != true || strings.Contains(rec["req_body"].(string), "very") {
		t.Fatalf("truncated body not masked: %v", rec)
	}
}

func TestMaskBody(t *testing.T) {
	cases := []struct {
		name, in  string
		truncated bool
		encoding  string
		want      string
	}{
		{"nested json", `{"a":1,"refresh_token":"x","b":{"current_password":2}}`, false, "", `{"a":1,"b":{"current_password":"[REDACTED]"},"refresh_token":"[REDACTED]"}`},
		{"broken json", `{"password":"abc","x":1`, false, "", `{"password":"[REDACTED]","x":1`},
		{"truncated mid-value", `{"token":"abcdef`, true, "", `{"token":"[REDACTED]"`},
		{"unquoted value", `{"secret":12345,`, true, "", `{"secret":"[REDACTED]",`},
		{"plain text", `hello`, false, "", `hello`},
		{"gzip", "\x1f\x8b", false, "gzip", "[gzip-encoded body omitted]"},
		{"binary", "\xff\xfe\x00\x01garbage\xff", false, "", "[binary body omitted]"},
	}
	for _, c := range cases {
		if got := maskBody([]byte(c.in), c.truncated, c.encoding); got != c.want {
			t.Errorf("%s: maskBody = %q, want %q", c.name, got, c.want)
		}
	}
}
