package pullscheduler

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"fmt"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"healthbeat-server/internal/alertengine"
	"healthbeat-server/internal/model"
	"healthbeat-server/internal/notify"
	"healthbeat-server/internal/secretbox"
	"healthbeat-server/internal/store"
	"healthbeat-server/internal/testdb"
	"healthbeat-server/internal/version"
)

const hostSecret = "the-pull-secret"

// fakeAgent, sahte bir pull modu agent'tır: scheduler'ın poll ettiği bir HTTPS server.
type fakeAgent struct {
	*httptest.Server
	requests atomic.Int32
	lastAuth atomic.Value // string
	handler  atomic.Value // http.HandlerFunc
}

// newFakeAgent, httptest'in kendinden imzalı sertifikasıyla ya da verilmişse cert ile
// TLS üzerinden sunar.
func newFakeAgent(t *testing.T, h http.HandlerFunc, cert *tls.Certificate) *fakeAgent {
	t.Helper()
	fc := &fakeAgent{}
	fc.handler.Store(h)
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fc.requests.Add(1)
		fc.lastAuth.Store(r.Header.Get("Authorization"))
		fc.handler.Load().(http.HandlerFunc)(w, r)
	})
	if cert == nil {
		fc.Server = httptest.NewTLSServer(inner)
	} else {
		fc.Server = httptest.NewUnstartedServer(inner)
		fc.Server.TLS = &tls.Config{Certificates: []tls.Certificate{*cert}}
		fc.Server.StartTLS()
	}
	t.Cleanup(fc.Server.Close)
	return fc
}

func report(cpu, ram float64) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(model.MetricsIngestRequest{
			CPUUsagePct: cpu, RAMUsagePct: ram,
			Disk:             []model.DiskUsage{{Mount: "/", UsedPct: 10, Total: 100, Free: 90}},
			DockerContainers: []model.DockerContainerReport{{Name: "web", Image: "nginx", Status: "running"}},
		})
	}
}

type env struct {
	t      *testing.T
	ctx    context.Context
	pool   *pgxpool.Pool
	s      *Scheduler
	org    uuid.UUID
	host   model.Host
	fake   *fakeAgent
	engine *alertengine.Engine
}

func newEnv(t *testing.T, interval int, h http.HandlerFunc) *env {
	t.Helper()
	return newEnvTLS(t, interval, h, nil, nil)
}

// newEnvTLS, sahte agent'ın cert sunduğu (nil = kendinden imzalı) ve scheduler'ın rootCAs'a
// karşı doğruladığı (nil = doğrulama kapalı, varsayılan) newEnv'dir.
func newEnvTLS(t *testing.T, interval int, h http.HandlerFunc, cert *tls.Certificate, rootCAs *x509.CertPool) *env {
	t.Helper()
	pool := testdb.New(t)
	box := testdb.SecretBox(t)
	fake := newFakeAgent(t, h, cert)

	_, portStr, _ := net.SplitHostPort(strings.TrimPrefix(fake.URL, "https://"))
	port, _ := strconv.Atoi(portStr)

	org := testdb.Org(t, pool, "acme")
	secret := hostSecret
	host, err := store.NewHosts(pool, box).Create(context.Background(), store.CreateHostParams{
		OrganizationID: org, Title: "pull-1", IP: "127.0.0.1", Mode: "pull", IntervalSeconds: interval,
		PullPort: &port, PullEndpoint: ptr("/api/v1/status"), PullSecret: &secret,
	})
	if err != nil {
		t.Fatal(err)
	}

	engine := alertengine.New(pool, notify.New(notify.Config{}), "")
	return &env{t: t, ctx: context.Background(), pool: pool, s: New(pool, engine, box, rootCAs), org: org, host: host, fake: fake, engine: engine}
}

func ptr[T any](v T) *T { return &v }

// poll, bir scheduler tick'i çalıştırır ve başlattığı poll'ları bekler.
func (e *env) poll() {
	e.t.Helper()
	e.s.pollDueHosts(e.ctx)
	e.s.wg.Wait()
}

