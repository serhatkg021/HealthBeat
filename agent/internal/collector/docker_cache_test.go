package collector

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

// cacheDocker, tek container'lı ("web") sahte bir daemon'dur; inspect ve stats çağrılarını sayar. cpuTotal/cpuSystem,
// one-shot yanıtının sayaçlarıdır; iki örnekli (stream=false) yanıt hep 300/2000 (öncesi 100/1000), 2 CPU döner.
type cacheDocker struct {
	mu                  sync.Mutex
	apiVersion          string
	status              string
	present             bool
	inspects, twoPass   int
	oneShots            int
	cpuTotal, cpuSystem uint64
}

func (f *cacheDocker) handler(t *testing.T) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		switch r.URL.Path {
		case "/version":
			json.NewEncoder(w).Encode(map[string]string{"Version": "27.3.1", "ApiVersion": f.apiVersion})
		case "/containers/json":
			list := []map[string]any{}
			if f.present {
				list = append(list, map[string]any{"Id": "aaa", "Names": []string{"/web"}, "Image": "nginx", "State": "running", "Status": f.status})
			}
			json.NewEncoder(w).Encode(list)
		case "/containers/aaa/json":
			f.inspects++
			w.Write([]byte(`{"RestartCount":0,"State":{"Status":"running","StartedAt":"2026-10-06T10:00:00Z"}}`))
		case "/containers/aaa/stats":
			if r.URL.Query().Get("one-shot") == "true" {
				f.oneShots++
				json.NewEncoder(w).Encode(map[string]any{
					"cpu_stats":    map[string]any{"cpu_usage": map[string]any{"total_usage": f.cpuTotal}, "system_cpu_usage": f.cpuSystem, "online_cpus": 2},
					"memory_stats": map[string]any{"usage": 52428800},
				})
				return
			}
			f.twoPass++
			w.Write([]byte(`{"cpu_stats":{"cpu_usage":{"total_usage":300},"system_cpu_usage":2000,"online_cpus":2},
				"precpu_stats":{"cpu_usage":{"total_usage":100},"system_cpu_usage":1000},"memory_stats":{"usage":52428800}}`))
		default:
			t.Errorf("unexpected docker API call %s", r.URL)
			http.NotFound(w, r)
		}
	}
}

func (f *cacheDocker) counts() (inspects, twoPass, oneShots int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.inspects, f.twoPass, f.oneShots
}

func sampleOne(t *testing.T, c *DockerCollector) DockerContainer {
	t.Helper()
	got, err := c.Sample(context.Background())
	if err != nil || len(got) != 1 {
		t.Fatalf("Sample = %v, %v; want one container", got, err)
	}
	return got[0]
}

// İlk örnek daemon'un iki örnekli ölçümüyle alınır; sonrakiler one-shot ve CPU farkı önceki örnekle hesaplanır.
// Durumu değişmeyen container yeniden inspect edilmez.
func TestDockerCollectorUsesOneShotAfterFirstSample(t *testing.T) {
	f := &cacheDocker{apiVersion: "1.47", status: "Up 2 hours", present: true}
	c := fakeDocker(t, f.handler(t))

	approx(t, sampleOne(t, c).CPUPct, 40, "first sample cpu%") // 200/1000 * 2 * 100
	if ins, two, one := f.counts(); ins != 1 || two != 1 || one != 0 {
		t.Fatalf("after first sample: inspects=%d twoPass=%d oneShots=%d; want 1/1/0", ins, two, one)
	}

	f.mu.Lock()
	f.cpuTotal, f.cpuSystem = 700, 3000 // önceki örnek 300/2000: 400/1000 * 2 * 100
	f.mu.Unlock()
	web := sampleOne(t, c)
	approx(t, web.CPUPct, 80, "one-shot cpu% from the previous sample")
	approx(t, web.RAMMB, 50, "one-shot ram")
	if ins, two, one := f.counts(); ins != 1 || two != 1 || one != 1 {
		t.Fatalf("after second sample: inspects=%d twoPass=%d oneShots=%d; want 1/1/1 (cached inspect, one-shot stats)", ins, two, one)
	}

	f.mu.Lock()
	f.status = "Up 3 seconds" // container yeniden başladı
	f.mu.Unlock()
	sampleOne(t, c)
	if ins, _, _ := f.counts(); ins != 2 {
		t.Fatalf("inspects=%d after the container's status changed; want a fresh inspect", ins)
	}
}

// Container yeniden başlayınca sayaçlar sıfırlanır: geri giden sayaçla fark hesaplanmaz, iki örnekli ölçüme dönülür.
func TestDockerCollectorFallsBackWhenCountersGoBackwards(t *testing.T) {
	f := &cacheDocker{apiVersion: "1.47", status: "Up 2 hours", present: true}
	c := fakeDocker(t, f.handler(t))
	sampleOne(t, c)

	f.mu.Lock()
	f.cpuTotal, f.cpuSystem = 10, 3000
	f.mu.Unlock()
	approx(t, sampleOne(t, c).CPUPct, 40, "cpu% after a counter reset")
	if _, two, one := f.counts(); two != 2 || one != 1 {
		t.Fatalf("twoPass=%d oneShots=%d; want the two-pass fallback after the one-shot", two, one)
	}
}

