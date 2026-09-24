package httpapi

import (
	"math"
	"net"
	"net/http"
	"strconv"
	"time"

	"healthbeat-server/internal/ratelimit"
)

// remoteIP, hız sınırlarının ve denetim kaydının kullandığı istemci IP'sidir. Router'daki resolveClientIP onu istek
// başına bir kez belirler: TCP eşi, ya da eş TRUSTED_PROXIES'teki bir reverse proxy ise X-Forwarded-For'daki istemci
// (bkz. internal/clientip — başlığa başka hiç kimseden güvenilmez, yoksa herkes sınırlayıcıyı atlatırdı). Router
// dışından çağrılırsa (context'te yoksa) TCP eşine düşer.
func remoteIP(r *http.Request) string {
	if ip, ok := clientIPFromContext(r.Context()); ok {
		return ip
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// rejectIfThrottled, key'in l içinde bütçesi kalmadığında 429 yazar ve true döndürür. Bir
// jeton tüketmez — başarısızlıkta l.Allow ile eşleştirin.
func rejectIfThrottled(w http.ResponseWriter, l *ratelimit.Limiter, key string) bool {
	ok, retry := l.Check(key)
	if ok {
		return false
	}
	writeTooManyRequests(w, retry)
	return true
}

func writeTooManyRequests(w http.ResponseWriter, retryAfter time.Duration) {
	secs := int(math.Ceil(retryAfter.Seconds()))
	if secs < 1 {
		secs = 1
	}
	w.Header().Set("Retry-After", strconv.Itoa(secs))
	writeError(w, http.StatusTooManyRequests, "çok fazla istek")
}