// makeDue, host'ın interval'inin dolduğunu varsayar.
func (e *env) makeDue() {
	e.s.mu.Lock()
	e.s.lastPolled[e.host.ID] = time.Now().Add(-time.Hour)
	e.s.mu.Unlock()
}

func (e *env) count(query string, args ...any) int {
	e.t.Helper()
	var n int
	if err := e.pool.QueryRow(e.ctx, query, args...).Scan(&n); err != nil {
		e.t.Fatal(err)
	}
	return n
}

func (e *env) metricRows() int {
	return e.count(`SELECT count(*) FROM metrics WHERE host_id = $1`, e.host.ID)
}

func (e *env) status() string {
	var st string
	if err := e.pool.QueryRow(e.ctx, `SELECT status FROM host_status WHERE host_id = $1`, e.host.ID).Scan(&st); err != nil {
		e.t.Fatal(err)
	}
	return st
}

func TestPollStoresReportAuthenticatesAndMarksOnline(t *testing.T) {
	e := newEnv(t, 10, report(42, 55))
	if e.status() != "offline" {
		t.Fatal("precondition: a new host starts offline")
	}

	e.poll()

	if got := e.fake.lastAuth.Load(); got != "Bearer "+hostSecret {
		t.Fatalf("scheduler sent Authorization %q, want the decrypted secret", got)
	}
	if e.metricRows() != 1 {
		t.Fatalf("metric rows = %d, want 1", e.metricRows())
	}
	var cpu, ram float64
	e.pool.QueryRow(e.ctx, `SELECT cpu_usage_pct, ram_usage_pct FROM metrics WHERE host_id = $1`, e.host.ID).Scan(&cpu, &ram)
	if cpu != 42 || ram != 55 {
		t.Fatalf("stored cpu/ram = %v/%v, want 42/55", cpu, ram)
	}
	if n := e.count(`SELECT count(*) FROM docker_containers WHERE host_id = $1 AND name = 'web'`, e.host.ID); n != 1 {
		t.Fatalf("docker container rows = %d, want 1", n)
	}
	if e.status() != "online" {
		t.Fatal("host not marked online after a successful poll")
	}
	if n := e.count(`SELECT count(*) FROM host_status WHERE host_id = $1 AND last_seen IS NOT NULL`, e.host.ID); n != 1 {
		t.Fatal("last_seen not set")
	}
}

func TestPolledReportsDriveAlerts(t *testing.T) {
	e := newEnv(t, 10, report(95, 10))
	testdb.Threshold(t, e.pool, nil, nil, "cpu", 50, 90)
	alerts := store.NewAlerts(e.pool)

	e.poll()
	open, err := alerts.GetActive(e.ctx, e.host.ID, "cpu")
	if err != nil || open.Level != model.AlertLevelCritical {
		t.Fatalf("open cpu alert = %+v, err = %v; want critical", open, err)
	}

	e.fake.handler.Store(report(10, 10))
	e.makeDue()
	e.poll()
	got, _ := alerts.GetByID(e.ctx, open.ID)
	if got.Status != model.AlertStatusResolved {
		t.Fatalf("alert status = %q after recovery, want resolved", got.Status)
	}
}

func TestSuccessfulPollResolvesOfflineAlert(t *testing.T) {
	e := newEnv(t, 10, report(1, 1))
	e.engine.RaiseOffline(e.ctx, e.host.ID, e.org)
	if _, err := store.NewAlerts(e.pool).GetActive(e.ctx, e.host.ID, model.AlertTypeHostOffline); err != nil {
		t.Fatalf("precondition: offline alert should be open: %v", err)
	}

	e.poll()
	if _, err := store.NewAlerts(e.pool).GetActive(e.ctx, e.host.ID, model.AlertTypeHostOffline); err == nil {
		t.Fatal("offline alert still open after the host answered")
	}
}

