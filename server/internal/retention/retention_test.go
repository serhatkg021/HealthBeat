package retention

import (
	"context"
	"testing"
	"time"

	"healthbeat-server/internal/store"
	"healthbeat-server/internal/testdb"
)

func TestRunOnceDeletesOnlyExpiredSamples(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	host := testdb.PushHost(t, pool, testdb.Org(t, pool, "A"), "c", "h")

	for _, age := range []string{"1 hour", "29 days", "31 days", "90 days"} {
		if _, err := pool.Exec(ctx,
			`INSERT INTO metrics (host_id, recorded_at, cpu_usage_pct, ram_usage_pct, disk_json) VALUES ($1, now() - $2::interval, 1, 1, '[]')`,
			host, age); err != nil {
			t.Fatal(err)
		}
	}

	p := New(store.NewMetrics(pool), 30)
	n, err := p.RunOnce(ctx)
	if err != nil || n != 2 {
		t.Fatalf("RunOnce = %d err=%v, want 2 (the 31- and 90-day-old samples)", n, err)
	}
	var left int
	pool.QueryRow(ctx, `SELECT count(*) FROM metrics`).Scan(&left)
	if left != 2 {
		t.Fatalf("%d samples left, want the 2 within 30 days", left)
	}
}

func TestZeroDaysKeepsEverything(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	host := testdb.PushHost(t, pool, testdb.Org(t, pool, "A"), "c", "h")
	if _, err := pool.Exec(ctx,
		`INSERT INTO metrics (host_id, recorded_at, cpu_usage_pct, ram_usage_pct, disk_json) VALUES ($1, now() - interval '400 days', 1, 1, '[]')`, host); err != nil {
		t.Fatal(err)
	}
	if n, err := New(store.NewMetrics(pool), 0).RunOnce(ctx); err != nil || n != 0 {
		t.Fatalf("RunOnce with retention disabled = %d err=%v", n, err)
	}
	var left int
	pool.QueryRow(ctx, `SELECT count(*) FROM metrics`).Scan(&left)
	if left != 1 {
		t.Fatal("a sample was deleted although retention is disabled")
	}
}

func TestRunStopsWhenContextIsCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { New(nil, 30).Run(ctx); close(done) }() // temizlik iptalden önce asla tetiklenmez
	cancel()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("Run did not return after its context was cancelled")
	}
}
