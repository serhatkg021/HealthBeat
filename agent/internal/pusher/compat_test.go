package pusher

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"healthbeat-agent/internal/collector"
)

// fakeSender, sırayla verilen sonuçları döndürür ve gönderilen payload'ları kaydeder.
type fakeSender struct {
	results []error
	sent    []MetricsPayload
}

func (f *fakeSender) Push(_ context.Context, p MetricsPayload) error {
	f.sent = append(f.sent, p)
	if len(f.results) == 0 {
		return nil
	}
	err := f.results[0]
	f.results = f.results[1:]
	return err
}

var (
	bad400  = &StatusError{Status: 400, Body: `{"error":"geçersiz istek gövdesi"}`}
	fail500 = &StatusError{Status: 500, Body: "boom"}
	fail401 = &StatusError{Status: 401, Body: "nope"}
	netErr  = errors.New("send request: connection refused")
)

func full() MetricsPayload {
	p := sample
	p.CPUCores, p.RAMTotalMB = 8, 16000
	p.PhysicalDisks = []PhysicalDisk{{Name: "sda", Mounts: []string{"/"}}}
	inodes := 42.5
	p.Disk = []DiskUsage{{Mount: "/", UsedPct: 40, Total: 100, Free: 60, InodesUsedPct: &inodes}}
	p.HostInfo = &collector.HostInfo{Hostname: "h", OS: &collector.OSInfo{PrettyName: "Ubuntu 24.04"}}
	return p
}

// hasHardware, çekirdek dışı (sonradan eklenen) herhangi bir alan varsa true: donanım özeti, envanter ya da
// disk girdisindeki inode alanı.
func hasHardware(p MetricsPayload) bool {
	if p.CPUCores != 0 || p.RAMTotalMB != 0 || p.PhysicalDisks != nil || p.HostInfo != nil {
		return true
	}
	for _, d := range p.Disk {
		if d.InodesUsedPct != nil {
			return true
		}
	}
	return false
}

func newTestCompat(s Sender) (*Compat, *[]string) {
	var logs []string
	c := NewCompat(s)
	c.logf = func(format string, args ...any) { logs = append(logs, fmt.Sprintf(format, args...)) }
	return c, &logs
}

func TestCoreDropsHardwareButKeepsCoreFields(t *testing.T) {
	core := full().Core()
	if hasHardware(core) {
		t.Errorf("Core() kept hardware fields: %+v", core)
	}
	if core.CPUUsagePct != sample.CPUUsagePct || len(core.Disk) != 1 || len(core.DockerContainers) != 1 {
		t.Errorf("Core() lost core fields: %+v", core)
	}
	b, _ := json.Marshal(core)
	for _, k := range []string{"cpu_cores", "ram_total_mb", "physical_disks", "host_info", "inodes_used_pct"} {
		if strings.Contains(string(b), k) {
			t.Errorf("core JSON contains %s: %s", k, b)
		}
	}
}

func TestCompatSendsFullPayloadWhenServerAcceptsIt(t *testing.T) {
	f := &fakeSender{}
	c, logs := newTestCompat(f)
	if err := c.Push(context.Background(), full()); err != nil {
		t.Fatal(err)
	}
	if c.Degraded() || len(f.sent) != 1 || !hasHardware(f.sent[0]) || len(*logs) != 0 {
		t.Errorf("degraded=%v sent=%d logs=%v", c.Degraded(), len(f.sent), *logs)
	}
}

// Eski, bilinmeyen alanı reddeden server: aynı döngüde çekirdek payload ile yeniden denenir,
// metrik kaybolmaz.
func TestCompatFallsBackToCoreOn400(t *testing.T) {
	f := &fakeSender{results: []error{bad400, nil}}
	c, logs := newTestCompat(f)
	if err := c.Push(context.Background(), full()); err != nil {
		t.Fatalf("Push = %v, want nil (core payload was accepted)", err)
	}
	if !c.Degraded() {
		t.Error("not degraded after fallback")
	}
	if len(f.sent) != 2 || !hasHardware(f.sent[0]) || hasHardware(f.sent[1]) {
		t.Errorf("sent = %+v, want [full, core]", f.sent)
	}
	if len(*logs) != 1 || !strings.Contains((*logs)[0], "core metrics only") {
		t.Errorf("logs = %v", *logs)
	}
}