func TestRespectsIntervalBetweenPolls(t *testing.T) {
	e := newEnv(t, 3600, report(1, 1))
	e.poll()
	e.poll() // hemen yeniden: interval dolmadı
	if n := e.fake.requests.Load(); n != 1 {
		t.Fatalf("host polled %d times within its interval, want 1", n)
	}

	e.makeDue()
	e.poll()
	if n := e.fake.requests.Load(); n != 2 {
		t.Fatalf("host polled %d times after the interval elapsed, want 2", n)
	}
}

func TestBadResponsesStoreNothing(t *testing.T) {
	huge := func(w http.ResponseWriter, r *http.Request) {
		// Geçerli bir JSON öneki, sonra scheduler'ın asla okuyacağından çok daha fazlası.
		fmt.Fprint(w, `{"cpu_usage_pct":1,"ram_usage_pct":1,"pad":"`)
		chunk := strings.Repeat("A", 64<<10)
		for i := 0; i < 40; i++ { // ~2,5 MiB, 1 MiB sınırının üstünde
			if _, err := fmt.Fprint(w, chunk); err != nil {
				return
			}
		}
		fmt.Fprint(w, `"}`)
	}

	for name, h := range map[string]http.HandlerFunc{
		"server error":       func(w http.ResponseWriter, r *http.Request) { http.Error(w, "boom", 500) },
		"wrong secret (401)": func(w http.ResponseWriter, r *http.Request) { http.Error(w, "unauthorized", 401) },
		"not json":           func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, "<html>hi</html>") },
		"empty body":         func(w http.ResponseWriter, r *http.Request) {},
		"cpu out of range":   report(150, 10),
		"ram out of range":   report(10, -5),
		"oversized body":     huge,
		"truncated json":     func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, `{"cpu_usage_pct":1,`) },
	} {
		t.Run(name, func(t *testing.T) {
			e := newEnv(t, 10, h)
			e.poll()
			if e.fake.requests.Load() != 1 {
				t.Fatal("host was not polled")
			}
			if e.metricRows() != 0 {
				t.Fatalf("%d metric row(s) stored from a bad response", e.metricRows())
			}
			if e.status() != "offline" {
				t.Fatal("a bad response marked the host online")
			}
		})
	}
}

func TestUnreachableHostIsToleratedAndRetried(t *testing.T) {
	e := newEnv(t, 10, report(1, 1))
	e.fake.Close()

	e.poll() // panik yapmamalı ya da takılmamalı
	if e.metricRows() != 0 || e.status() != "offline" {
		t.Fatal("state changed after polling an unreachable host")
	}

	e.makeDue()
	e.poll() // ve sonra yeniden denenir
}

func TestNoOverlappingPollsOfTheSameHost(t *testing.T) {
	release := make(chan struct{})
	var once sync.Once
	e := newEnv(t, 1, func(w http.ResponseWriter, r *http.Request) {
		<-release
		report(1, 1)(w, r)
	})
	t.Cleanup(func() { once.Do(func() { close(release) }) })

	e.s.pollDueHosts(e.ctx) // sahte host'ta bloklanan bir poll başlatır
	deadline := time.Now().Add(3 * time.Second)
	for e.fake.requests.Load() < 1 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}

	for i := 0; i < 3; i++ { // interval doldu ama önceki poll hâlâ çalışıyor
		e.makeDue()
		e.s.pollDueHosts(e.ctx)
	}
	time.Sleep(100 * time.Millisecond)
	if n := e.fake.requests.Load(); n != 1 {
		t.Fatalf("%d polls in flight for one host, want 1", n)
	}

	once.Do(func() { close(release) })
	e.s.wg.Wait()

	e.makeDue()
	e.poll() // ilk poll bitince host yeniden uygun olur
	if n := e.fake.requests.Load(); n != 2 {
		t.Fatalf("polls = %d after the first finished, want 2", n)
	}
}

