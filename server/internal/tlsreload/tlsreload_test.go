package tlsreload

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// writePair, cn için taze bir kendinden imzalı sertifika/anahtar yazar ve iki dosyayı da mod ile
// damgalar; böylece testler uyumak yerine değiştirme zamanlarını denetler.
func writePair(t *testing.T, dir, cn string, mod time.Time) (certFile, keyFile string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()),
		Subject:      pkix.Name{CommonName: cn},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, _ := x509.MarshalECPrivateKey(key)

	certFile, keyFile = filepath.Join(dir, "cert.pem"), filepath.Join(dir, "key.pem")
	if err := os.WriteFile(certFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyFile, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{certFile, keyFile} {
		if err := os.Chtimes(f, mod, mod); err != nil {
			t.Fatal(err)
		}
	}
	return certFile, keyFile
}

func cnOf(t *testing.T, r *Reloader) string {
	t.Helper()
	c, err := r.GetCertificate(nil)
	if err != nil {
		t.Fatal(err)
	}
	leaf, err := x509.ParseCertificate(c.Certificate[0])
	if err != nil {
		t.Fatal(err)
	}
	return leaf.Subject.CommonName
}

type clock struct{ t time.Time }

func (c *clock) now() time.Time { return c.t }

func newReloader(t *testing.T, certFile, keyFile string) (*Reloader, *clock) {
	t.Helper()
	r, err := New(certFile, keyFile)
	if err != nil {
		t.Fatal(err)
	}
	clk := &clock{t: time.Now()}
	r.now, r.lastCheck = clk.now, clk.t
	return r, clk
}

func TestPicksUpRenewedCertificate(t *testing.T) {
	dir := t.TempDir()
	t0 := time.Now().Add(-24 * time.Hour)
	certFile, keyFile := writePair(t, dir, "old", t0)
	r, clk := newReloader(t, certFile, keyFile)

	if got := cnOf(t, r); got != "old" {
		t.Fatalf("initial cert CN = %q", got)
	}

	writePair(t, dir, "renewed", t0.Add(time.Hour)) // certbot dosyaları yeniden yazar
	if got := cnOf(t, r); got != "old" {
		t.Fatalf("checked the disk again before the recheck interval: CN = %q", got)
	}

	clk.t = clk.t.Add(2 * recheckEvery)
	if got := cnOf(t, r); got != "renewed" {
		t.Fatalf("after renewal CN = %q, want renewed", got)
	}
}

func TestKeepsServingWhenNewFilesAreBroken(t *testing.T) {
	dir := t.TempDir()
	t0 := time.Now().Add(-24 * time.Hour)
	certFile, keyFile := writePair(t, dir, "good", t0)
	r, clk := newReloader(t, certFile, keyFile)

	// Yarım yazılmış bir yenileme: sertifika çöple değiştirildi, mtime değişti.
	if err := os.WriteFile(certFile, []byte("not a certificate"), 0o600); err != nil {
		t.Fatal(err)
	}
	os.Chtimes(certFile, t0.Add(time.Hour), t0.Add(time.Hour))

	clk.t = clk.t.Add(2 * recheckEvery)
	if got := cnOf(t, r); got != "good" {
		t.Fatalf("CN = %q after a broken renewal, want the previous certificate", got)
	}

	// Geçerli bir çift gelince, sonraki bir denetimde alınır.
	writePair(t, dir, "fixed", t0.Add(2*time.Hour))
	clk.t = clk.t.Add(2 * recheckEvery)
	if got := cnOf(t, r); got != "fixed" {
		t.Fatalf("CN = %q after the files were repaired, want fixed", got)
	}
}

func TestKeepsServingWhenFilesTemporarilyMissing(t *testing.T) {
	dir := t.TempDir()
	certFile, keyFile := writePair(t, dir, "good", time.Now().Add(-time.Hour))
	r, clk := newReloader(t, certFile, keyFile)

	os.Remove(certFile)
	clk.t = clk.t.Add(2 * recheckEvery)
	if got := cnOf(t, r); got != "good" {
		t.Fatalf("CN = %q with the file missing, want the previous certificate", got)
	}
}

func TestNewFailsOnUnusableFiles(t *testing.T) {
	dir := t.TempDir()
	if _, err := New(filepath.Join(dir, "nope.pem"), filepath.Join(dir, "nope.key")); err == nil {
		t.Fatal("New succeeded with missing files")
	}
	certFile, _ := writePair(t, dir, "a", time.Now())
	otherDir := t.TempDir()
	_, otherKey := writePair(t, otherDir, "b", time.Now())
	if _, err := New(certFile, otherKey); err == nil {
		t.Fatal("New accepted a certificate with a mismatched key")
	}
}

func TestTLSConfig(t *testing.T) {
	certFile, keyFile := writePair(t, t.TempDir(), "a", time.Now())
	r, _ := newReloader(t, certFile, keyFile)
	cfg := r.TLSConfig()
	if cfg.GetCertificate == nil || cfg.MinVersion < 0x0303 {
		t.Fatalf("TLSConfig = %+v, want GetCertificate and TLS >= 1.2", cfg)
	}
}
