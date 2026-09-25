package httpapi

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"

	"healthbeat-server/internal/logging"
	"healthbeat-server/internal/version"
)

// HeaderRequestID, isteğin kimliğini taşır: gelen istekte geçerliyse (ör. reverse proxy'nin koyduğu) korunur, yoksa
// üretilir; her yanıtta döner ve o isteğin bütün log satırlarında request_id olarak yer alır.
const HeaderRequestID = "X-Request-ID"

// DefaultErrorBodyBytes, LOG_ERROR_BODY_BYTES'ın varsayılanıdır.
const DefaultErrorBodyBytes = 4096

// validRequestID, dışarıdan gelen bir kimliği kabul etmek için yeterince sıkıdır: log satırını bozamaz ya da şişiremez.
var validRequestID = regexp.MustCompile(`^[A-Za-z0-9._:-]{1,64}$`)

// withRequestID, isteğe bir kimlik verir, yanıt başlığına yazar ve log bağlamını (logging.RequestInfo) kurar.
func withRequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get(HeaderRequestID)
		if !validRequestID.MatchString(id) {
			id = uuid.NewString()
		}
		w.Header().Set(HeaderRequestID, id)
		ctx := logging.WithRequestInfo(r.Context(), &logging.RequestInfo{ID: id})
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// requestLog, her isteği bir satırla loglar ve handler'daki panic'i kurtarır (istemciye JSON 500, loga yığın izi).
//
// Seviye durum koduna göredir: 5xx ERROR, 4xx WARN, diğerleri INFO; başarılı ingest ve /healthz DEBUG'dır (150+ host
// ~30 sn'de bir rapor verir, INFO'da logu doldururdu). Hata alan isteklerde veriyle ilgili sorunlar yeniden
// üretilmeden incelenebilsin diye query, güvenli başlıklar, istek gövdesi ve yanıt gövdesi de yazılır (en fazla
// errorBodyBytes). Şifre, token ve secret içeren alanlar ile Authorization/Cookie başlıkları asla yazılmaz.
func (d *Deps) requestLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		limit := d.errorBodyBytes

		var reqBody *bodyCapture
		if limit > 0 && r.Body != nil && r.Body != http.NoBody {
			reqBody = &bodyCapture{ReadCloser: r.Body, limit: limit}
			r.Body = reqBody
		}
		rec := &responseRecorder{ResponseWriter: w, status: http.StatusOK, limit: limit}

		serveRecovering(rec, r, next)

		status := rec.status
		level := slog.LevelInfo
		switch {
		case status >= 500:
			level = slog.LevelError
		case status >= 400:
			level = slog.LevelWarn
		case isRoutine(r):
			level = slog.LevelDebug
		}
		ctx := r.Context()
		if !slog.Default().Enabled(ctx, level) {
			return
		}

		attrs := []slog.Attr{
			slog.String("method", r.Method),
			slog.String("path", r.URL.Path),
			slog.Int("status", status),
			slog.Duration("duration", time.Since(start)),
		}
		if status >= 400 {
			if r.URL.RawQuery != "" {
				attrs = append(attrs, slog.String("query", maskQuery(r.URL.RawQuery)))
			}
			attrs = append(attrs, safeHeaders(r.Header))
			if reqBody != nil {
				reqBody.drain()
				if reqBody.buf.Len() > 0 {
					attrs = append(attrs, bodyAttrs("req", reqBody.buf.Bytes(), reqBody.truncated, r.Header)...)
				}
			}
			if limit > 0 && rec.buf.Len() > 0 {
				attrs = append(attrs, bodyAttrs("resp", rec.buf.Bytes(), rec.truncated, rec.Header())...)
			}
		}
		msg := "request"
		if status >= 400 {
			msg = "request failed"
		}
		slog.LogAttrs(ctx, level, msg, attrs...)
	})
}

// isRoutine, başarılı olduğunda yalnızca DEBUG'da loglanan sık ve sıradan isteklerdir.
func isRoutine(r *http.Request) bool {
	return (r.Method == http.MethodPost && r.URL.Path == "/api/v1/metrics") ||
		(r.Method == http.MethodGet && r.URL.Path == "/healthz")
}

// serveRecovering, next'i çalıştırır; bir panic'i loglayıp (yığın iziyle) istemciye JSON 500 döner, süreç ayakta
// kalır. http.ErrAbortHandler bilerek yeniden fırlatılır: net/http onu yanıtı kesmek için kullanır.
func serveRecovering(w *responseRecorder, r *http.Request, next http.Handler) {
	defer func() {
		v := recover()
		if v == nil {
			return
		}
		if v == http.ErrAbortHandler {
			panic(v)
		}
		logging.LogPanic(r.Context(), r.Method+" "+r.URL.Path, v)
		if w.wroteHeader {
			// Yanıt yarıda kaldı; durum kodu artık değiştirilemez ama log 500 göstersin.
			w.status = http.StatusInternalServerError
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{
			"error":      "beklenmeyen bir hata oluştu",
			"request_id": logging.RequestID(r.Context()),
		})
	}()
	next.ServeHTTP(w, r)
}

// responseRecorder durum kodunu ve (yalnızca hata yanıtlarında) gövdenin ilk limit baytını tutar.
type responseRecorder struct {
	http.ResponseWriter
	status      int
	wroteHeader bool

	limit     int
	buf       bytes.Buffer
	truncated bool
}

func (r *responseRecorder) WriteHeader(status int) {
	if !r.wroteHeader {
		r.status = status
		r.wroteHeader = true
	}
	r.ResponseWriter.WriteHeader(status)
}

