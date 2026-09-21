package pullserver

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"healthbeat-agent/internal/config"
	"healthbeat-agent/internal/pusher"
	"healthbeat-agent/internal/version"
)

const secret = "pull-shared-secret"

func newServer(t *testing.T, allowed ...string) *Server {
	t.Helper()
	s, err := New(&config.Config{
		Mode: config.ModePull, ListenAddr: "127.0.0.1:0", PullEndpoint: "/api/v1/status",
		PullSecret: secret, AllowedServerIPs: allowed, DiskMounts: []string{"/"},
	})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func get(s *Server, remote, path string, headers map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest("GET", path, nil)
	req.RemoteAddr = remote
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	s.httpServer.Handler.ServeHTTP(rec, req)
	return rec
}

var bearer = map[string]string{"Authorization": "Bearer " + secret}

func TestNewRejectsInvalidWhitelistEntries(t *testing.T) {
	for _, bad := range []string{"not-an-ip", "10.0.0.256", "10.0.0.0/33", "10.0.0.0/", ""} {
		_, err := New(&config.Config{PullEndpoint: "/s", AllowedServerIPs: []string{bad}})
		if err == nil {
			t.Errorf("allowed_server_ips entry %q was accepted", bad)
		}
	}
}

func TestIsAllowed(t *testing.T) {
	s := newServer(t, "10.0.0.5", "192.168.1.0/24", "2001:db8::1", "fd00::/8")
	for remote, want := range map[string]bool{
		"10.0.0.5:5000":        true,
		"10.0.0.6:5000":        false,
		"192.168.1.77:1":       true,
		"192.168.2.1:1":        false,
		"[2001:db8::1]:443":    true,
		"[2001:db8::2]:443":    false,
		"[fd12:3456::1]:443":   true,
		"[::ffff:10.0.0.5]:80": true, // IPv4-eşlemeli IPv6, IPv4 girdisiyle eşleşmeli
		"10.0.0.5":             true, // port yok
		"":                     false,
		"garbage":              false,
		"localhost:80":         false, // host adlarına asla güvenilmez
		"[::1]:80":             false,
	} {
		if got := s.isAllowed(remote); got != want {
			t.Errorf("isAllowed(%q) = %v, want %v", remote, got, want)
		}
	}
}

func TestHandleStatusAuthorization(t *testing.T) {
	s := newServer(t, "10.0.0.5")

	cases := []struct {
		name    string
		remote  string
		headers map[string]string
		want    int
	}{
		{"whitelisted + secret", "10.0.0.5:1", bearer, 200},
		{"whitelisted, no secret", "10.0.0.5:1", nil, 401},
		{"whitelisted, wrong secret", "10.0.0.5:1", map[string]string{"Authorization": "Bearer nope"}, 401},
		{"whitelisted, prefix of secret", "10.0.0.5:1", map[string]string{"Authorization": "Bearer pull"}, 401},
		{"whitelisted, secret plus junk", "10.0.0.5:1", map[string]string{"Authorization": "Bearer " + secret + "x"}, 401},
		{"whitelisted, same-length wrong secret", "10.0.0.5:1", map[string]string{"Authorization": "Bearer " + strings.Repeat("x", len(secret))}, 401},
		{"whitelisted, one char off", "10.0.0.5:1", map[string]string{"Authorization": "Bearer " + secret[:len(secret)-1] + "X"}, 401},
		{"whitelisted, wrong scheme", "10.0.0.5:1", map[string]string{"Authorization": "Basic " + secret}, 401},
		{"whitelisted, bare secret", "10.0.0.5:1", map[string]string{"Authorization": secret}, 401},
		{"right secret, wrong IP", "10.9.9.9:1", bearer, 403},
		{"no secret, wrong IP", "10.9.9.9:1", nil, 403}, // önce IP denetlenir: secret hakkında hiçbir şey ele vermez
	}
	for _, c := range cases {
		if got := get(s, c.remote, "/api/v1/status", c.headers).Code; got != c.want {
			t.Errorf("%s: status %d, want %d", c.name, got, c.want)
		}
	}
}

func TestHandleStatusResponseShape(t *testing.T) {
	s := newServer(t, "10.0.0.5")
	rec := get(s, "10.0.0.5:1", "/api/v1/status", bearer)
	if rec.Code != 200 || rec.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("status %d, content-type %q", rec.Code, rec.Header().Get("Content-Type"))
	}

	// Sürüm bilgisi yanıt başlıklarında gider (gövdede değil).
	if got := rec.Header().Get(version.HeaderAgentVersion); got != version.Version {
		t.Errorf("%s = %q, want %q", version.HeaderAgentVersion, got, version.Version)
	}
	if got := rec.Header().Get(version.HeaderProtocol); got != strconv.Itoa(version.Protocol) {
		t.Errorf("%s = %q, want %d", version.HeaderProtocol, got, version.Protocol)
	}

	var p pusher.MetricsPayload
	if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil {
		t.Fatalf("body is not a MetricsPayload: %v\n%s", err, rec.Body.String())
	}
	if p.CPUUsagePct < 0 || p.CPUUsagePct > 100 || p.RAMUsagePct < 0 || p.RAMUsagePct > 100 {
		t.Errorf("out-of-range reading: cpu=%v ram=%v", p.CPUUsagePct, p.RAMUsagePct)
	}
	if len(p.Disk) != 1 || p.Disk[0].Mount != "/" {
		t.Errorf("disk = %+v, want the configured mount", p.Disk)
	}
	if p.DockerContainers == nil {
		t.Error("docker_containers must be an array, not null, even without Docker")
	}
	// Donanım özeti pull yanıtında da bulunmalı (push ile aynı payload).
	if p.CPUCores < 1 || p.RAMTotalMB <= 0 {
		t.Errorf("hardware totals missing: cpu_cores=%d ram_total_mb=%d", p.CPUCores, p.RAMTotalMB)
	}
	// Makine envanteri (protokol 3) pull yanıtında da bulunmalı; çekirdek sürümü her Linux'ta okunur.
	if p.HostInfo == nil || p.HostInfo.Kernel == nil || p.HostInfo.Kernel.Release == "" {
		t.Errorf("host_info missing from the pull response: %+v", p.HostInfo)
	}
	// Fiziksel diskler ortama bağlıdır (konteynerde hiç olmayabilir); varsa yalnızca yapılandırılmış
	// mount'u içermeli.
	for _, d := range p.PhysicalDisks {
		if d.Name == "" || len(d.Mounts) != 1 || d.Mounts[0] != "/" {
			t.Errorf("physical disk %+v, want a named disk holding only the configured mount /", d)
		}
	}
}

