package report

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

// Arka plan toplayıcıları başlamadan da rapor kurulur: Docker'ı beklemez (boş liste, null değil), çekirdek alanlar dolu.
// Envanterin komut gerektiren alanları bu durumda eskisi gibi Collect içinde toplanır (bkz. HostInfoCollector.Start).
func TestBuildWithoutBackgroundResultsDoesNotBlock(t *testing.T) {
	b := New(nil, func() time.Duration { return 30 * time.Second })
	begin := time.Now()
	p := b.Build(context.Background())
	if time.Since(begin) > 8*time.Second {
		t.Fatalf("Build took %s without background results; it must not wait for Docker", time.Since(begin))
	}
	if p.DockerContainers == nil {
		t.Fatal("docker_containers must be an empty array, not null")
	}
	if p.HostInfo == nil {
		t.Fatal("host_info missing")
	}
	body, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(body, &raw); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"cpu_usage_pct", "ram_usage_pct", "disk", "docker_containers"} {
		if _, ok := raw[field]; !ok {
			t.Errorf("core field %q missing from the report", field)
		}
	}
}

func TestStartAndWaitReadyOnThisHost(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	b := New(nil, func() time.Duration { return time.Hour })
	b.Start(ctx)
	begin := time.Now()
	b.WaitReady(ctx, FirstReportWait)
	if time.Since(begin) > FirstReportWait+time.Second {
		t.Fatalf("WaitReady took %s; it must give up after %s", time.Since(begin), FirstReportWait)
	}
	if p := b.Build(ctx); p.HostInfo == nil {
		t.Fatal("no host_info after the background start")
	}
}
