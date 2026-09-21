package httpapi

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"healthbeat-server/internal/store"
)

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if v != nil {
		_ = json.NewEncoder(w).Encode(v)
	}
}

type errorResponse struct {
	Error string `json:"error"`
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, errorResponse{Error: message})
}

// errorCodePasswordChangeRequired, bir istemcinin "önce şifrenizi değiştirmelisiniz"i sıradan
// bir 403'ten ayırt etmesini sağlar.
const errorCodePasswordChangeRequired = "password_change_required"

func writeErrorCode(w http.ResponseWriter, status int, message, code string) {
	writeJSON(w, status, map[string]string{"error": message, "code": code})
}

// maxListLimit, sayfalanabilir listelerde (kullanıcılar, bir organizasyonun sunucuları,
// alert'ler) ?limit= için üst sınırdır.
const maxListLimit = 200

// parseListParams, paylaşılan ?q=&limit=&offset= sözleşmesini ayrıştırır (bkz. store.ListParams).
// limit verilmezse Limit sıfır kalır — çağıran o zaman sayfalamadan tüm satırları döndürür,
// böylece bu parametreleri hiç göndermeyen eski çağıranlar değişmeden çalışmaya devam eder.
func parseListParams(r *http.Request) (store.ListParams, error) {
	q := r.URL.Query()
	p := store.ListParams{Search: strings.TrimSpace(q.Get("q"))}
	if v := q.Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > maxListLimit {
			return store.ListParams{}, fmt.Errorf("limit 1 ile %d arasında olmalı", maxListLimit)
		}
		p.Limit = n
	}
	if v := q.Get("offset"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 {
			return store.ListParams{}, fmt.Errorf("offset negatif olmamalı")
		}
		p.Offset = n
	}
	return p, nil
}

// maxBodyBytes her JSON istek gövdesini sınırlar; en büyük meşru payload (birkaç düzine
// container'lı bir metrik raporu) birkaç KiB'dir.
const maxBodyBytes = 1 << 20

// readJSONBody, gövdeyi maxBodyBytes ile sınırlı okur. Ingest, gövdeyi kendisi ayrıştırır
// (model.ParseMetricsIngest): bilinmeyen alanlar hata değildir, bkz. docs/COMPATIBILITY.md.
func readJSONBody(r *http.Request) ([]byte, error) {
	r.Body = http.MaxBytesReader(nil, r.Body, maxBodyBytes)
	return io.ReadAll(r.Body)
}

func decodeJSON(r *http.Request, v any) error {
	// Nil bir ResponseWriter sorun değil: yalnızca MaxBytesReader'ın aşırı büyük bir gövdeden sonra
	// server'dan bağlantıyı kapatmasını istemesine izin verir.
	r.Body = http.MaxBytesReader(nil, r.Body, maxBodyBytes)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	return dec.Decode(v)
}
