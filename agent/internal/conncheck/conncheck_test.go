package conncheck

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"healthbeat-agent/internal/pusher"
	"healthbeat-agent/internal/version"
)

// server, verilen durumla yanıt veren bir HTTPS server'dır; sertifikası "example.com", 127.0.0.1 ve ::1 içindir.
func server(t *testing.T, status int) (*httptest.Server, *x509.CertPool) {
	t.Helper()
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set(version.HeaderServerVersion, "9.9.9")
		w.WriteHeader(status)
	}))
	t.Cleanup(srv.Close)
	pool := x509.NewCertPool()
	pool.AddCert(srv.Certificate())
	return srv, pool
}

// otherCA, test server'ının sertifikasını imzalamamış bir CA içeren havuzdur.
func otherCA(t *testing.T) *x509.CertPool {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "Başka CA"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	pool.AddCert(cert)
	return pool
}

// run, denemeyi gerçek bir gönderimle (boş rapor) yapar; sonucu ve çıktıyı döndürür.
func run(t *testing.T, url string, opts pusher.TLSOptions) (Result, string) {
	t.Helper()
	var out strings.Builder
	send := func(ctx context.Context) (pusher.ServerInfo, error) {
		p := pusher.New(url, "id", "token", opts)
		err := p.Push(ctx, pusher.MetricsPayload{})
		return p.ServerInfo(), err
	}
	res := Run(context.Background(), url, opts, send, &out)
	for _, line := range strings.Split(strings.TrimRight(out.String(), "\n"), "\n") {
		if utf8.RuneCountInString(line) > 72 {
			t.Errorf("line longer than 72 characters: %q", line)
		}
	}
	return res, out.String()
}

func TestSuccess(t *testing.T) {
	srv, pool := server(t, http.StatusNoContent)
	res, out := run(t, srv.URL, pusher.TLSOptions{RootCAs: pool})
	if res != OK || !strings.Contains(out, "✓ Porta ulaşıldı") || !strings.Contains(out, "✓ Sertifika doğrulandı") ||
		!strings.Contains(out, "✓ Rapor kabul edildi (server 9.9.9)") {
		t.Fatalf("res = %d, out:\n%s", res, out)
	}
}

func TestUntrustedCertificate(t *testing.T) {
	srv, _ := server(t, http.StatusNoContent)
	res, out := run(t, srv.URL, pusher.TLSOptions{})
	if res != ResultUntrusted || !strings.Contains(out, "özel bir CA ile imzalanmış") {
		t.Fatalf("res = %d, out:\n%s", res, out)
	}

	res, out = run(t, srv.URL, pusher.TLSOptions{RootCAs: otherCA(t)}) // verilen CA bu sertifikayı imzalamamış
	if res != ResultUntrusted || !strings.Contains(out, "verilen CA bu sertifikayı") {
		t.Fatalf("wrong CA: res = %d, out:\n%s", res, out)
	}
}

func TestInsecureSkipVerify(t *testing.T) {
	srv, _ := server(t, http.StatusNoContent)
	res, out := run(t, srv.URL, pusher.TLSOptions{InsecureSkipVerify: true})
	if res != OK || !strings.Contains(out, "doğrulama kapalı") {
		t.Fatalf("res = %d, out:\n%s", res, out)
	}
}

func TestCertificateForAnotherName(t *testing.T) {
	srv, pool := server(t, http.StatusNoContent)
	url := strings.Replace(srv.URL, "127.0.0.1", "localhost", 1) // sertifikada "localhost" yok
	res, out := run(t, url, pusher.TLSOptions{RootCAs: pool})
	if res != ResultName || !strings.Contains(out, "(localhost) için düzenlenmemiş") || !strings.Contains(out, "example.com") {
		t.Fatalf("res = %d, out:\n%s", res, out)
	}
}

func TestRejectedToken(t *testing.T) {
	srv, pool := server(t, http.StatusUnauthorized)
	res, out := run(t, srv.URL, pusher.TLSOptions{RootCAs: pool})
	if res != ResultAuth || !strings.Contains(out, "kabul etmedi (401)") || !strings.Contains(out, "Kimlik bilgisini yenile") {
		t.Fatalf("res = %d, out:\n%s", res, out)
	}
}

func TestRejectedReport(t *testing.T) {
	srv, pool := server(t, http.StatusBadRequest)
	res, out := run(t, srv.URL, pusher.TLSOptions{RootCAs: pool})
	if res != ResultRejected || !strings.Contains(out, "HTTP 400") {
		t.Fatalf("res = %d, out:\n%s", res, out)
	}
}

func TestClosedPort(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	l.Close()
	res, out := run(t, "https://"+addr, pusher.TLSOptions{})
	if res != ResultConnect || !strings.Contains(out, "Porta ulaşılamıyor") {
		t.Fatalf("res = %d, out:\n%s", res, out)
	}
}

func TestUnresolvableName(t *testing.T) {
	res, out := run(t, "https://healthbeat-test.invalid:8443", pusher.TLSOptions{})
	if res != ResultDNS || !strings.Contains(out, "Adres çözülemedi: healthbeat-test.invalid") {
		t.Fatalf("res = %d, out:\n%s", res, out)
	}
}
