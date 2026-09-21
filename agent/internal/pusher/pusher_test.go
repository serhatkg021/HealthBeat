package pusher

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
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"healthbeat-agent/internal/collector"
	"healthbeat-agent/internal/version"
)

type captured struct {
	method, path, auth, hostID, contentType string
	userAgent, protocol                     string
	body                                    MetricsPayload
}

func fakeServer(t *testing.T, status int, reply string) (*httptest.Server, *captured) {
	t.Helper()
	got := &captured{}
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got.method, got.path = r.Method, r.URL.Path
		got.auth, got.hostID, got.contentType = r.Header.Get("Authorization"), r.Header.Get("X-Host-ID"), r.Header.Get("Content-Type")
		got.userAgent, got.protocol = r.Header.Get("User-Agent"), r.Header.Get(version.HeaderProtocol)
		json.NewDecoder(r.Body).Decode(&got.body)
		w.WriteHeader(status)
		io.WriteString(w, reply)
	}))
	t.Cleanup(srv.Close)
	return srv, got
}

var sample = MetricsPayload{
	CPUUsagePct: 12.5, RAMUsagePct: 60,
	Disk:             []DiskUsage{{Mount: "/", UsedPct: 40, Total: 100, Free: 60}},
	DockerContainers: []DockerContainer{{Name: "web", Image: "nginx", Status: "running", RestartCount: 2}},
}

func TestPushSendsAuthenticatedRequest(t *testing.T) {
	srv, got := fakeServer(t, http.StatusNoContent, "")
	p := New(srv.URL+"/", "agent-123", "secret-token", TLSOptions{InsecureSkipVerify: true}) // sondaki eğik çizgi ikilenmemeli

	if err := p.Push(context.Background(), sample); err != nil {
		t.Fatal(err)
	}
	if got.method != "POST" || got.path != "/api/v1/metrics" {
		t.Errorf("request = %s %s", got.method, got.path)
	}
	if got.auth != "Bearer secret-token" || got.hostID != "agent-123" || got.contentType != "application/json" {
		t.Errorf("headers: auth=%q agent=%q type=%q", got.auth, got.hostID, got.contentType)
	}
	if got.body.CPUUsagePct != 12.5 || len(got.body.Disk) != 1 || got.body.DockerContainers[0].RestartCount != 2 {
		t.Errorf("body = %+v", got.body)
	}
}

// Donanım toplamları 0 ("bilinmiyor") ise JSON'a hiç yazılmamalı; server son bilinen değeri korur.
func TestPayloadOmitsUnknownHardwareTotals(t *testing.T) {
	b, _ := json.Marshal(sample)
	if strings.Contains(string(b), "cpu_cores") || strings.Contains(string(b), "ram_total_mb") {
		t.Errorf("zero totals serialized: %s", b)
	}
	withTotals := sample
	withTotals.CPUCores, withTotals.RAMTotalMB = 8, 16000
	b, _ = json.Marshal(withTotals)
	if !strings.Contains(string(b), `"cpu_cores":8`) || !strings.Contains(string(b), `"ram_total_mb":16000`) {
		t.Errorf("totals missing: %s", b)
	}
}

func TestFromPhysicalDisksOmitsWhenUnknownAndSerializes(t *testing.T) {
	if got := FromPhysicalDisks(nil); got != nil {
		t.Fatalf("FromPhysicalDisks(nil) = %v, want nil", got)
	}
	p := sample
	b, _ := json.Marshal(p)
	if strings.Contains(string(b), "physical_disks") {
		t.Errorf("empty physical disks serialized: %s", b)
	}
	p.PhysicalDisks = FromPhysicalDisks([]collector.PhysicalDisk{
		{Name: "nvme0n1", Model: "CT500", SizeBytes: 500107862016, Kind: "nvme", Mounts: []string{"/", "/boot/efi"}},
	})
	b, _ = json.Marshal(p)
	for _, want := range []string{`"physical_disks":[{"name":"nvme0n1"`, `"size_bytes":500107862016`, `"kind":"nvme"`, `"mounts":["/","/boot/efi"]`} {
		if !strings.Contains(string(b), want) {
			t.Errorf("missing %s in %s", want, b)
		}
	}
}

