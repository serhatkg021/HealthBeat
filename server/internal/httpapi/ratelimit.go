package httpapi

import (
	"math"
	"net"
	"net/http"
	"strconv"
	"time"

	"healthbeat-server/internal/ratelimit"
)

// remoteIP TCP karşı taraf adresidir. X-Forwarded-For bilerek güvenilmez: server TLS'i kendisi
// sonlandırır (önünde proxy yok); bu yüzden başlık saldırgan denetiminde olur ve herkesin
// sınırlayıcıyı atlatmasına izin verirdi. Önüne güvenilir bir reverse proxy konursa bunun
// bir güvenilir-proxy ayarına ihtiyacı olur.
func remoteIP(r *http.Request) string {
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
