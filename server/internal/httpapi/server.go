package httpapi

import (
	"crypto/tls"
	"net/http"
	"time"
)

// Server zaman aşımları. Bunlar olmadan bir karşı taraf bağlantı açıp baytları damla damla
// yollayabilir (ya da hiçbir şey yollamayabilir), her biri bir goroutine ve bir dosya
// tanımlayıcısını sonsuza dek bağlar (Slowloris). Buradaki her meşru istek küçük ve hızlıdır;
// bu yüzden bunlar cömerttir; yalnızca okuma tarafı sıkıdır.
const (
	readHeaderTimeout = 10 * time.Second
	readTimeout       = 30 * time.Second
	// writeTimeout en yavaş handler'ı (geniş bir metrik aralığı) kapsamalı.
	writeTimeout   = 60 * time.Second
	idleTimeout    = 120 * time.Second
	maxHeaderBytes = 64 << 10
)

// NewServer yukarıdaki zaman aşımlarıyla HTTPS server'ını kurar.
func NewServer(addr string, handler http.Handler, tlsConfig *tls.Config) *http.Server {
	return &http.Server{
		Addr:              addr,
		Handler:           handler,
		TLSConfig:         tlsConfig,
		ReadHeaderTimeout: readHeaderTimeout,
		ReadTimeout:       readTimeout,
		WriteTimeout:      writeTimeout,
		IdleTimeout:       idleTimeout,
		MaxHeaderBytes:    maxHeaderBytes,
	}
}
