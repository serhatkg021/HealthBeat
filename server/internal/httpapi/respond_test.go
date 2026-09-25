package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCodeForStatus(t *testing.T) {
	cases := map[int]string{
		http.StatusBadRequest:            errorCodeValidationFailed,
		http.StatusRequestEntityTooLarge: errorCodeValidationFailed,
		http.StatusUnauthorized:          errorCodeUnauthorized,
		http.StatusForbidden:             errorCodeForbidden,
		http.StatusNotFound:              errorCodeNotFound,
		http.StatusConflict:              errorCodeConflict,
		http.StatusTooManyRequests:       errorCodeRateLimited,
		http.StatusInternalServerError:   errorCodeInternal,
		http.StatusServiceUnavailable:    errorCodeInternal,
	}
	for status, want := range cases {
		if got := codeForStatus(status); got != want {
			t.Errorf("codeForStatus(%d) = %q, want %q", status, got, want)
		}
	}
}

func decodeAPIError(t *testing.T, rr *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var body map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("error body is not JSON: %q", rr.Body.String())
	}
	return body
}

func TestErrorResponseCarriesCodeAndRequestID(t *testing.T) {
	logRecords(t) // istek logunu teste karıştırma
	d := newLimitedDeps()

	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { writeError(w, http.StatusNotFound, "sunucu bulunamadı") })
	rr := serveLogged(d, h, httptest.NewRequest(http.MethodGet, "/x", nil))
	body := decodeAPIError(t, rr)
	if body["error"] != "sunucu bulunamadı" || body["code"] != errorCodeNotFound {
		t.Fatalf("body = %v", body)
	}
	if id := rr.Header().Get(HeaderRequestID); id == "" || body["request_id"] != id {
		t.Fatalf("request_id = %v, header = %q; want them equal", body["request_id"], id)
	}
	if _, ok := body["fields"]; ok {
		t.Fatalf("empty fields must be omitted: %v", body)
	}

	// Özel kod durum kodundan türetilenin yerine geçer.
	h = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeErrorCode(w, http.StatusForbidden, "önce şifrenizi değiştirin", errorCodePasswordChangeRequired)
	})
	if body := decodeAPIError(t, serveLogged(d, h, httptest.NewRequest(http.MethodGet, "/x", nil))); body["code"] != errorCodePasswordChangeRequired {
		t.Fatalf("custom code not kept: %v", body)
	}

	h = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeAPIError(w, &apiError{Status: http.StatusBadRequest, Message: "geçersiz", Fields: map[string]string{"title": "boş olamaz"}})
	})
	body = decodeAPIError(t, serveLogged(d, h, httptest.NewRequest(http.MethodPost, "/x", nil)))
	if fields, _ := body["fields"].(map[string]any); body["code"] != errorCodeValidationFailed || fields["title"] != "boş olamaz" {
		t.Fatalf("validation error = %v", body)
	}
}

func TestErrorResponseWithoutRequestIDOmitsIt(t *testing.T) {
	rr := httptest.NewRecorder()
	writeError(rr, http.StatusConflict, "çakışma")
	body := decodeAPIError(t, rr)
	if _, ok := body["request_id"]; ok || body["code"] != errorCodeConflict {
		t.Fatalf("body = %v", body)
	}
}

func TestRateLimitedResponseHasCode(t *testing.T) {
	rr := httptest.NewRecorder()
	writeTooManyRequests(rr, 0)
	if body := decodeAPIError(t, rr); body["code"] != errorCodeRateLimited || rr.Header().Get("Retry-After") != "1" {
		t.Fatalf("body = %v, Retry-After = %q", body, rr.Header().Get("Retry-After"))
	}
}
