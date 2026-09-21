package collector

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

func approx(t *testing.T, got, want float64, what string) {
	t.Helper()
	if math.Abs(got-want) > 0.01 {
		t.Errorf("%s = %.4f, want %.4f", what, got, want)
	}
}

func TestParseProcStatLine(t *testing.T) {
	// user nice system idle iowait irq softirq steal guest guest_nice
	s, err := parseProcStatLine("cpu  100 0 50 800 50 0 0 0 0 0")
	if err != nil {
		t.Fatal(err)
	}
	if s.total != 1000 || s.idle != 850 { // idle, idle + iowait sayılır
		t.Fatalf("sample = %+v, want total 1000 / idle 850", s)
	}
}

func TestParseProcStatLineRejectsMalformedInput(t *testing.T) {
	for _, line := range []string{
		"", "cpu", "cpu0 1 2 3 4 5 6", "intr 1 2 3 4 5 6", "cpu a b c d e", "cpu 1 2 3 -4 5",
	} {
		if _, err := parseProcStatLine(line); err == nil {
			t.Errorf("parseProcStatLine(%q) succeeded", line)
		}
	}
}

// Eski çekirdekler (ve container'lar) 5'ten az sayaç sunabilir. Bu durum tüm agent'ı
// çökertmek yerine zarifçe ele alınmalı.
func TestParseProcStatLineWithFewCountersDoesNotPanic(t *testing.T) {
	for _, line := range []string{"cpu 10 0 5 85", "cpu 10 0 5 85 "} {
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("parseProcStatLine(%q) panicked: %v", line, r)
				}
			}()
			s, err := parseProcStatLine(line)
			if err != nil {
				return // hata dönmesi de kabul edilebilir
			}
			if s.idle > s.total {
				t.Errorf("%q: idle %d > total %d", line, s.idle, s.total)
			}
		}()
	}
}

func TestPercentFromDelta(t *testing.T) {
	approx(t, percentFromDelta(cpuSample{idle: 100, total: 200}, cpuSample{idle: 150, total: 300}), 50, "half busy")
	approx(t, percentFromDelta(cpuSample{idle: 0, total: 0}, cpuSample{idle: 100, total: 100}), 0, "fully idle")
	approx(t, percentFromDelta(cpuSample{idle: 0, total: 0}, cpuSample{idle: 0, total: 100}), 100, "fully busy")
	approx(t, percentFromDelta(cpuSample{idle: 5, total: 100}, cpuSample{idle: 5, total: 100}), 0, "no elapsed time")
}

// Geriye giden sayaçlar (sıfırlama, sıcak çıkarma) devasa bir işaretsiz farka ve sahte
// bir okumaya dönüşmemeli.
func TestPercentFromDeltaWhenCountersGoBackwards(t *testing.T) {
	got := percentFromDelta(cpuSample{idle: 500, total: 1000}, cpuSample{idle: 100, total: 200})
	if got != 0 {
		t.Errorf("percent with backwards counters = %v, want 0", got)
	}
}

func TestMemoryUsedPct(t *testing.T) {
	meminfo := "MemTotal:       16000000 kB\nMemFree:         1000000 kB\nMemAvailable:    4000000 kB\nBuffers: 1 kB\n"
	got, err := memoryUsedPct(strings.NewReader(meminfo))
	if err != nil {
		t.Fatal(err)
	}
	approx(t, got, 75, "used%") // (16M-4M)/16M
}

func TestTotalMemoryMB(t *testing.T) {
	got := totalMemoryMB(strings.NewReader("MemTotal:       16384000 kB\nMemFree: 1 kB\n"))
	if got != 16000 { // 16384000 kB / 1024
		t.Errorf("totalMemoryMB = %d, want 16000", got)
	}
	if got := totalMemoryMB(strings.NewReader("MemFree: 1 kB\n")); got != 0 {
		t.Errorf("totalMemoryMB without MemTotal = %d, want 0", got)
	}
}

func TestCPUCoresIsPositive(t *testing.T) {
	if CPUCores() < 1 {
		t.Errorf("CPUCores = %d, want >= 1", CPUCores())
	}
}

func TestMemoryUsedPctErrorsWithoutTotal(t *testing.T) {
	if _, err := memoryUsedPct(strings.NewReader("MemFree: 1 kB\n")); err == nil {
		t.Fatal("no error without MemTotal")
	}
	if _, err := memoryUsedPct(strings.NewReader("")); err == nil {
		t.Fatal("no error for empty input")
	}
}