func TestCompatDegradedSendsCoreThenProbesFullPayload(t *testing.T) {
	f := &fakeSender{results: []error{bad400, nil}}
	c, _ := newTestCompat(f)
	_ = c.Push(context.Background(), full()) // degraded'a geç
	f.sent = nil

	// probeEvery-1 döngü boyunca yalnızca çekirdek gider (tek istek).
	for i := 1; i < probeEvery; i++ {
		if err := c.Push(context.Background(), full()); err != nil {
			t.Fatal(err)
		}
	}
	if len(f.sent) != probeEvery-1 {
		t.Fatalf("%d requests in %d degraded cycles, want one each", len(f.sent), probeEvery-1)
	}
	for _, p := range f.sent {
		if hasHardware(p) {
			t.Fatalf("degraded cycle sent hardware fields: %+v", p)
		}
	}

	// Yoklama döngüsü: server hâlâ eski (400) => tam payload denenir, çekirdekle devam edilir.
	f.sent, f.results = nil, []error{bad400, nil}
	if err := c.Push(context.Background(), full()); err != nil {
		t.Fatal(err)
	}
	if !c.Degraded() || len(f.sent) != 2 || !hasHardware(f.sent[0]) || hasHardware(f.sent[1]) {
		t.Fatalf("probe against old server: degraded=%v sent=%+v", c.Degraded(), f.sent)
	}

	// Sonraki yoklamada server güncellenmiş: normale döner.
	for i := 1; i < probeEvery; i++ {
		_ = c.Push(context.Background(), full())
	}
	f.sent, f.results = nil, nil // yoklama tam payload ile başarılı
	c2logs := 0
	c.logf = func(string, ...any) { c2logs++ }
	if err := c.Push(context.Background(), full()); err != nil {
		t.Fatal(err)
	}
	if c.Degraded() || len(f.sent) != 1 || !hasHardware(f.sent[0]) || c2logs != 1 {
		t.Fatalf("after server update: degraded=%v sent=%d logs=%d", c.Degraded(), len(f.sent), c2logs)
	}
	// Ve sonrasında tam payload gitmeye devam eder.
	f.sent = nil
	_ = c.Push(context.Background(), full())
	if len(f.sent) != 1 || !hasHardware(f.sent[0]) {
		t.Errorf("after recovery sent = %+v", f.sent)
	}
}

// Yalnızca 400 uyumsuzluk sayılır; kimlik, hız sınırı, 5xx ve ağ hataları olduğu gibi bildirilir
// ve TEK istekle biter (çekirdek payload ile yeniden denenmez).
func TestCompatOnlyFallsBackOn400(t *testing.T) {
	for name, e := range map[string]error{"500": fail500, "401": fail401, "network": netErr} {
		f := &fakeSender{results: []error{e}}
		c, _ := newTestCompat(f)
		err := c.Push(context.Background(), full())
		if !errors.Is(err, e) && err != e {
			t.Errorf("%s: err = %v, want %v", name, err, e)
		}
		if c.Degraded() || len(f.sent) != 1 {
			t.Errorf("%s: degraded=%v requests=%d, want a single request and no fallback", name, c.Degraded(), len(f.sent))
		}
	}
}

// Çekirdek payload da 400 alıyorsa sorun uyumluluk değildir: ilk hata döner, degraded'a geçilmez.
func TestCompatDoesNotDegradeWhenCorePayloadIsRejectedToo(t *testing.T) {
	f := &fakeSender{results: []error{bad400, bad400}}
	c, _ := newTestCompat(f)
	if err := c.Push(context.Background(), full()); err != bad400 {
		t.Errorf("err = %v, want the original 400", err)
	}
	if c.Degraded() {
		t.Error("degraded although the core payload was rejected too")
	}
}

func TestCompatDegradedCoreFailureIsReportedAndKeepsMode(t *testing.T) {
	f := &fakeSender{results: []error{bad400, nil, netErr}}
	c, _ := newTestCompat(f)
	_ = c.Push(context.Background(), full())
	if err := c.Push(context.Background(), full()); err != netErr {
		t.Errorf("err = %v, want the network error", err)
	}
	if !c.Degraded() {
		t.Error("left degraded mode after a network error")
	}
}