func (r *responseRecorder) Write(p []byte) (int, error) {
	if !r.wroteHeader {
		r.WriteHeader(http.StatusOK)
	}
	if r.status >= 400 && r.limit > 0 {
		r.truncated = capture(&r.buf, p, r.limit) || r.truncated
	}
	return r.ResponseWriter.Write(p)
}

// Unwrap, http.ResponseController'ın alttaki ResponseWriter'a ulaşmasını sağlar.
func (r *responseRecorder) Unwrap() http.ResponseWriter { return r.ResponseWriter }

// bodyCapture, handler'ın okuduğu istek gövdesinin ilk limit baytını tutar.
type bodyCapture struct {
	io.ReadCloser
	limit     int
	buf       bytes.Buffer
	truncated bool
	eof       bool
}

func (b *bodyCapture) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	b.truncated = capture(&b.buf, p[:n], b.limit) || b.truncated
	if err == io.EOF {
		b.eof = true
	}
	return n, err
}

// drain, handler'ın okumadığı gövdeyi (ör. kimlik doğrulamada reddedilen istek) limit dolana kadar okur; hata
// logunda gövdenin görünmesi içindir. Okunamazsa elde olanla yetinilir.
func (b *bodyCapture) drain() {
	if b.eof || b.truncated {
		return
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(b, int64(b.limit-b.buf.Len()+1)))
}

// capture p'yi buf'a limit dolana kadar ekler; sığmayan bir şey kaldıysa true döner.
func capture(buf *bytes.Buffer, p []byte, limit int) bool {
	room := limit - buf.Len()
	if room <= 0 {
		return len(p) > 0
	}
	if len(p) > room {
		buf.Write(p[:room])
		return true
	}
	buf.Write(p)
	return false
}

// loggedHeaders, hata logunda gösterilen başlıklardır (izin listesi). Authorization, Cookie ve benzeri kimlik
// bilgileri bilerek yoktur.
var loggedHeaders = []string{
	"Content-Type", "Content-Length", "Content-Encoding", "User-Agent", "Origin",
	"X-Host-ID", version.HeaderAgentVersion, version.HeaderProtocol,
}

func safeHeaders(h http.Header) slog.Attr {
	var attrs []any
	for _, k := range loggedHeaders {
		if v := h.Get(k); v != "" {
			attrs = append(attrs, slog.String(k, v))
		}
	}
	return slog.Group("req_headers", attrs...)
}

// redacted, maskelenen değerlerin yerine yazılır.
const redacted = "[REDACTED]"

// sensitiveKey, değeri loga yazılmayan alan adlarıdır: password, current_password, token, access_token,
// refresh_token, api_token, pull_secret vb. Yeni bir hassas alan bu kalıba uymuyorsa buraya eklenmeli.
var sensitiveKey = regexp.MustCompile(`(?i)passw|token|secret|authorization|cookie`)

// sensitiveJSONValue, ayrıştırılamayan (kesilmiş ya da bozuk) JSON'da hassas bir anahtarın değerini bulur: tırnaklı
// değer (sonu kesilmiş olabilir) ya da tırnaksız bir değer.
var sensitiveJSONValue = regexp.MustCompile(`(?i)("[^"]*(?:passw|token|secret|authorization|cookie)[^"]*"\s*:\s*)("(?:[^"\\]|\\.)*(?:"|\\?$)|[^,}\]\s]+)`)

// bodyAttrs, bir gövdeyi maskeli olarak <prefix>_body (ve kesildiyse <prefix>_body_truncated) alanlarına çevirir.
func bodyAttrs(prefix string, body []byte, truncated bool, h http.Header) []slog.Attr {
	attrs := []slog.Attr{slog.String(prefix+"_body", maskBody(body, truncated, h.Get("Content-Encoding")))}
	if truncated {
		attrs = append(attrs, slog.Bool(prefix+"_body_truncated", true))
	}
	return attrs
}

func maskBody(body []byte, truncated bool, encoding string) string {
	if encoding != "" && !strings.EqualFold(encoding, "identity") {
		return "[" + encoding + "-encoded body omitted]"
	}
	if !utf8.Valid(body) {
		// Kesilme çok baytlı bir karakteri bölmüş olabilir; yalnızca sondaki eksik karakteri at.
		trimmed := bytes.ToValidUTF8(body, nil)
		if !truncated || len(body)-len(trimmed) > utf8.UTFMax {
			return "[binary body omitted]"
		}
		body = trimmed
	}
	if !truncated {
		var v any
		dec := json.NewDecoder(bytes.NewReader(body))
		dec.UseNumber()
		if dec.Decode(&v) == nil && !dec.More() {
			var out bytes.Buffer
			enc := json.NewEncoder(&out)
			enc.SetEscapeHTML(false)
			if enc.Encode(maskJSON(v)) == nil {
				return strings.TrimSuffix(out.String(), "\n")
			}
		}
	}
	return sensitiveJSONValue.ReplaceAllString(string(body), `${1}"`+redacted+`"`)
}

// maskJSON, hassas anahtarların değerini (türü ne olursa olsun) her derinlikte maskeler.
func maskJSON(v any) any {
	switch t := v.(type) {
	case map[string]any:
		for k, val := range t {
			if sensitiveKey.MatchString(k) {
				t[k] = redacted
			} else {
				t[k] = maskJSON(val)
			}
		}
	case []any:
		for i, val := range t {
			t[i] = maskJSON(val)
		}
	}
	return v
}

func maskQuery(raw string) string {
	q, err := url.ParseQuery(raw)
	if err != nil {
		return "[unparseable query omitted]"
	}
	for k := range q {
		if sensitiveKey.MatchString(k) {
			q[k] = []string{redacted}
		}
	}
	// Encode köşeli parantezleri kaçışlar; maske okunur kalsın.
	return strings.ReplaceAll(q.Encode(), url.QueryEscape(redacted), redacted)
}