func TestForgetsDeletedHosts(t *testing.T) {
	e := newEnv(t, 10, report(1, 1))
	e.poll()
	e.s.mu.Lock()
	tracked := len(e.s.lastPolled)
	e.s.mu.Unlock()
	if tracked != 1 {
		t.Fatalf("tracked = %d, want 1", tracked)
	}

	if _, err := e.pool.Exec(e.ctx, `DELETE FROM hosts WHERE id = $1`, e.host.ID); err != nil {
		t.Fatal(err)
	}
	e.poll()
	e.s.mu.Lock()
	tracked = len(e.s.lastPolled)
	e.s.mu.Unlock()
	if tracked != 0 {
		t.Fatalf("scheduler still tracks %d deleted host(s)", tracked)
	}
}

func TestHostWithUndecryptableSecretIsNotPolled(t *testing.T) {
	e := newEnv(t, 10, report(1, 1))
	otherKey, _ := secretbox.New([]byte("ffffffffffffffffffffffffffffffff"))
	e.s = New(e.pool, e.engine, otherKey, nil)

	e.poll()
	if n := e.fake.requests.Load(); n != 0 {
		t.Fatalf("polled %d times with a secret that cannot be decrypted", n)
	}
}

func TestRunStopsOnContextCancel(t *testing.T) {
	e := newEnv(t, 10, report(1, 1))
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { e.s.Run(ctx); close(done) }()
	cancel()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("Run did not return after its context was cancelled")
	}
}

func TestPolledContainersDriveRestartAlerts(t *testing.T) {
	e := newEnv(t, 10, func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(model.MetricsIngestRequest{
			CPUUsagePct: 1, RAMUsagePct: 1,
			DockerContainers: []model.DockerContainerReport{
				{Name: "web", Image: "nginx", Status: "running", RestartCount: 7},
				{Name: "db", Image: "pg", Status: "running", RestartCount: 0},
			},
		})
	})
	testdb.Threshold(t, e.pool, nil, nil, "docker_restart", 3, 10)

	e.poll()
	open, err := store.NewAlerts(e.pool).ListActive(e.ctx, e.host.ID, model.MetricTypeDockerRestart)
	if err != nil || len(open) != 1 || open[0].Subject != "web" || open[0].Level != model.AlertLevelWarning {
		t.Fatalf("open docker_restart alerts = %+v err=%v, want a single warning for web", open, err)
	}
}

// ---- poll edilen agent'ın sertifikasını özel bir CA'ya karşı doğrulamak -------------------------------

type testCA struct {
	cert *x509.Certificate
	key  *ecdsa.PrivateKey
}

func newTestCA(t *testing.T) *testCA {
	t.Helper()
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()), Subject: pkix.Name{CommonName: "Corp Root CA"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(24 * time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	cert, _ := x509.ParseCertificate(der)
	return &testCA{cert: cert, key: key}
}

func (ca *testCA) pool() *x509.CertPool {
	p := x509.NewCertPool()
	p.AddCert(ca.cert)
	return p
}

func (ca *testCA) leaf(t *testing.T, ips []net.IP, notAfter time.Time) *tls.Certificate {
	t.Helper()
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()), Subject: pkix.Name{CommonName: "agent"},
		NotBefore: time.Now().Add(-2 * time.Hour), NotAfter: notAfter, IPAddresses: ips,
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, ca.cert, &key.PublicKey, ca.key)
	if err != nil {
		t.Fatal(err)
	}
	return &tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
}

