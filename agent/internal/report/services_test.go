package report

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"healthbeat-agent/internal/collector"
)

func svc(name, active, sub string, restarts int) collector.ServiceState {
	return collector.ServiceState{Name: name, Active: active, Sub: sub, Restarts: &restarts}
}

func names(t *testing.T, r *serviceReporter, current []collector.ServiceState) (bool, []string) {
	t.Helper()
	out := r.next(current, true)
	if out == nil {
		t.Fatal("known services produced no services section")
	}
	var n []string
	for _, s := range out.Items {
		n = append(n, s.Name)
	}
	return out.Full, n
}

func TestServiceReporterFullThenPartial(t *testing.T) {
	now := time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)
	r := &serviceReporter{now: func() time.Time { return now }}
	base := []collector.ServiceState{
		svc("cron.service", "active", "running", 0),
		svc("nginx.service", "active", "running", 0),
		svc("certbot.service", "inactive", "dead", 0),
		svc("redis.service", "activating", "auto-restart", 4),
	}

	if full, got := names(t, r, base); !full || len(got) != 4 {
		t.Fatalf("first report: full=%v %v; want the full list", full, got)
	}

	now = now.Add(30 * time.Second)
	if full, got := names(t, r, base); full || strings.Join(got, ",") != "redis.service" {
		t.Fatalf("unchanged report: full=%v %v; want only the troubled service", full, got)
	}

	now = now.Add(30 * time.Second)
	next := []collector.ServiceState{
		svc("cron.service", "active", "running", 0),
		svc("nginx.service", "failed", "failed", 0),     // durdu
		svc("certbot.service", "inactive", "dead", 0),   // değişmedi: gönderilmez
		svc("redis.service", "active", "running", 4),    // düzeldi: değişti
		svc("postgres.service", "active", "running", 0), // yeni kuruldu
	}
	if full, got := names(t, r, next); full || strings.Join(got, ",") != "nginx.service,redis.service,postgres.service" {
		t.Fatalf("changes: full=%v %v", full, got)
	}

	now = now.Add(30 * time.Second)
	next[0].Restarts = ptrInt(1) // yeniden başlatıldı, durum aynı
	if _, got := names(t, r, next); strings.Join(got, ",") != "cron.service,nginx.service" {
		t.Fatalf("restart counter change: %v; want cron (restarted) and nginx (still failed)", got)
	}

	now = now.Add(fullServicesEvery)
	if full, got := names(t, r, next); !full || len(got) != 5 {
		t.Fatalf("after %s: full=%v %v; want the full list again", fullServicesEvery, full, got)
	}
}

func TestServiceReporterUnknownAndEmpty(t *testing.T) {
	r := newServiceReporter()
	if r.next(nil, true) != nil || r.next([]collector.ServiceState{svc("a.service", "active", "running", 0)}, false) != nil {
		t.Fatal("no systemd / stale list must omit the services section")
	}
	r.next([]collector.ServiceState{svc("a.service", "active", "running", 0)}, true)
	out := r.next([]collector.ServiceState{svc("a.service", "active", "running", 0)}, true)
	b, _ := json.Marshal(out)
	if string(b) != `{"full":false,"items":[]}` {
		t.Fatalf("a quiet partial report = %s; want an empty item list, not null", b)
	}
}

func ptrInt(v int) *int { return &v }