// 3.14 öncesi çekirdeklerde MemAvailable yoktur. 0 kullanılabilir bellek raporlamak %100
// kullanım gibi okunur ve sağlıklı bir sunucuda sahte kritik alert üretir.
func TestMemoryUsedPctWithoutMemAvailable(t *testing.T) {
	meminfo := "MemTotal: 1000 kB\nMemFree: 200 kB\nBuffers: 100 kB\nCached: 300 kB\n"
	got, err := memoryUsedPct(strings.NewReader(meminfo))
	if err != nil {
		t.Fatal(err)
	}
	approx(t, got, 40, "used% (free+buffers+cached = 600 of 1000)")
}

func TestMemoryUsedPctClampsImpossibleValues(t *testing.T) {
	got, err := memoryUsedPct(strings.NewReader("MemTotal: 100 kB\nMemAvailable: 500 kB\n"))
	if err != nil {
		t.Fatal(err)
	}
	if got != 0 {
		t.Errorf("available > total gave %v%%, want 0", got)
	}
}

func TestSampleMemoryOnThisHost(t *testing.T) {
	got, err := SampleMemory()
	if err != nil {
		t.Skipf("no /proc/meminfo: %v", err)
	}
	if got < 0 || got > 100 {
		t.Errorf("SampleMemory = %v, outside 0..100", got)
	}
}

func TestCPUCollectorOnThisHost(t *testing.T) {
	c := NewCPUCollector()
	for i := 0; i < 2; i++ { // ilk çağrı kendi başlangıç değerini alır, ikincisi bir önceki örneği kullanır
		got, err := c.Sample()
		if err != nil {
			t.Skipf("no /proc/stat: %v", err)
		}
		if got < 0 || got > 100 {
			t.Errorf("Sample #%d = %v, outside 0..100", i+1, got)
		}
	}
}

func TestSampleDisk(t *testing.T) {
	dir := t.TempDir()
	got := SampleDisk([]string{dir, filepath.Join(dir, "does-not-exist")})
	if len(got) != 1 {
		t.Fatalf("got %d entries, want 1 (the unreadable mount is skipped): %+v", len(got), got)
	}
	d := got[0]
	if d.Mount != dir || d.Total <= 0 || d.Free < 0 || d.Free > d.Total || d.UsedPct < 0 || d.UsedPct > 100 {
		t.Fatalf("implausible disk usage: %+v", d)
	}
	if len(SampleDisk(nil)) != 0 {
		t.Fatal("no mounts should yield no entries")
	}
}

func TestComputeContainerCPUPercent(t *testing.T) {
	var s statsResponse
	s.CPUStats.CPUUsage.TotalUsage, s.PreCPUStats.CPUUsage.TotalUsage = 300, 100
	s.CPUStats.SystemCPUUsage, s.PreCPUStats.SystemCPUUsage = 2000, 1000
	s.CPUStats.OnlineCPUs = 4
	approx(t, computeContainerCPUPercent(s), 80, "container cpu%") // 200/1000 * 4 * 100

	s.CPUStats.OnlineCPUs = 0 // raporlanmamış: tek CPU sayılır
	approx(t, computeContainerCPUPercent(s), 20, "one-cpu fallback")

	s.CPUStats.CPUUsage.TotalUsage = 50 // sayaç geriye gitti
	if got := computeContainerCPUPercent(s); got != 0 {
		t.Errorf("negative delta gave %v, want 0", got)
	}
}

func TestDockerCollectorWithoutSocketReturnsNothing(t *testing.T) {
	c := newDockerCollector(filepath.Join(t.TempDir(), "no-docker.sock"))
	got, err := c.Sample(context.Background())
	if err != nil || got != nil {
		t.Fatalf("Sample = %v, %v; want nil, nil (hosts without Docker are normal)", got, err)
	}
}

// fakeDocker, unix soketi üzerinde asgari bir Docker Engine API'si sunar.
func fakeDocker(t *testing.T, handler http.HandlerFunc) *DockerCollector {
	t.Helper()
	// Unix soket yolları uzunlukla sınırlıdır; t.TempDir() çok uzun olabilir.
	sock := filepath.Join(t.TempDir(), "d.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Skipf("cannot listen on a unix socket here: %v", err)
	}
	srv := &http.Server{Handler: handler}
	go srv.Serve(ln)
	t.Cleanup(func() { srv.Close() })
	return newDockerCollector(sock)
}

