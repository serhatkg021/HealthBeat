package httpapi

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestHandleTranslatesErrors(t *testing.T) {
	d := newLimitedDeps()
	cases := []struct {
		name       string
		err        error
		wantStatus int
		wantMsg    string
		wantCode   string
		wantLog    string // loga düşmesi beklenen ERROR satırının mesajı; "" = ERROR yok
	}{
		{"api error", notFound("sunucu bulunamadı"), http.StatusNotFound, "sunucu bulunamadı", errorCodeNotFound, ""},
		{"wrapped api error", fmt.Errorf("sarılı: %w", conflict("çakışma: aynı ad")), http.StatusConflict, "çakışma: aynı ad", errorCodeConflict, ""},
		{"forbidden", forbidden(), http.StatusForbidden, "yetkiniz yok", errorCodeForbidden, ""},
		{"server error", failWith("sunucu alınamadı")("get host", errors.New("db down")), http.StatusInternalServerError, "sunucu alınamadı", errorCodeInternal, "get host"},
		{"plain error", errors.New("oops"), http.StatusInternalServerError, "beklenmeyen bir hata oluştu", errorCodeInternal, "unhandled handler error"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			records := logRecords(t)
			h := handle(func(w http.ResponseWriter, r *http.Request) error { return tc.err })
			rr := serveLogged(d, h, httptest.NewRequest(http.MethodGet, "/x", nil))
			body := decodeAPIError(t, rr)
			if rr.Code != tc.wantStatus || body["error"] != tc.wantMsg || body["code"] != tc.wantCode {
				t.Fatalf("status %d body %v", rr.Code, body)
			}
			if body["request_id"] == nil {
				t.Fatalf("request_id missing: %v", body)
			}
			var gotLog string
			for _, rec := range records() {
				if rec["level"] == "ERROR" && rec["msg"] != "request failed" {
					gotLog = fmt.Sprint(rec["msg"])
					if rec["err"] == nil {
						t.Fatalf("error log without err attribute: %v", rec)
					}
				}
			}
			if gotLog != tc.wantLog {
				t.Fatalf("error log = %q, want %q", gotLog, tc.wantLog)
			}
		})
	}
}

func TestHandleSuccessWritesNothingExtra(t *testing.T) {
	h := handle(func(w http.ResponseWriter, r *http.Request) error {
		writeJSON(w, http.StatusCreated, map[string]string{"ok": "1"})
		return nil
	})
	rr := httptest.NewRecorder()
	h(rr, httptest.NewRequest(http.MethodPost, "/x", nil))
	if rr.Code != http.StatusCreated || strings.TrimSpace(rr.Body.String()) != `{"ok":"1"}` {
		t.Fatalf("status %d body %q", rr.Code, rr.Body.String())
	}
}

func TestPathID(t *testing.T) {
	id := uuid.New()
	r := httptest.NewRequest(http.MethodGet, "/x", nil)
	r.SetPathValue("id", id.String())
	if got, err := pathID(r, "geçersiz"); err != nil || got != id {
		t.Fatalf("pathID = %v, %v", got, err)
	}

	r.SetPathValue("id", "abc")
	_, err := pathID(r, "geçersiz sunucu kimliği")
	var apiErr *apiError
	if !errors.As(err, &apiErr) || apiErr.Status != http.StatusBadRequest || apiErr.Message != "geçersiz sunucu kimliği" {
		t.Fatalf("pathID error = %#v", err)
	}
}