func TestDockerCollectorOldAPINeverUsesOneShot(t *testing.T) {
	f := &cacheDocker{apiVersion: "1.40", status: "Up 2 hours", present: true}
	c := fakeDocker(t, f.handler(t))
	sampleOne(t, c)
	sampleOne(t, c)
	if _, two, one := f.counts(); two != 2 || one != 0 {
		t.Fatalf("twoPass=%d oneShots=%d; a daemon older than API 1.41 must not get one-shot requests", two, one)
	}
}

func TestDockerCollectorInspectCacheExpires(t *testing.T) {
	f := &cacheDocker{apiVersion: "1.47", status: "Up 2 hours", present: true}
	c := fakeDocker(t, f.handler(t))
	now := time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)
	c.now = func() time.Time { return now }

	sampleOne(t, c)
	now = now.Add(inspectMaxAge - time.Second)
	sampleOne(t, c)
	if ins, _, _ := f.counts(); ins != 1 {
		t.Fatalf("inspects=%d before the cache expired; want 1", ins)
	}
	now = now.Add(2 * time.Second)
	sampleOne(t, c)
	if ins, _, _ := f.counts(); ins != 2 {
		t.Fatalf("inspects=%d after %s; want a fresh inspect", ins, inspectMaxAge)
	}
}

func TestDockerCollectorForgetsRemovedContainers(t *testing.T) {
	f := &cacheDocker{apiVersion: "1.47", status: "Up 2 hours", present: true}
	c := fakeDocker(t, f.handler(t))
	sampleOne(t, c)

	f.mu.Lock()
	f.present = false
	f.mu.Unlock()
	if got, err := c.Sample(context.Background()); err != nil || len(got) != 0 {
		t.Fatalf("Sample = %v, %v; want no containers", got, err)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.inspects) != 0 || len(c.prevCPU) != 0 {
		t.Fatalf("caches still hold %d inspects and %d cpu samples for removed containers", len(c.inspects), len(c.prevCPU))
	}
}

func TestAPIAtLeast(t *testing.T) {
	for v, want := range map[string]bool{"1.41": true, "1.47": true, "2.0": true, "1.40": false, "1.9": false, "": false, "garbage": false} {
		if got := apiAtLeast(v, 1, 41); got != want {
			t.Errorf("apiAtLeast(%q, 1, 41) = %v, want %v", v, got, want)
		}
	}
}

// Sağlık durumu yalnızca healthcheck tanımlıysa, çıkış kodu ve OOM yalnızca durmuş/yeniden başlayan container'da gelir.
func TestDockerCollectorHealthExitCodeAndOOM(t *testing.T) {
	c := fakeDocker(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/version":
			w.Write([]byte(`{"Version":"27.3.1","ApiVersion":"1.47"}`))
		case "/containers/json":
			w.Write([]byte(`[{"Id":"a","Names":["/api"]},{"Id":"b","Names":["/redis"]},{"Id":"c","Names":["/worker"]},
				{"Id":"d","Names":["/flappy"]},{"Id":"e","Names":["/migrate"]}]`))
		case "/containers/a/json":
			w.Write([]byte(`{"State":{"Status":"running","ExitCode":0,"Health":{"Status":"unhealthy","FailingStreak":3}}}`))
		case "/containers/b/json":
			w.Write([]byte(`{"State":{"Status":"running","ExitCode":0}}`)) // healthcheck yok
		case "/containers/c/json":
			w.Write([]byte(`{"State":{"Status":"exited","ExitCode":137,"OOMKilled":true,"Health":{"Status":"unhealthy","FailingStreak":1}}}`))
		case "/containers/d/json":
			w.Write([]byte(`{"RestartCount":8,"State":{"Status":"restarting","ExitCode":1,"OOMKilled":false,"Health":{"Status":"starting","FailingStreak":0}}}`))
		case "/containers/e/json":
			w.Write([]byte(`{"State":{"Status":"exited","ExitCode":0,"Health":{"Status":"none"}}}`))
		default:
			if strings.HasSuffix(r.URL.Path, "/stats") {
				w.Write([]byte(`{"cpu_stats":{},"precpu_stats":{},"memory_stats":{}}`))
				return
			}
			t.Errorf("unexpected docker API call %s", r.URL)
		}
	})
	got, err := c.Sample(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	by := map[string]DockerContainer{}
	for _, ct := range got {
		by[ct.Name] = ct
	}

	if api := by["api"]; api.Health != "unhealthy" || api.HealthFailingStreak == nil || *api.HealthFailingStreak != 3 || api.ExitCode != nil || api.OOMKilled != nil {
		t.Errorf("running unhealthy container = %+v; want health with streak 3 and no exit code/OOM", api)
	}
	if redis := by["redis"]; redis.Health != "" || redis.HealthFailingStreak != nil {
		t.Errorf("container without a healthcheck reported health %+v", redis)
	}
	if w := by["worker"]; w.ExitCode == nil || *w.ExitCode != 137 || w.OOMKilled == nil || !*w.OOMKilled {
		t.Errorf("OOM-killed exited container = %+v; want exit 137, oom_killed=true", w)
	}
	if f := by["flappy"]; f.Health != "starting" || f.ExitCode == nil || *f.ExitCode != 1 || f.OOMKilled == nil || *f.OOMKilled {
		t.Errorf("restarting container = %+v; want health starting, exit 1, oom_killed=false", f)
	}
	if m := by["migrate"]; m.Health != "" || m.ExitCode == nil || *m.ExitCode != 0 {
		t.Errorf("exited job = %+v; health \"none\" is no healthcheck, exit code 0 is known", m)
	}
}