func TestDockerCollectorSample(t *testing.T) {
	started := time.Now().Add(-90 * time.Second).UTC().Format(time.RFC3339Nano)
	c := fakeDocker(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/containers/json":
			if r.URL.Query().Get("all") != "true" {
				t.Errorf("list did not ask for stopped containers too: %s", r.URL.RawQuery)
			}
			json.NewEncoder(w).Encode([]map[string]any{
				{"Id": "aaa", "Names": []string{"/web"}, "Image": "nginx:1.27"},
				{"Id": "bbb", "Names": []string{"/old-job"}, "Image": "busybox"},
				{"Id": "ccc", "Names": []string{"/vanished"}, "Image": "alpine"},
				{"Id": "ddd", "Names": []string{}, "Image": "scratch"},
			})
		case r.URL.Path == "/containers/aaa/json":
			w.Write([]byte(`{"RestartCount":3,"State":{"Status":"running","StartedAt":"` + started + `"}}`))
		case r.URL.Path == "/containers/bbb/json":
			w.Write([]byte(`{"RestartCount":0,"State":{"Status":"exited","StartedAt":"` + started + `"}}`))
		case r.URL.Path == "/containers/ccc/json":
			http.Error(w, "no such container", http.StatusNotFound) // liste ile inspect arasında silindi
		case r.URL.Path == "/containers/ddd/json":
			w.Write([]byte(`{"RestartCount":0,"State":{"Status":"running","StartedAt":"garbage"}}`))
		case r.URL.Path == "/containers/aaa/stats":
			w.Write([]byte(`{"cpu_stats":{"cpu_usage":{"total_usage":300},"system_cpu_usage":2000,"online_cpus":2},
				"precpu_stats":{"cpu_usage":{"total_usage":100},"system_cpu_usage":1000},
				"memory_stats":{"usage":52428800}}`))
		case strings.HasSuffix(r.URL.Path, "/stats"):
			http.Error(w, "stats unavailable", http.StatusInternalServerError)
		default:
			t.Errorf("unexpected docker API call %s", r.URL)
			http.NotFound(w, r)
		}
	})

	got, err := c.Sample(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]DockerContainer{}
	for _, ct := range got {
		byName[ct.Name] = ct
	}
	if _, present := byName["vanished"]; present || len(got) != 3 {
		t.Fatalf("got %d containers %v; a container whose inspect fails must be skipped, not fail the cycle", len(got), got)
	}

	web := byName["web"]
	if web.Image != "nginx:1.27" || web.Status != "running" || web.RestartCount != 3 {
		t.Errorf("web = %+v", web)
	}
	if web.UptimeSeconds < 85 || web.UptimeSeconds > 100 {
		t.Errorf("web uptime = %ds, want ~90", web.UptimeSeconds)
	}
	approx(t, web.CPUPct, 40, "web cpu%") // 200/1000 * 2 * 100
	approx(t, web.RAMMB, 50, "web ram")

	old := byName["old-job"]
	if old.Status != "exited" || old.UptimeSeconds != 0 || old.CPUPct != 0 || old.RAMMB != 0 {
		t.Errorf("stopped container must report no uptime/cpu/ram: %+v", old)
	}

	unnamed := byName[""]
	if unnamed.Status != "running" || unnamed.UptimeSeconds != 0 {
		t.Errorf("container with an unparsable start time / no stats should still be reported: %+v", unnamed)
	}
}

func TestDockerCollectorReportsAPIFailure(t *testing.T) {
	c := fakeDocker(t, func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "daemon on fire", http.StatusInternalServerError)
	})
	if _, err := c.Sample(context.Background()); err == nil {
		t.Fatal("expected an error when the container list itself fails")
	}

	c = fakeDocker(t, func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("not json")) })
	if _, err := c.Sample(context.Background()); err == nil {
		t.Fatal("expected an error for an undecodable container list")
	}
}

