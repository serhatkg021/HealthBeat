package retention

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"healthbeat-server/internal/store"
	"healthbeat-server/internal/testdb"
)

func count(t *testing.T, pool *pgxpool.Pool, query string, args ...any) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(), query, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

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

	p := New(Stores{Metrics: store.NewMetrics(pool)}, Days{Metrics: 30})
	n, err := p.RunOnce(ctx)
	if err != nil || n["metric samples"] != 2 {
		t.Fatalf("RunOnce = %v err=%v, want 2 metric samples (the 31- and 90-day-old ones)", n, err)
	}
	if left := count(t, pool, `SELECT count(*) FROM metrics`); left != 2 {
		t.Fatalf("%d samples left, want the 2 within 30 days", left)
	}
}

// Denetim kayıtları oluşturulma zamanına göre silinir.
func TestRunOnceDeletesExpiredAuditEntries(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	for _, age := range []string{"1 day", "364 days", "366 days", "800 days"} {
		if _, err := pool.Exec(ctx,
			`INSERT INTO audit_logs (actor_email, action, target_type, created_at) VALUES ('a@x.test', 'auth.login', 'user', now() - $1::interval)`,
			age); err != nil {
			t.Fatal(err)
		}
	}
	n, err := New(Stores{Audit: store.NewAudit(pool)}, Days{Audit: 365}).RunOnce(ctx)
	if err != nil || n["audit log entries"] != 2 {
		t.Fatalf("RunOnce = %v err=%v, want 2 audit log entries", n, err)
	}
	if left := count(t, pool, `SELECT count(*) FROM audit_logs`); left != 2 {
		t.Fatalf("%d audit entries left, want the 2 within 365 days", left)
	}
}

// Yalnızca süresi dolmuş ÇÖZÜLMÜŞ alert'ler silinir; açık ve onaylanmış alert'ler ne kadar eski olursa olsun kalır.
// Silinen alert'in bildirim satırı kalır ama alert'e bağı kopar (outbox temizliği onu sonra siler).
func TestRunOnceDeletesOnlyOldResolvedAlerts(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	host := testdb.PushHost(t, pool, testdb.Org(t, pool, "A"), "c", "h")
	insert := func(subject, status, resolvedAgo string) uuid.UUID {
		t.Helper()
		var id uuid.UUID
		if err := pool.QueryRow(ctx,
			`INSERT INTO alerts (host_id, alert_type, subject, level, status, created_at, resolved_at)
			 VALUES ($1, 'disk', $2, 'warning', $3, now() - interval '400 days',
			         CASE WHEN $4::text = '' THEN NULL ELSE now() - $4::interval END)
			 RETURNING id`, host, subject, status, resolvedAgo).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	old := insert("/old", "resolved", "200 days")
	insert("/recent", "resolved", "10 days")
	insert("/open", "open", "")
	insert("/acked", "acknowledged", "")
	if _, err := pool.Exec(ctx,
		`INSERT INTO notification_outbox (id, kind, channel, recipients, subject, body, alert_id, alert_event, alert_level, sent_at)
		 VALUES (gen_random_uuid(), 'alert', 'email', '{a@x.test}', 's', 'b', $1, 'resolved', 'warning', now())`, old); err != nil {
		t.Fatal(err)
	}

	n, err := New(Stores{Alerts: store.NewAlerts(pool)}, Days{ResolvedAlerts: 180}).RunOnce(ctx)
	if err != nil || n["resolved alerts"] != 1 {
		t.Fatalf("RunOnce = %v err=%v, want 1 resolved alert", n, err)
	}
	if left := count(t, pool, `SELECT count(*) FROM alerts WHERE subject <> '/old'`); left != 3 {
		t.Fatalf("%d other alerts left, want 3 (recent resolved, open, acknowledged)", left)
	}
	if orphans := count(t, pool, `SELECT count(*) FROM notification_outbox WHERE alert_id IS NULL`); orphans != 1 {
		t.Fatalf("orphaned notification rows = %d, want 1 (kept until the outbox purge)", orphans)
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
	if _, err := pool.Exec(ctx,
		`INSERT INTO audit_logs (actor_email, action, target_type, created_at) VALUES ('a@x.test', 'auth.login', 'user', now() - interval '4000 days')`); err != nil {
		t.Fatal(err)
	}
	p := New(Stores{Metrics: store.NewMetrics(pool), Audit: store.NewAudit(pool), Alerts: store.NewAlerts(pool)}, Days{})
	if n, err := p.RunOnce(ctx); err != nil || len(n) != 0 {
		t.Fatalf("RunOnce with retention disabled = %v err=%v", n, err)
	}
	if left := count(t, pool, `SELECT count(*) FROM metrics`) + count(t, pool, `SELECT count(*) FROM audit_logs`); left != 2 {
		t.Fatal("a row was deleted although retention is disabled")
	}

	// Süre panelden sonradan açılırsa bir sonraki tur onu kullanır.
	p.SetDays(Days{Audit: 365})
	if n, err := p.RunOnce(ctx); err != nil || n["audit log entries"] != 1 || n["metric samples"] != 0 {
		t.Fatalf("RunOnce after SetDays = %v err=%v, want only the old audit entry deleted", n, err)
	}
	if left := count(t, pool, `SELECT count(*) FROM metrics`); left != 1 {
		t.Fatalf("%d metric rows left, want 1 (metrics retention still 0)", left)
	}
}

func TestRunStopsWhenContextIsCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	p := New(Stores{Metrics: store.NewMetrics(nil)}, Days{Metrics: 30}) // temizlik iptalden önce asla tetiklenmez
	go func() { p.Run(ctx); close(done) }()
	cancel()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("Run did not return after its context was cancelled")
	}
}
