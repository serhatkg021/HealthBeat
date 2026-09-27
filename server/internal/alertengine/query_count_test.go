package alertengine_test

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"healthbeat-server/internal/alertengine"
	"healthbeat-server/internal/model"
	"healthbeat-server/internal/notify"
	"healthbeat-server/internal/testdb"
)

// queryCounter, havuzdan geçen sorguları sayar.
type queryCounter struct{ n atomic.Int64 }

func (c *queryCounter) TraceQueryStart(ctx context.Context, _ *pgx.Conn, _ pgx.TraceQueryStartData) context.Context {
	c.n.Add(1)
	return ctx
}

func (c *queryCounter) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}

// Durum değişmeyen bir rapor, mount ve container sayısından bağımsız olarak üç sorgu çalıştırır: eşikler, aktif
// alert'ler ve disk seçimi rapor başına bir kez okunur (kalem başına değil).
func TestEvaluateReportReadsOncePerReport(t *testing.T) {
	ctx := context.Background()
	cfg := testdb.New(t).Config()
	counter := &queryCounter{}
	cfg.ConnConfig.Tracer = counter
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)

	org := testdb.Org(t, pool, "acme")
	host := testdb.PushHost(t, pool, org, "web-1", "h")
	for _, m := range []string{model.MetricTypeCPU, model.MetricTypeRAM, model.MetricTypeDisk, model.MetricTypeDockerRestart} {
		testdb.Threshold(t, pool, &org, nil, m, 80, 90)
	}
	engine := alertengine.New(pool, notify.New(notify.Config{}), "")

	for _, n := range []int{2, 20} {
		report := alertengine.Report{CPUPct: 85, RAMPct: 10} // cpu uyarıda: aktif bir alert de okunur
		for i := 0; i < n; i++ {
			report.Disks = append(report.Disks, model.DiskUsage{Mount: fmt.Sprintf("/m%d", i), UsedPct: 10, Total: 100, Free: 90})
			report.Containers = append(report.Containers, model.DockerContainerReport{Name: fmt.Sprintf("c%d", i), Status: "running"})
		}
		engine.EvaluateReport(ctx, host, org, report) // ilk rapor cpu alert'ini açar
		counter.n.Store(0)
		engine.EvaluateReport(ctx, host, org, report)
		if got := counter.n.Load(); got != 3 {
			t.Errorf("%d mounts and containers: %d queries per unchanged report, want 3", n, got)
		}
	}
}
