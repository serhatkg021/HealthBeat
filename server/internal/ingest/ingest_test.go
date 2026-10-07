package ingest_test

import (
	"context"
	"errors"
	"os"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"healthbeat-server/internal/alertengine"
	"healthbeat-server/internal/ingest"
	"healthbeat-server/internal/model"
	"healthbeat-server/internal/notify"
	"healthbeat-server/internal/store"
	"healthbeat-server/internal/testdb"
)

func TestDecode(t *testing.T) {
	payload, unknown, err := ingest.Decode([]byte(`{"cpu_usage_pct": 12.5, "ram_usage_pct": 40, "disk": [], "future_field": 1}`))
	if err != nil || payload.CPUUsagePct != 12.5 || !slices.Equal(unknown, []string{"future_field"}) {
		t.Fatalf("Decode = %+v, %v, %v", payload, unknown, err)
	}

	// Çözülemeyen rapor ErrMalformed'dır; push onu "geçersiz istek gövdesi"ne çevirir.
	for _, body := range []string{`{"cpu_usage_pct": `, `[]`, `{"cpu_usage_pct": "yüksek"}`} {
		if _, _, err := ingest.Decode([]byte(body)); !errors.Is(err, ingest.ErrMalformed) {
			t.Errorf("Decode(%s) = %v, want ErrMalformed", body, err)
		}
	}

	// Doğrulama hatası ayrıdır ve metni istemciye gider.
	_, _, err = ingest.Decode([]byte(`{"cpu_usage_pct": 150, "ram_usage_pct": 10}`))
	if err == nil || errors.Is(err, ingest.ErrMalformed) || err.Error() != "cpu_usage_pct ve ram_usage_pct 0 ile 100 arasında olmalı" {
		t.Fatalf("validation error = %v", err)
	}
}

type env struct {
	pool    *pgxpool.Pool
	svc     *ingest.Service
	metrics *store.Metrics
	hosts   *store.Hosts
	engine  *alertengine.Engine
	host    uuid.UUID
	org     uuid.UUID
}

func newEnv(t *testing.T) *env {
	t.Helper()
	pool := testdb.New(t)
	e := &env{pool: pool, metrics: store.NewMetrics(pool), hosts: store.NewHosts(pool, testdb.SecretBox(t))}
	e.engine = alertengine.New(pool, notify.New(notify.Config{}), "") // yalnızca log'a yazan posta
	e.svc = ingest.New(e.metrics, e.hosts, e.engine)
	e.org = testdb.Org(t, pool, "o")
	e.host = testdb.PushHost(t, pool, e.org, "h", "")
	t.Cleanup(e.engine.Flush)
	return e
}