// Uçtan uca: gerçek Pusher, bilinmeyen alanı 400 ile reddeden ESKİ bir server'a karşı — yeni agent
// metriklerini kaybetmez.
func TestCompatAgainstAStrictLegacyServer(t *testing.T) {
	// Eski server'ın bildiği disk girdisi: inode alanı YOK (DisallowUnknownFields iç içe nesnelerde de uygulanır).
	type legacyDisk struct {
		Mount   string  `json:"mount"`
		UsedPct float64 `json:"used_pct"`
		Total   int64   `json:"total"`
		Free    int64   `json:"free"`
	}
	type legacyBody struct {
		CPUUsagePct      float64           `json:"cpu_usage_pct"`
		RAMUsagePct      float64           `json:"ram_usage_pct"`
		Disk             []legacyDisk      `json:"disk"`
		DockerContainers []DockerContainer `json:"docker_containers"`
	}
	var accepted []legacyBody
	rejected := 0
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var b legacyBody
		dec := json.NewDecoder(r.Body)
		dec.DisallowUnknownFields() // eski server'ın davranışı
		if err := dec.Decode(&b); err != nil {
			rejected++
			http.Error(w, `{"error":"geçersiz istek gövdesi"}`, http.StatusBadRequest)
			return
		}
		accepted = append(accepted, b)
		w.WriteHeader(http.StatusNoContent) // sürüm başlığı yok: sözleşmeyi bilmeyen server
	}))
	t.Cleanup(srv.Close)

	p := New(srv.URL, "id", "tok", TLSOptions{InsecureSkipVerify: true})
	c, _ := newTestCompat(p)
	for i := 0; i < 3; i++ {
		if err := c.Push(context.Background(), full()); err != nil {
			t.Fatalf("cycle %d: %v", i, err)
		}
	}
	if len(accepted) != 3 {
		t.Fatalf("legacy server accepted %d reports, want 3 (no metrics lost)", len(accepted))
	}
	if accepted[0].CPUUsagePct != sample.CPUUsagePct || len(accepted[0].Disk) != 1 || len(accepted[0].DockerContainers) != 1 {
		t.Errorf("core fields were not delivered intact: %+v", accepted[0])
	}
	if rejected != 1 {
		t.Errorf("legacy server rejected %d requests, want exactly the first full attempt", rejected)
	}
	if info := p.ServerInfo(); info != (ServerInfo{}) {
		t.Errorf("ServerInfo from a legacy server = %+v, want empty", info)
	}
}

func TestPusherCapturesServerInfoHeaders(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-HealthBeat-Server-Version", "1.3.0")
		w.Header().Set("X-HealthBeat-Protocol", "2")
		w.Header().Set("X-HealthBeat-Latest-Agent", "1.4.0")
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(srv.Close)
	p := New(srv.URL, "id", "tok", TLSOptions{InsecureSkipVerify: true})
	if (p.ServerInfo() != ServerInfo{}) {
		t.Fatal("ServerInfo before any response is not empty")
	}
	if err := p.Push(context.Background(), sample); err != nil {
		t.Fatal(err)
	}
	if got := p.ServerInfo(); got != (ServerInfo{Version: "1.3.0", Protocol: 2, LatestAgent: "1.4.0"}) {
		t.Errorf("ServerInfo = %+v", got)
	}
}

func TestPushReturnsStatusErrorWithUnchangedMessage(t *testing.T) {
	srv, _ := fakeServer(t, http.StatusBadRequest, "  bad body \n")
	err := New(srv.URL, "id", "tok", TLSOptions{InsecureSkipVerify: true}).Push(context.Background(), sample)
	var se *StatusError
	if !errors.As(err, &se) || se.Status != 400 || se.Body != "bad body" {
		t.Fatalf("err = %#v, want *StatusError{400, \"bad body\"}", err)
	}
	if err.Error() != "server returned 400: bad body" {
		t.Errorf("message = %q", err.Error())
	}
}

func TestServerNotes(t *testing.T) {
	for _, tc := range []struct {
		name         string
		info         ServerInfo
		want, forbid []string
	}{
		{"legacy server says nothing", ServerInfo{}, nil, []string{"server", "recommends", "older"}},
		{"same protocol, nothing to recommend", ServerInfo{Version: "1.1.0", Protocol: 2, LatestAgent: "1.1.0"},
			[]string{"server 1.1.0 (protocol 2)"}, []string{"recommends", "older than"}},
		{"newer agent recommended", ServerInfo{Version: "1.3.0", Protocol: 2, LatestAgent: "1.4.0"},
			[]string{"server 1.3.0", "recommends agent 1.4.0", "this agent is 1.1.0"}, []string{"older than"}},
		{"older protocol", ServerInfo{Version: "1.0.0", Protocol: 1}, []string{"protocol 1, older than this agent's 2"}, []string{"recommends"}},
		{"newer protocol is not a problem", ServerInfo{Version: "1.9.0", Protocol: 3}, []string{"server 1.9.0"}, []string{"older than"}},
		{"recommended is older than us", ServerInfo{Version: "1.0.5", Protocol: 2, LatestAgent: "1.0.9"}, []string{"server 1.0.5"}, []string{"recommends"}},
		{"recommended equals us", ServerInfo{Version: "1.1.0", Protocol: 2, LatestAgent: "1.1.0"}, []string{"server 1.1.0"}, []string{"recommends"}},
	} {
		notes := strings.Join(ServerNotes(tc.info, "1.1.0", 2), " | ")
		for _, w := range tc.want {
			if !strings.Contains(notes, w) {
				t.Errorf("%s: notes %q lack %q", tc.name, notes, w)
			}
		}
		for _, f := range tc.forbid {
			if strings.Contains(notes, f) {
				t.Errorf("%s: notes %q must not contain %q", tc.name, notes, f)
			}
		}
		if len(tc.want) == 0 && notes != "" {
			t.Errorf("%s: notes = %q, want none", tc.name, notes)
		}
	}
}