// Gerçek daemon'larda stats çağrısı ~2 sn sürer; sıralı toplama 5+ container'da server'ın
// 10 sn'lik poll zaman aşımını aşıyordu. Örnekleme çakışmalı ve sınırlı olmalı, her
// container'ın değerleri doğru container'a ve liste sırasına bağlı kalmalı.
func TestDockerCollectorSamplesContainersConcurrentlyAndBounded(t *testing.T) {
	const n = 40
	const statsDelay = 200 * time.Millisecond
	started := time.Now().Add(-time.Minute).UTC().Format(time.RFC3339Nano)

	var mu sync.Mutex
	inFlight, maxInFlight := 0, 0
	c := fakeDocker(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/containers/json" {
			list := make([]map[string]any, n)
			for i := range list {
				list[i] = map[string]any{"Id": fmt.Sprintf("c%d", i), "Names": []string{fmt.Sprintf("/ct%d", i)}, "Image": "img"}
			}
			json.NewEncoder(w).Encode(list)
			return
		}
		id, kind, _ := strings.Cut(strings.TrimPrefix(r.URL.Path, "/containers/"), "/")
		switch kind {
		case "json":
			w.Write([]byte(`{"RestartCount":0,"State":{"Status":"running","StartedAt":"` + started + `"}}`))
		case "stats":
			mu.Lock()
			inFlight++
			if inFlight > maxInFlight {
				maxInFlight = inFlight
			}
			mu.Unlock()
			time.Sleep(statsDelay)
			mu.Lock()
			inFlight--
			mu.Unlock()
			var idx int
			fmt.Sscanf(id, "c%d", &idx)
			fmt.Fprintf(w, `{"cpu_stats":{},"precpu_stats":{},"memory_stats":{"usage":%d}}`, idx*1024*1024)
		default:
			t.Errorf("unexpected docker API call %s", r.URL)
		}
	})

	begin := time.Now()
	got, err := c.Sample(context.Background())
	elapsed := time.Since(begin)
	if err != nil {
		t.Fatal(err)
	}

	if len(got) != n {
		t.Fatalf("got %d containers, want %d", len(got), n)
	}
	for i, ct := range got {
		if ct.Name != fmt.Sprintf("ct%d", i) {
			t.Fatalf("result %d is %q: list order must be preserved", i, ct.Name)
		}
		approx(t, ct.RAMMB, float64(i), fmt.Sprintf("ct%d ram (stats attached to the wrong container?)", i))
	}
	if elapsed > n*statsDelay/4 {
		t.Errorf("Sample took %s for %d containers with %s stats each; sampling is not overlapping", elapsed, n, statsDelay)
	}
	if maxInFlight < 2 {
		t.Errorf("max concurrent stats calls = %d, want overlapping calls", maxInFlight)
	}
	if maxInFlight > maxConcurrentContainerSamples || maxInFlight >= n {
		t.Errorf("max concurrent stats calls = %d; must be bounded by %d and well below the %d containers", maxInFlight, maxConcurrentContainerSamples, n)
	}
}

func TestInodePct(t *testing.T) {
	if p := inodePct(1000, 250); p == nil || *p != 75 {
		t.Errorf("inodePct(1000,250) = %v, want 75", p)
	}
	if p := inodePct(1000, 1000); p == nil || *p != 0 {
		t.Errorf("a filesystem with no inodes used must report 0%%, not unknown: %v", p)
	}
	if p := inodePct(1000, 0); p == nil || *p != 100 {
		t.Errorf("inodePct(1000,0) = %v, want 100", p)
	}
	// Dosya sistemi inode bildirmiyor (btrfs: Files=0) ya da okuma tutarsız: bilinmiyor (nil), 0 değil.
	if inodePct(0, 0) != nil || inodePct(100, 200) != nil {
		t.Error("unknown inode counts must yield nil")
	}
}

// statfs'ten gelen inode sayıları rapora bağlanır.
func TestSampleDiskReportsInodeUsage(t *testing.T) {
	orig := statfsFn
	t.Cleanup(func() { statfsFn = orig })
	statfsFn = func(path string, st *syscall.Statfs_t) error {
		st.Blocks, st.Bavail, st.Bsize = 1000, 500, 4096
		switch path {
		case "/ext4":
			st.Files, st.Ffree = 1000, 100
		case "/btrfs":
			st.Files, st.Ffree = 0, 0
		}
		return nil
	}
	got := map[string]*float64{}
	for _, d := range SampleDisk([]string{"/ext4", "/btrfs"}) {
		got[d.Mount] = d.InodesUsedPct
	}
	if got["/ext4"] == nil || *got["/ext4"] != 90 {
		t.Errorf("/ext4 inode pct = %v, want 90", got["/ext4"])
	}
	if got["/btrfs"] != nil {
		t.Errorf("/btrfs (no inode count) = %v, want nil", *got["/btrfs"])
	}
}