func TestPollVerifiesAgentCertificatesAgainstThePrivateCA(t *testing.T) {
	loopback := []net.IP{net.ParseIP("127.0.0.1")}
	ca := newTestCA(t)
	valid := time.Now().Add(time.Hour)

	type tc struct {
		name    string
		cert    *tls.Certificate
		roots   *x509.CertPool
		wantHit bool // rapor saklanıyor mu?
	}
	other := newTestCA(t)
	for _, c := range []tc{
		{"agent certificate signed by the configured CA", ca.leaf(t, loopback, valid), ca.pool(), true},
		{"agent certificate from a different CA", other.leaf(t, loopback, valid), ca.pool(), false},
		{"right CA but not valid for the agent's IP", ca.leaf(t, []net.IP{net.ParseIP("192.0.2.9")}, valid), ca.pool(), false},
		{"right CA but expired", ca.leaf(t, loopback, time.Now().Add(-time.Minute)), ca.pool(), false},
		{"self-signed agent while a CA is configured", nil, ca.pool(), false},
		{"self-signed agent with verification off (the default)", nil, nil, true},
		{"private-CA agent with verification off (the default)", ca.leaf(t, loopback, valid), nil, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			e := newEnvTLS(t, 10, report(42, 55), c.cert, c.roots)
			e.poll()
			got := e.metricRows()
			if c.wantHit && (got != 1 || e.status() != "online") {
				t.Fatalf("report not stored (rows=%d status=%s)", got, e.status())
			}
			if !c.wantHit {
				if got != 0 || e.status() != "offline" {
					t.Fatalf("data was accepted from an agent that failed verification (rows=%d status=%s)", got, e.status())
				}
				if e.fake.lastAuth.Load() != nil {
					t.Fatalf("the shared secret was sent to an agent that failed verification (%v)", e.fake.lastAuth.Load())
				}
			}
		})
	}
}

// Çekilen raporun donanım bölümü (çekirdek, RAM, fiziksel diskler) push ile aynı yoldan hosts'a
// yazılır; sonraki, alanları taşımayan bir rapor eski değeri silmez.
func TestPollStoresHardwareAsLastKnownValue(t *testing.T) {
	withHardware := true
	e := newEnv(t, 10, func(w http.ResponseWriter, r *http.Request) {
		req := model.MetricsIngestRequest{
			CPUUsagePct: 10, RAMUsagePct: 20,
			Disk: []model.DiskUsage{{Mount: "/", UsedPct: 10, Total: 100, Free: 90}},
		}
		if withHardware {
			req.CPUCores, req.RAMTotalMB = 8, 16000
			req.PhysicalDisks = []model.PhysicalDisk{{Name: "sda", Model: "M", SizeBytes: 1000, Kind: "ssd", Mounts: []string{"/"}}}
		}
		json.NewEncoder(w).Encode(req)
	})

	read := func() model.Host {
		t.Helper()
		c, err := store.NewHosts(e.pool, testdb.SecretBox(t)).GetByID(e.ctx, e.host.ID)
		if err != nil {
			t.Fatal(err)
		}
		return c
	}

	e.poll()
	c := read()
	if c.CPUCores == nil || *c.CPUCores != 8 || c.RAMTotalMB == nil || *c.RAMTotalMB != 16000 ||
		len(c.PhysicalDisks) != 1 || c.PhysicalDisks[0].Name != "sda" || c.PhysicalDisks[0].Mounts[0] != "/" {
		t.Fatalf("after first poll = cores %v ram %v disks %+v", c.CPUCores, c.RAMTotalMB, c.PhysicalDisks)
	}

	withHardware = false
	e.makeDue()
	e.poll()
	c = read()
	if c.CPUCores == nil || *c.CPUCores != 8 || len(c.PhysicalDisks) != 1 {
		t.Fatalf("after poll without hardware = cores %v disks %+v, want last known kept", c.CPUCores, c.PhysicalDisks)
	}
}