func TestOnlyGETOnTheConfiguredEndpoint(t *testing.T) {
	s := newServer(t, "10.0.0.5")
	for _, method := range []string{"POST", "PUT", "DELETE"} {
		req := httptest.NewRequest(method, "/api/v1/status", nil)
		req.RemoteAddr = "10.0.0.5:1"
		req.Header.Set("Authorization", "Bearer "+secret)
		rec := httptest.NewRecorder()
		s.httpServer.Handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusMethodNotAllowed {
			t.Errorf("%s: status %d, want 405", method, rec.Code)
		}
	}
	if got := get(s, "10.0.0.5:1", "/api/v1/other", bearer).Code; got != 404 {
		t.Errorf("other path: status %d, want 404", got)
	}
}

func selfSigned(t *testing.T) (certFile, keyFile string) {
	t.Helper()
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "pull-agent"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		IPAddresses: []net.IP{net.ParseIP("127.0.0.1")},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, _ := x509.MarshalECPrivateKey(key)
	dir := t.TempDir()
	certFile, keyFile = filepath.Join(dir, "c.pem"), filepath.Join(dir, "k.pem")
	os.WriteFile(certFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600)
	os.WriteFile(keyFile, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}), 0o600)
	return
}

// Gerçek bir TLS dinleyicisi üzerinden uçtan uca; agent 127.0.0.1'den bağlanıyor.
func TestServesOverTLSOnly(t *testing.T) {
	certFile, keyFile := selfSigned(t)
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	l.Close() // portu server için serbest bırak (küçük bir yarış, testte kabul edilebilir)

	start := func(allowed string) *Server {
		s, err := New(&config.Config{
			Mode: config.ModePull, ListenAddr: addr, PullEndpoint: "/api/v1/status", PullSecret: secret,
			AllowedServerIPs: []string{allowed}, TLSCertFile: certFile, TLSKeyFile: keyFile, DiskMounts: []string{"/"},
		})
		if err != nil {
			t.Fatal(err)
		}
		go s.ListenAndServe()
		t.Cleanup(func() { s.Shutdown(context.Background()) })
		return s
	}

	insecure := &http.Client{Timeout: 3 * time.Second, Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}}
	do := func(scheme string) (int, error) {
		req, _ := http.NewRequest("GET", scheme+"://"+addr+"/api/v1/status", nil)
		req.Header.Set("Authorization", "Bearer "+secret)
		var resp *http.Response
		var err error
		for i := 0; i < 50; i++ { // dinleyiciyi bekle
			if resp, err = insecure.Do(req); err == nil || scheme == "http" {
				break
			}
			time.Sleep(20 * time.Millisecond)
		}
		if err != nil {
			return 0, err
		}
		resp.Body.Close()
		return resp.StatusCode, nil
	}

	s := start("127.0.0.1")
	if code, err := do("https"); err != nil || code != 200 {
		t.Fatalf("https from a whitelisted IP: %d, %v", code, err)
	}
	// TLS portuna düz HTTP kullanılabilir bir yanıt vermemeli.
	if code, err := do("http"); err == nil && code == 200 {
		t.Fatal("served metrics over plain HTTP")
	}
	s.Shutdown(context.Background())

	// Aynı dinleyici, ama arayan (127.0.0.1) izin listesinde değil: bağlantı kabul anında,
	// herhangi bir TLS ya da HTTP başlamadan bırakılır.
	time.Sleep(100 * time.Millisecond)
	start("10.0.0.99")
	if code, err := do("https"); err == nil {
		t.Fatalf("https from a non-whitelisted IP got HTTP %d; the connection should be dropped", code)
	}
}