// Sürüm bilgisi gövdede değil başlıkta gider: gövdedeki bilinmeyen bir alan, sözleşmeyi henüz
// bilmeyen eski bir server'da 400 üretirdi.
func TestPushSendsVersionHeadersAndNoVersionFieldInBody(t *testing.T) {
	srv, got := fakeServer(t, http.StatusNoContent, "")
	if err := New(srv.URL, "id", "tok", TLSOptions{InsecureSkipVerify: true}).Push(context.Background(), sample); err != nil {
		t.Fatal(err)
	}
	if got.userAgent != "healthbeat-agent/"+version.Version {
		t.Errorf("User-Agent = %q", got.userAgent)
	}
	if got.protocol != strconv.Itoa(version.Protocol) {
		t.Errorf("%s = %q, want %d", version.HeaderProtocol, got.protocol, version.Protocol)
	}
	b, _ := json.Marshal(sample)
	for _, banned := range []string{"version", "protocol", "agent"} {
		if strings.Contains(string(b), banned) {
			t.Errorf("payload body contains %q: %s", banned, b)
		}
	}
}

// Envanter (protokol 3) yalnızca toplanabildiyse gönderilir; inode alanı bilinmiyorsa disk girdisinde yoktur.
func TestPayloadOmitsHostInfoAndInodesWhenUnknown(t *testing.T) {
	b, _ := json.Marshal(sample)
	for _, banned := range []string{"host_info", "inodes_used_pct"} {
		if strings.Contains(string(b), banned) {
			t.Errorf("unknown %s serialized: %s", banned, b)
		}
	}
	inodes := 12.5
	p := sample
	p.Disk = []DiskUsage{{Mount: "/", UsedPct: 1, Total: 2, Free: 1, InodesUsedPct: &inodes}}
	p.HostInfo = &collector.HostInfo{Hostname: "h"}
	b, _ = json.Marshal(p)
	for _, want := range []string{`"host_info":{"hostname":"h"}`, `"inodes_used_pct":12.5`} {
		if !strings.Contains(string(b), want) {
			t.Errorf("missing %s in %s", want, b)
		}
	}
	// FromDiskUsages inode alanını taşır
	got := FromDiskUsages([]collector.DiskUsage{{Mount: "/", InodesUsedPct: &inodes}})
	if got[0].InodesUsedPct == nil || *got[0].InodesUsedPct != 12.5 {
		t.Errorf("FromDiskUsages dropped the inode pct: %+v", got)
	}
}

func TestPushSurfacesServerErrors(t *testing.T) {
	for _, tc := range []struct {
		status int
		reply  string
	}{
		{http.StatusUnauthorized, "invalid agent credentials"},
		{http.StatusTooManyRequests, "too many requests"},
		{http.StatusOK, ""}, // API sözleşmesi 204'tür; başka her şey bir sorundur
	} {
		srv, _ := fakeServer(t, tc.status, tc.reply)
		err := New(srv.URL, "id", "tok", TLSOptions{InsecureSkipVerify: true}).Push(context.Background(), sample)
		if err == nil {
			t.Errorf("status %d treated as success", tc.status)
			continue
		}
		if tc.reply != "" && !strings.Contains(err.Error(), tc.reply) {
			t.Errorf("status %d: error %q lacks the server's message", tc.status, err)
		}
	}
}

func TestPushRejectsUntrustedCertificateUnlessOptedIn(t *testing.T) {
	srv, _ := fakeServer(t, http.StatusNoContent, "") // httptest'in sertifikası kendinden imzalıdır

	if err := New(srv.URL, "id", "tok", TLSOptions{}).Push(context.Background(), sample); err == nil {
		t.Fatal("push succeeded against an untrusted certificate without insecure_skip_verify")
	}
	if err := New(srv.URL, "id", "tok", TLSOptions{InsecureSkipVerify: true}).Push(context.Background(), sample); err != nil {
		t.Fatalf("insecure_skip_verify=true should accept the self-signed cert: %v", err)
	}
}

func TestPushHonoursContextAndUnreachableServer(t *testing.T) {
	srv, _ := fakeServer(t, http.StatusNoContent, "")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := New(srv.URL, "id", "tok", TLSOptions{InsecureSkipVerify: true}).Push(ctx, sample); err == nil {
		t.Fatal("push ignored a cancelled context")
	}

	srv.Close()
	start := time.Now()
	if err := New(srv.URL, "id", "tok", TLSOptions{InsecureSkipVerify: true}).Push(context.Background(), sample); err == nil {
		t.Fatal("push to a closed server succeeded")
	}
	if time.Since(start) > 12*time.Second {
		t.Fatal("push did not respect its timeout")
	}
}