// Pull'da agent sürümü yanıt başlıklarından okunur; server da kendi sürümünü isteğin
// başlıklarında bildirir (bkz. docs/COMPATIBILITY.md).
func TestPollRecordsAgentVersionAndAnnouncesServerVersion(t *testing.T) {
	var sawUA, sawServerVersion atomic.Value
	sendHeaders := true
	e := newEnv(t, 10, func(w http.ResponseWriter, r *http.Request) {
		sawUA.Store(r.Header.Get("User-Agent"))
		sawServerVersion.Store(r.Header.Get(version.HeaderServerVersion))
		if sendHeaders {
			w.Header().Set(version.HeaderAgentVersion, "1.1.0")
			w.Header().Set(version.HeaderProtocol, "2")
		}
		report(10, 20)(w, r)
	})

	read := func() model.Host {
		t.Helper()
		c, err := store.NewHosts(e.pool, testdb.SecretBox(t)).GetByID(e.ctx, e.host.ID)
		if err != nil {
			t.Fatal(err)
		}
		return c
	}

	e.poll()
	c := read()
	if c.AgentVersion == nil || *c.AgentVersion != "1.1.0" || c.AgentProtocol == nil || *c.AgentProtocol != 2 {
		t.Fatalf("after versioned poll = %v / %v", c.AgentVersion, c.AgentProtocol)
	}
	if got, _ := sawUA.Load().(string); got != version.UserAgent() {
		t.Errorf("User-Agent sent to host = %q, want %q", got, version.UserAgent())
	}
	if got, _ := sawServerVersion.Load().(string); got != version.Version {
		t.Errorf("%s sent to host = %q, want %q", version.HeaderServerVersion, got, version.Version)
	}

	// Başlık göndermeyen eski pull agent'ı: sürüm silinir, protokol 1.
	sendHeaders = false
	e.makeDue()
	e.poll()
	c = read()
	if c.AgentVersion != nil || c.AgentProtocol == nil || *c.AgentProtocol != model.LegacyProtocol {
		t.Fatalf("after legacy poll = %v / %v, want nil / 1", c.AgentVersion, c.AgentProtocol)
	}
}

// Pull yanıtındaki server'ın tanımadığı alanlar da kaydedilir (push ile aynı kural).
func TestPollRecordsUnsupportedFieldNames(t *testing.T) {
	future := true
	e := newEnv(t, 10, func(w http.ResponseWriter, r *http.Request) {
		body := map[string]any{
			"cpu_usage_pct": 10, "ram_usage_pct": 20,
			"disk":              []model.DiskUsage{{Mount: "/", UsedPct: 10, Total: 100, Free: 90}},
			"docker_containers": []any{},
		}
		if future {
			body["gpu"] = []any{}
			body["load_average"] = map[string]any{"1m": 0.5}
		}
		json.NewEncoder(w).Encode(body)
	})
	read := func() model.Host {
		t.Helper()
		c, err := store.NewHosts(e.pool, testdb.SecretBox(t)).GetByID(e.ctx, e.host.ID)
		if err != nil {
			t.Fatal(err)
		}
		return c
	}

	e.poll()
	if got := read().UnsupportedFields; len(got) != 2 || got[0] != "gpu" || got[1] != "load_average" {
		t.Fatalf("after future poll = %v, want [gpu load_average]", got)
	}
	future = false
	e.makeDue()
	e.poll()
	if got := read().UnsupportedFields; got != nil {
		t.Fatalf("after a poll without unknown fields = %v, want cleared", got)
	}
}

// Çekilen raporun host_info bölümü push ile aynı yoldan (temizlenip) hosts'a yazılır.
func TestPollStoresHostInfo(t *testing.T) {
	e := newEnv(t, 10, func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"cpu_usage_pct": 10, "ram_usage_pct": 20,
			"disk":              []model.DiskUsage{{Mount: "/", UsedPct: 10, Total: 100, Free: 90}},
			"docker_containers": []any{},
			"host_info": map[string]any{
				"hostname":  "pull-host\x00",
				"os":        map[string]any{"pretty_name": "Rocky Linux 9.4"},
				"addresses": []map[string]any{{"interface": "eth0", "address": "10.0.0.7/24"}, {"address": "junk"}},
			},
		})
	})
	e.poll()
	c, err := store.NewHosts(e.pool, testdb.SecretBox(t)).GetByID(e.ctx, e.host.ID)
	if err != nil {
		t.Fatal(err)
	}
	h := c.HostInfo
	if h == nil || h.Hostname != "pull-host" || h.OS == nil || h.OS.PrettyName != "Rocky Linux 9.4" || len(h.Addresses) != 1 || h.Addresses[0].Address != "10.0.0.7/24" {
		t.Fatalf("host info after poll = %+v", h)
	}
}