func TestAllowListenerDropsStrangersBeforeAnyProtocolWork(t *testing.T) {
	inner, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer inner.Close()
	l := &allowListener{Listener: inner, allowed: func(string) bool { return false }}

	accepted := make(chan net.Conn, 1)
	go func() {
		if c, err := l.Accept(); err == nil {
			accepted <- c
		}
	}()

	conn, err := net.Dial("tcp", inner.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	if _, err := conn.Read(make([]byte, 1)); err == nil {
		t.Fatal("read data from a connection that should have been closed")
	} else if ne, ok := err.(net.Error); ok && ne.Timeout() {
		t.Fatal("stranger's connection was left open")
	}
	select {
	case <-accepted:
		t.Fatal("Accept returned a connection from a non-whitelisted address")
	default:
	}
}

// Bağlanıp sonra takılan (TLS el sıkışması yapmayan) izinli bir karşı taraf, sonsuza dek
// açık tutulmak yerine okuma zaman aşımıyla kesilmeli.
func TestStalledHandshakeIsClosed(t *testing.T) {
	certFile, keyFile := selfSigned(t)
	l, _ := net.Listen("tcp", "127.0.0.1:0")
	addr := l.Addr().String()
	l.Close()

	s, err := New(&config.Config{
		Mode: config.ModePull, ListenAddr: addr, PullEndpoint: "/api/v1/status", PullSecret: secret,
		AllowedServerIPs: []string{"127.0.0.1"}, TLSCertFile: certFile, TLSKeyFile: keyFile, DiskMounts: []string{"/"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if s.httpServer.ReadTimeout <= 0 || s.httpServer.ReadHeaderTimeout <= 0 || s.httpServer.WriteTimeout <= 0 || s.httpServer.IdleTimeout <= 0 {
		t.Fatalf("server timeouts not all set: %+v", s.httpServer)
	}
	s.httpServer.ReadTimeout = 300 * time.Millisecond // test için kısaltıldı
	go s.ListenAndServe()
	t.Cleanup(func() { s.Shutdown(context.Background()) })

	var conn net.Conn
	for i := 0; i < 50; i++ {
		if conn, err = net.Dial("tcp", addr); err == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	start := time.Now()
	_, err = conn.Read(make([]byte, 1)) // hiçbir şey göndermiyoruz; server bağlantıyı kapatmalı
	if ne, ok := err.(net.Error); ok && ne.Timeout() {
		t.Fatalf("server held a stalled connection for %v", time.Since(start))
	}
}