func TestConvertersPreserveEveryField(t *testing.T) {
	disks := FromDiskUsages([]collector.DiskUsage{{Mount: "/data", UsedPct: 91.5, Total: 1000, Free: 85}})
	if len(disks) != 1 || disks[0] != (DiskUsage{Mount: "/data", UsedPct: 91.5, Total: 1000, Free: 85}) {
		t.Errorf("disks = %+v", disks)
	}
	in := collector.DockerContainer{Name: "n", Image: "i", Status: "running", CPUPct: 1.5, RAMMB: 2.5, RestartCount: 3, UptimeSeconds: 4}
	out := FromDockerContainers([]collector.DockerContainer{in})
	want := DockerContainer{Name: "n", Image: "i", Status: "running", CPUPct: 1.5, RAMMB: 2.5, RestartCount: 3, UptimeSeconds: 4}
	if len(out) != 1 || out[0] != want {
		t.Errorf("containers = %+v, want %+v", out, want)
	}

	// null yerine [] olarak serileşmeli: server ikisini de çözer, ama panele bakan JSON biçimi
	// Docker'ın varlığına bağlı olmamalı.
	b, _ := json.Marshal(MetricsPayload{Disk: FromDiskUsages(nil), DockerContainers: FromDockerContainers(nil)})
	if !strings.Contains(string(b), `"disk":[]`) || !strings.Contains(string(b), `"docker_containers":[]`) {
		t.Errorf("empty collections marshal as %s", b)
	}
}

// ---- özel CA desteği ------------------------------------------------------------------------

// testCA, tek kullanımlık bir sertifika otoritesidir.
type testCA struct {
	cert *x509.Certificate
	key  *ecdsa.PrivateKey
	pem  []byte
}

func newTestCA(t *testing.T, cn string) *testCA {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()), Subject: pkix.Name{CommonName: cn},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(24 * time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	cert, _ := x509.ParseCertificate(der)
	return &testCA{cert: cert, key: key, pem: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})}
}

func (ca *testCA) pool() *x509.CertPool {
	p := x509.NewCertPool()
	p.AddCert(ca.cert)
	return p
}

// leaf, verilen IP'ler için notAfter'da sona eren bir server sertifikası üretir.
func (ca *testCA) leaf(t *testing.T, ips []net.IP, notAfter time.Time) tls.Certificate {
	t.Helper()
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()), Subject: pkix.Name{CommonName: "server"},
		NotBefore: time.Now().Add(-2 * time.Hour), NotAfter: notAfter, IPAddresses: ips,
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, ca.cert, &key.PublicKey, ca.key)
	if err != nil {
		t.Fatal(err)
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
}

func serverWith(t *testing.T, cert tls.Certificate) *httptest.Server {
	t.Helper()
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	srv.TLS = &tls.Config{Certificates: []tls.Certificate{cert}}
	srv.StartTLS()
	t.Cleanup(srv.Close)
	return srv
}

func TestPushVerifiesTheServerAgainstAPrivateCA(t *testing.T) {
	ca := newTestCA(t, "Corp Root CA")
	loopback := []net.IP{net.ParseIP("127.0.0.1")}
	push := func(srv *httptest.Server, opts TLSOptions) error {
		return New(srv.URL, "id", "tok", opts).Push(context.Background(), sample)
	}

	good := serverWith(t, ca.leaf(t, loopback, time.Now().Add(time.Hour)))
	if err := push(good, TLSOptions{RootCAs: ca.pool()}); err != nil {
		t.Fatalf("a server certificate signed by the configured CA was rejected: %v", err)
	}

	// CA olmadan özel sertifika (haklı olarak) güvenilmezdir...
	if err := push(good, TLSOptions{}); err == nil {
		t.Error("a private-CA certificate was accepted against the system roots")
	}
	// ...ve YALNIZCA yapılandırılan CA'ya güvenilir: başka bir CA'nın server'ı reddedilir.
	other := newTestCA(t, "Some Other CA")
	stranger := serverWith(t, other.leaf(t, loopback, time.Now().Add(time.Hour)))
	if err := push(stranger, TLSOptions{RootCAs: ca.pool()}); err == nil {
		t.Error("a certificate from a CA that is not the configured one was accepted")
	}
	// Ad hâlâ denetlenir: CA doğru ama sertifika bu adres için geçerli değil.
	wrongName := serverWith(t, ca.leaf(t, []net.IP{net.ParseIP("192.0.2.77")}, time.Now().Add(time.Hour)))
	if err := push(wrongName, TLSOptions{RootCAs: ca.pool()}); err == nil {
		t.Error("a certificate for a different IP was accepted (the CA alone must not be enough)")
	}
	// Süre sonu hâlâ denetlenir.
	expired := serverWith(t, ca.leaf(t, loopback, time.Now().Add(-time.Minute)))
	if err := push(expired, TLSOptions{RootCAs: ca.pool()}); err == nil {
		t.Error("an expired certificate was accepted")
	}
	// Geliştirme için kaçış kapısı hâlâ var.
	if err := push(stranger, TLSOptions{InsecureSkipVerify: true}); err != nil {
		t.Errorf("insecure_skip_verify no longer works: %v", err)
	}
}
