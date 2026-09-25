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

// apiError, API'nin hata yanıtıdır: {"error": "…", "code": "…", "request_id": "…", "fields": {…}}.
//
// Message kullanıcıya gösterilen Türkçe metindir; Code istemcinin mesaja bakmadan karar vermesi içindir (yeni
// kodlar eklenebilir, var olanların anlamı değişmez); RequestID o isteğin log satırlarını bulmayı sağlar (bkz.
// withRequestID); Fields, doğrulama hatasında alan adı → sorun eşlemesidir.
type apiError struct {
	Status    int               `json:"-"`
	Message   string            `json:"error"`
	Code      string            `json:"code"`
	RequestID string            `json:"request_id,omitempty"`
	Fields    map[string]string `json:"fields,omitempty"`
}

func (e *apiError) Error() string { return e.Message }

// Genel hata kodları; writeError onları durum kodundan türetir.
const (
	errorCodeValidationFailed = "validation_failed"
	errorCodeUnauthorized     = "unauthorized"
	errorCodeForbidden        = "forbidden"
	errorCodeNotFound         = "not_found"
	errorCodeConflict         = "conflict"
	errorCodeRateLimited      = "rate_limited"
	errorCodeInternal         = "internal"
)

// errorCodePasswordChangeRequired, bir istemcinin "önce şifrenizi değiştirmelisiniz"i sıradan
// bir 403'ten ayırt etmesini sağlar.
const errorCodePasswordChangeRequired = "password_change_required"

// codeForStatus, özel bir kod verilmemiş hatanın kodudur.
func codeForStatus(status int) string {
	switch {
	case status == http.StatusUnauthorized:
		return errorCodeUnauthorized
	case status == http.StatusForbidden:
		return errorCodeForbidden
	case status == http.StatusNotFound:
		return errorCodeNotFound
	case status == http.StatusConflict:
		return errorCodeConflict
	case status == http.StatusTooManyRequests:
		return errorCodeRateLimited
	case status >= 500:
		return errorCodeInternal
	default:
		// 400 ve diğer 4xx'ler (ör. 413 gövde çok büyük): istek geçersiz.
		return errorCodeValidationFailed
	}
}

// writeAPIError, e'yi yazar; kod boşsa durum kodundan türetilir. İstek kimliği, withRequestID'nin yanıt başlığına
// koyduğu değerdir: handler'ın r'yi taşımasına gerek kalmaz.
func writeAPIError(w http.ResponseWriter, e *apiError) {
	if e.Code == "" {
		e.Code = codeForStatus(e.Status)
	}
	if e.RequestID == "" {
		e.RequestID = w.Header().Get(HeaderRequestID)
	}
	writeJSON(w, e.Status, e)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeAPIError(w, &apiError{Status: status, Message: message})
}

func writeErrorCode(w http.ResponseWriter, status int, message, code string) {
	writeAPIError(w, &apiError{Status: status, Message: message, Code: code})
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
