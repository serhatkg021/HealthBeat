package httpapi

import (
	"errors"
	"net/http"
	"testing"

	"healthbeat-server/internal/store"
)

// Her neden kendi sınıfına uygun bir durum koduyla eşlenir: ErrNotFound → 404, ErrConflict → 409 (ya da istek hatası
// sayılanlar için 400).
func TestStoreReasonStatusesMatchTheirClass(t *testing.T) {
	for _, r := range storeReasons {
		notFound, conflict := errors.Is(r.err, store.ErrNotFound), errors.Is(r.err, store.ErrConflict)
		switch {
		case r.message == "":
			t.Errorf("%v: empty message", r.err)
		case notFound && r.status != http.StatusNotFound:
			t.Errorf("%v: status %d, want 404", r.err, r.status)
		case conflict && r.status != http.StatusConflict && r.status != http.StatusBadRequest:
			t.Errorf("%v: status %d, want 409 or 400", r.err, r.status)
		case !notFound && !conflict:
			t.Errorf("%v: wraps neither ErrNotFound nor ErrConflict", r.err)
		}
	}
}
