// Package tlsreload bir TLS sertifikasını diskten sunar ve yenilemeleri (Let's Encrypt/certbot
// dosyaları ~60 günde bir yeniden yazar) yeniden başlatmadan alır.
package tlsreload

import (
	"crypto/tls"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"time"
)

// recheckEvery, dosyaların ne sıklıkla stat edildiğini sınırlar; bir yenilemenin bundan
// fazla gecikmeyle fark edilmesini değil.
const recheckEvery = time.Minute

type Reloader struct {
	certFile, keyFile string

	mu        sync.Mutex
	cert      *tls.Certificate
	certMod   time.Time
	keyMod    time.Time
	lastCheck time.Time

	now func() time.Time
}

// New sertifikayı bir kez yükler ve yükleyemezse başarısız olur; böylece yanlış yapılandırılmış
// bir server açılmaz.
func New(certFile, keyFile string) (*Reloader, error) {
	r := &Reloader{certFile: certFile, keyFile: keyFile, now: time.Now}
	if err := r.load(); err != nil {
		return nil, err
	}
	r.lastCheck = r.now()
	return r, nil
}

// GetCertificate bir tls.Config.GetCertificate geri çağrısıdır. Dosyalar değiştiyse ve yeni
// çift geçerliyse ona geçer; yeni dosyalar bozuksa (yarım yazılmış, uyumsuz) her el
// sıkışmasını başarısız kılmak yerine önceki sertifikayı sunmaya devam eder ve loglar.
func (r *Reloader) GetCertificate(*tls.ClientHelloInfo) (*tls.Certificate, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if now := r.now(); now.Sub(r.lastCheck) >= recheckEvery {
		r.lastCheck = now
		if r.changed() {
			if err := r.load(); err != nil {
				slog.Error("tls: certificate files changed but cannot be loaded, keeping the previous certificate", "err", err)
			} else {
				slog.Info("tls: reloaded certificate", "file", r.certFile)
			}
		}
	}
	return r.cert, nil
}

// TLSConfig, bu Reloader'ın desteklediği bir server yapılandırması döndürür.
func (r *Reloader) TLSConfig() *tls.Config {
	return &tls.Config{MinVersion: tls.VersionTLS12, GetCertificate: r.GetCertificate}
}

func (r *Reloader) changed() bool {
	c, err1 := os.Stat(r.certFile)
	k, err2 := os.Stat(r.keyFile)
	if err1 != nil || err2 != nil {
		return false // geçici (ör. yeniden adlandırma sırasında); elimizdekini koru
	}
	return !c.ModTime().Equal(r.certMod) || !k.ModTime().Equal(r.keyMod)
}

// load, r.mu tutulurken (ya da r paylaşılmadan önce) çağrılmalı.
func (r *Reloader) load() error {
	c, err := os.Stat(r.certFile)
	if err != nil {
		return fmt.Errorf("tls certificate: %w", err)
	}
	k, err := os.Stat(r.keyFile)
	if err != nil {
		return fmt.Errorf("tls key: %w", err)
	}
	cert, err := tls.LoadX509KeyPair(r.certFile, r.keyFile)
	if err != nil {
		return fmt.Errorf("load tls key pair: %w", err)
	}
	r.cert, r.certMod, r.keyMod = &cert, c.ModTime(), k.ModTime()
	return nil
}