func (e *env) openAlerts(t *testing.T, hostID uuid.UUID) int {
	t.Helper()
	var n int
	if err := e.pool.QueryRow(context.Background(), `SELECT count(*) FROM alerts WHERE host_id = $1 AND status = 'open'`, hostID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// Record, iki alım yolunun ortak adımlarını çalıştırır: metrik, container'lar, çevrimiçi işareti ve alert motoru.
func TestRecordStoresAndEvaluates(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	testdb.Threshold(t, e.pool, &e.org, nil, model.MetricTypeCPU, 50, 80)

	version := "1.4.0"
	err := e.svc.Record(ctx, ingest.Report{
		HostID: e.host, OrgID: e.org, Source: ingest.SourcePush,
		Payload: model.MetricsIngestRequest{
			CPUUsagePct: 91, RAMUsagePct: 20, CPUCores: 4,
			Disk:             []model.DiskUsage{{Mount: "/", UsedPct: 10, Total: 100, Free: 90}},
			DockerContainers: []model.DockerContainerReport{{Name: "web", Image: "nginx", Status: "running"}},
		},
		Agent: model.AgentInfo{Version: version, Protocol: 3},
	})
	if err != nil {
		t.Fatal(err)
	}

	latest, err := e.metrics.Latest(ctx, e.host)
	if err != nil || latest.CPUUsagePct != 91 || len(latest.Disk) != 1 {
		t.Fatalf("latest metric = %+v, %v", latest, err)
	}
	containers, err := e.metrics.LatestDockerContainers(ctx, e.host)
	if err != nil || len(containers) != 1 || containers[0].Name != "web" {
		t.Fatalf("containers = %+v, %v", containers, err)
	}
	host, err := e.hosts.GetByID(ctx, e.host)
	if err != nil || host.Status != "online" || host.CPUCores == nil || *host.CPUCores != 4 ||
		host.AgentVersion == nil || *host.AgentVersion != version {
		t.Fatalf("host after report = %+v, %v", host, err)
	}
	e.engine.Flush()
	if n := e.openAlerts(t, e.host); n != 1 {
		t.Fatalf("open alerts = %d, want 1 (cpu 91 > critical 80)", n)
	}
}

// Metrik satırı yazılamazsa hata döner ve sonraki adımlar (çevrimiçi işareti, alert) çalışmaz.
func TestRecordStopsWhenMetricsCannotBeStored(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	testdb.Threshold(t, e.pool, &e.org, nil, model.MetricTypeCPU, 50, 80)

	missing := uuid.New() // FK ihlali: metrics.host_id
	err := e.svc.Record(ctx, ingest.Report{HostID: missing, OrgID: e.org, Source: ingest.SourcePull,
		Payload: model.MetricsIngestRequest{CPUUsagePct: 95}})
	if err == nil {
		t.Fatal("want an error for an unknown host")
	}
	e.engine.Flush()
	if n := e.openAlerts(t, missing); n != 0 {
		t.Fatalf("alerts raised despite the failed insert: %d", n)
	}
}

// Protokol 4 raporu: zaman serisi metrik satırına (ölçüldüğü gibi), anlık durumlar host_status'a, servisler ve container
// sağlığı kendi tablolarına yazılır; ikinci raporda OOM sayacının artışı görülür.
func TestRecordStoresProtocol4Report(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	body, err := os.ReadFile("../../testdata/payloads/v4_health_performance.json")
	if err != nil {
		t.Fatal(err)
	}
	payload, unknown, err := ingest.Decode(body)
	if err != nil || unknown != nil {
		t.Fatalf("Decode: unknown=%v err=%v", unknown, err)
	}
	record := func(p model.MetricsIngestRequest) {
		t.Helper()
		if err := e.svc.Record(ctx, ingest.Report{HostID: e.host, OrgID: e.org, Source: ingest.SourcePush, Payload: p,
			Agent: model.AgentInfo{Version: "1.3.0", Protocol: 4}}); err != nil {
			t.Fatal(err)
		}
	}
	record(payload)

	var iowait, await, rx string
	if err := e.pool.QueryRow(ctx, `SELECT system_json->'cpu_detail'->>'iowait_pct', disk_io_json->0->>'await_ms', net_io_json->0->>'rx_bps'
		FROM metrics WHERE host_id = $1`, e.host).Scan(&iowait, &await, &rx); err != nil {
		t.Fatal(err)
	}
	if iowait != "0.1670843776106934" || await != "0.8333333333333334" || rx != "10458.774401897601" {
		t.Errorf("metric row: iowait=%s await=%s rx=%s, want the values as reported", iowait, await, rx)
	}

	st, err := e.hosts.SystemState(ctx, e.host)
	if err != nil || st == nil || *st.OOMKills != 2 || st.OOMLastIncreaseAt != nil || st.Updates.Security != 3 ||
		st.TimeSync == nil || *st.TimeSync.OffsetMs != 2.2285 || len(st.Temperatures) != 2 {
		t.Fatalf("system state = %+v, %v", st, err)
	}

	var services, failed int
	if err := e.pool.QueryRow(ctx, `SELECT count(*), count(*) FILTER (WHERE active = 'failed') FROM host_services WHERE host_id = $1`, e.host).
		Scan(&services, &failed); err != nil || services != 3 || failed != 1 {
		t.Fatalf("services = %d (failed %d), err=%v; want 3 (1)", services, failed, err)
	}
	var unhealthy int
	if err := e.pool.QueryRow(ctx, `SELECT count(*) FROM docker_containers WHERE host_id = $1 AND health = 'unhealthy' AND health_failing_streak = 3`, e.host).
		Scan(&unhealthy); err != nil || unhealthy != 1 {
		t.Fatalf("unhealthy containers = %d, %v", unhealthy, err)
	}

	// İkinci rapor: OOM sayacı arttı; servis listesi kısmi (yalnızca nginx düzeldi).
	time.Sleep(time.Millisecond)
	n := uint64(3)
	payload.MemoryDetail.OOMKills = &n
	payload.Services = &model.Services{Full: false, Items: []model.Service{{Name: "nginx.service", Active: "active", Sub: "running"}}}
	record(payload)
	if st, err := e.hosts.SystemState(ctx, e.host); err != nil || st.OOMLastIncreaseAt == nil {
		t.Fatalf("after the counter increased: %+v, %v", st, err)
	}
	if err := e.pool.QueryRow(ctx, `SELECT count(*), count(*) FILTER (WHERE active = 'failed') FROM host_services WHERE host_id = $1`, e.host).
		Scan(&services, &failed); err != nil || services != 3 || failed != 0 {
		t.Fatalf("after the partial report: services = %d (failed %d), err=%v; want 3 (0)", services, failed, err)
	}
}

// Durum kuralları açıkken v4 raporu durum alert'lerini açar; kural yoksa hiçbiri açılmaz.
func TestRecordRaisesStatusAlerts(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	body, err := os.ReadFile("../../testdata/payloads/v4_health_performance.json")
	if err != nil {
		t.Fatal(err)
	}
	payload, _, err := ingest.Decode(body)
	if err != nil {
		t.Fatal(err)
	}
	record := func() {
		t.Helper()
		time.Sleep(time.Millisecond)
		if err := e.svc.Record(ctx, ingest.Report{HostID: e.host, OrgID: e.org, Source: ingest.SourcePush, Payload: payload,
			Agent: model.AgentInfo{Version: "1.3.0", Protocol: 4}}); err != nil {
			t.Fatal(err)
		}
		e.engine.Flush()
	}
	open := func() map[string]string {
		t.Helper()
		rows, err := e.pool.Query(ctx, `SELECT alert_type || COALESCE('/' || subject, ''), level FROM alerts WHERE host_id = $1 AND status = 'open'`, e.host)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		out := map[string]string{}
		for rows.Next() {
			var k, l string
			if err := rows.Scan(&k, &l); err != nil {
				t.Fatal(err)
			}
			out[k] = l
		}
		return out
	}

	record()
	if got := open(); len(got) != 0 {
		t.Fatalf("alerts without any rule: %v", got)
	}

	th := store.NewThresholds(e.pool)
	if err := th.SetStatusRules(ctx, &e.org, model.StatusRuleChanges{
		model.RuleServiceFailed:      {Level: model.AlertLevelCritical},
		model.RuleContainerUnhealthy: {Level: model.AlertLevelCritical},
		model.RuleContainerOOM:       {Level: model.AlertLevelWarning},
		model.RuleRAIDRebuilding:     {Level: model.AlertLevelWarning},
		model.RuleSecurityUpdates:    {Level: model.AlertLevelInfo},
		model.RuleRebootRequired:     {Level: model.AlertLevelInfo},
	}); err != nil {
		t.Fatal(err)
	}
	if err := e.hosts.SetWatchedServices(ctx, e.host, []string{"nginx.service"}); err != nil {
		t.Fatal(err)
	}
	record()
	want := map[string]string{
		"service_failed/nginx.service": "critical", "container_unhealthy/db": "critical", "container_oom/migrate": "warning",
		"raid_degraded/md0": "warning", "security_updates": "info",
	}
	got := open()
	if len(got) != len(want) {
		t.Fatalf("open alerts = %v, want %v", got, want)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %q, want %q (all: %v)", k, got[k], v, got)
		}
	}

	// OOM: sayaç artınca açılır, artmayan ilk raporda (süre yok) kapanır.
	if err := th.SetStatusRules(ctx, &e.org, model.StatusRuleChanges{model.RuleOOMKill: {Level: model.AlertLevelWarning}}); err != nil {
		t.Fatal(err)
	}
	record() // sayaç aynı (2): artış yok
	if _, ok := open()["oom_kill"]; ok {
		t.Fatal("oom_kill opened without an increase")
	}
	n := uint64(5)
	payload.MemoryDetail.OOMKills = &n
	record()
	if open()["oom_kill"] != "warning" {
		t.Fatalf("oom_kill after the counter rose: %v", open())
	}
	record()
	if _, ok := open()["oom_kill"]; ok {
		t.Fatal("oom_kill still open after a report without an increase")
	}
}
