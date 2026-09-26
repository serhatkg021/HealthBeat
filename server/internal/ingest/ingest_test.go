package ingest_test

import (
	"context"
	"errors"
	"slices"
	"testing"

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
