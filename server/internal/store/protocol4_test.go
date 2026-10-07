package store_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"

	"healthbeat-server/internal/model"
	"healthbeat-server/internal/store"
	"healthbeat-server/internal/testdb"
)

// Protokol 4 zaman serisi metrik satırının kendi sütunlarına yazılır; eski agent'ın satırında bu sütunlar NULL'dır.
func TestMetricsInsertStoresProtocol4Series(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	m := store.NewMetrics(pool)
	host := testdb.PushHost(t, pool, testdb.Org(t, pool, "o"), "h", "x")

	series := model.MetricSeries{
		System: &model.SystemSample{CPUDetail: &model.CPUDetail{IOWaitPct: ptr(1.5)}},
		DiskIO: []model.DiskIO{{Name: "sda", AwaitMs: 2.25}},
		NetIO:  []model.NetIO{{Interface: "eth0", RxBps: 100}},
	}
	if err := m.Insert(ctx, host, 1, 2, nil, series); err != nil {
		t.Fatal(err)
	}
	var iowait, await, rx string
	if err := pool.QueryRow(ctx, `SELECT system_json->'cpu_detail'->>'iowait_pct', disk_io_json->0->>'await_ms', net_io_json->0->>'rx_bps'
		FROM metrics WHERE host_id = $1`, host).Scan(&iowait, &await, &rx); err != nil {
		t.Fatal(err)
	}
	if iowait != "1.5" || await != "2.25" || rx != "100" {
		t.Errorf("stored series: iowait=%s await=%s rx=%s", iowait, await, rx)
	}

	time.Sleep(time.Millisecond) // birincil anahtar (host_id, recorded_at)
	if err := m.Insert(ctx, host, 1, 2, nil, model.MetricSeries{}); err != nil {
		t.Fatal(err)
	}
	var nulls int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM metrics WHERE host_id = $1 AND system_json IS NULL AND disk_io_json IS NULL AND net_io_json IS NULL`, host).
		Scan(&nulls); err != nil || nulls != 1 {
		t.Fatalf("protocol 3 row: nulls=%d err=%v, want 1", nulls, err)
	}
}

// Anlık durum her raporda yazılır: nil (eski agent) önceki durumu siler.
func TestSystemStateIsWrittenOnEveryReport(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	hosts := store.NewHosts(pool, testdb.SecretBox(t))
	host := testdb.PushHost(t, pool, testdb.Org(t, pool, "o"), "h", "x")

	if st, err := hosts.SystemState(ctx, host); err != nil || st != nil {
		t.Fatalf("before any report: %+v, %v", st, err)
	}
	at := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)
	want := &model.SystemState{OOMKills: ptr(uint64(4)), OOMLastIncreaseAt: &at, Updates: &model.Updates{Pending: 2, Security: 1},
		RAID: []model.RAID{{Name: "md0", State: "degraded", Devices: 2, Active: 1}}}
	if err := hosts.MarkOnline(ctx, host, model.Hardware{}, model.AgentInfo{Protocol: 4}, want); err != nil {
		t.Fatal(err)
	}
	got, err := hosts.SystemState(ctx, host)
	if err != nil || got == nil || *got.OOMKills != 4 || !got.OOMLastIncreaseAt.Equal(at) || got.Updates.Security != 1 || got.RAID[0].State != "degraded" {
		t.Fatalf("state = %+v, %v", got, err)
	}

	if err := hosts.MarkOnline(ctx, host, model.Hardware{}, model.AgentInfo{Protocol: 3}, nil); err != nil {
		t.Fatal(err)
	}
	if got, err := hosts.SystemState(ctx, host); err != nil || got != nil {
		t.Fatalf("after a protocol 3 report: %+v, %v; want nil", got, err)
	}
	if _, err := hosts.SystemState(ctx, testdb.Org(t, pool, "not a host")); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("unknown host: err=%v, want ErrNotFound", err)
	}
}

// Tam servis listesi saklananı değiştirir, kısmi liste yalnızca gelenleri günceller; değişmeyen satır yeniden yazılmaz.
func TestSaveServicesFullAndPartial(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	hosts := store.NewHosts(pool, testdb.SecretBox(t))
	org := testdb.Org(t, pool, "o")
	host := testdb.PushHost(t, pool, org, "h", "x")
	other := testdb.PushHost(t, pool, org, "other", "x")

	type row struct {
		active, sub, since, enabled, desc string
		restarts                          *int
		updated                           time.Time
	}
	read := func(h uuid.UUID) map[string]row {
		t.Helper()
		rows, err := pool.Query(ctx, `SELECT name, active, COALESCE(sub, ''), COALESCE(to_char(since AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS"Z"'), ''),
			COALESCE(enabled, ''), COALESCE(description, ''), restarts, updated_at FROM host_services WHERE host_id = $1`, h)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		out := map[string]row{}
		for rows.Next() {
			var name string
			var r row
			if err := rows.Scan(&name, &r.active, &r.sub, &r.since, &r.enabled, &r.desc, &r.restarts, &r.updated); err != nil {
				t.Fatal(err)
			}
			out[name] = r
		}
		return out
	}
	save := func(h uuid.UUID, full bool, items ...model.Service) {
		t.Helper()
		if err := hosts.SaveServices(ctx, h, &model.Services{Full: full, Items: items}); err != nil {
			t.Fatal(err)
		}
	}

	save(host, true,
		model.Service{Name: "a.service", Active: "active", Sub: "running", Since: "2026-10-07T10:00:00Z", Restarts: ptr(0), Enabled: "enabled", Description: "A"},
		model.Service{Name: "b.service", Active: "active", Sub: "running"},
		model.Service{Name: "c.service", Active: "inactive", Sub: "dead"})
	save(other, true, model.Service{Name: "a.service", Active: "failed"})
	first := read(host)
	if len(first) != 3 || first["a.service"].since != "2026-10-07T10:00:00Z" || *first["a.service"].restarts != 0 ||
		first["a.service"].desc != "A" || first["b.service"].restarts != nil || first["b.service"].enabled != "" {
		t.Fatalf("after the full list: %+v", first)
	}

	// Kısmi: b başarısız oldu; a ve c dokunulmadan kalır.
	save(host, false, model.Service{Name: "b.service", Active: "failed", Sub: "failed", Restarts: ptr(3)})
	got := read(host)
	if len(got) != 3 || got["b.service"].active != "failed" || *got["b.service"].restarts != 3 || got["c.service"] != first["c.service"] {
		t.Fatalf("after a partial report: %+v", got)
	}

	// Aynı içerik yeniden gelince satır yazılmaz (updated_at değişmez).
	save(host, false, model.Service{Name: "b.service", Active: "failed", Sub: "failed", Restarts: ptr(3)})
	if again := read(host); !again["b.service"].updated.Equal(got["b.service"].updated) {
		t.Errorf("an unchanged service was rewritten: %v -> %v", got["b.service"].updated, again["b.service"].updated)
	}

	// Tam liste: listede olmayan c silinir; başka sunucunun listesi etkilenmez.
	save(host, true, model.Service{Name: "a.service", Active: "active"}, model.Service{Name: "b.service", Active: "active"})
	if got := read(host); len(got) != 2 || got["b.service"].active != "active" || got["a.service"].since != "" {
		t.Fatalf("after the second full list: %+v", got)
	}
	if o := read(other); len(o) != 1 || o["a.service"].active != "failed" {
		t.Fatalf("other host's services changed: %+v", o)
	}

	// Bilinmiyor (nil): hiçbir şey değişmez.
	if err := hosts.SaveServices(ctx, host, nil); err != nil || len(read(host)) != 2 {
		t.Fatalf("nil services: err=%v rows=%d", err, len(read(host)))
	}
}

// Docker sağlık alanları saklanır; bilinmeyen sağlık değeri ya da negatif sayaç container'ı reddettirmez.
func TestDockerContainersStoreHealth(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	m := store.NewMetrics(pool)
	host := testdb.PushHost(t, pool, testdb.Org(t, pool, "o"), "h", "x")

	err := m.ReplaceDockerContainers(ctx, host, []model.DockerContainerReport{
		{Name: "api", Image: "i", Status: "running", Health: "unhealthy", HealthFailingStreak: ptr(3)},
		{Name: "job", Image: "i", Status: "exited", ExitCode: ptr(137), OOMKilled: ptr(true)},
		{Name: "odd", Image: "i", Status: "running", Health: "sick", HealthFailingStreak: ptr(-1)},
	})
	if err != nil {
		t.Fatal(err)
	}
	rows, err := pool.Query(ctx, `SELECT name, COALESCE(health, '-'), COALESCE(health_failing_streak, -9), COALESCE(exit_code, -9),
		COALESCE(oom_killed::text, '-') FROM docker_containers WHERE host_id = $1 ORDER BY name`, host)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var got []string
	for rows.Next() {
		var name, health, oom string
		var streak, exit int
		if err := rows.Scan(&name, &health, &streak, &exit, &oom); err != nil {
			t.Fatal(err)
		}
		got = append(got, fmt.Sprintf("%s:%s:%d:%d:%s", name, health, streak, exit, oom))
	}
	want := []string{"api:unhealthy:3:-9:-", "job:-:-9:137:true", "odd:-:-9:-9:-"}
	if len(got) != len(want) {
		t.Fatalf("containers = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("container %d = %s, want %s", i, got[i], want[i])
		}
	}
}

// İzlenen servis seçimi sunucuya özeldir, tamamen değiştirilir ve raporlanmayan adları da tutar.
func TestWatchedServices(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	hosts := store.NewHosts(pool, testdb.SecretBox(t))
	org := testdb.Org(t, pool, "o")
	host := testdb.PushHost(t, pool, org, "h", "x")
	other := testdb.PushHost(t, pool, org, "other", "x")

	if err := hosts.SaveServices(ctx, host, &model.Services{Full: true, Items: []model.Service{
		{Name: "a.service", Active: "active"}, {Name: "b.service", Active: "failed"}}}); err != nil {
		t.Fatal(err)
	}
	if err := hosts.SetWatchedServices(ctx, host, []string{"b.service", "gone.service"}); err != nil {
		t.Fatal(err)
	}
	if err := hosts.SetWatchedServices(ctx, other, []string{"a.service"}); err != nil {
		t.Fatal(err)
	}
	watched, err := hosts.WatchedServices(ctx, host)
	if err != nil || fmt.Sprint(watched) != "[b.service gone.service]" {
		t.Fatalf("watched = %v, %v", watched, err)
	}
	services, err := hosts.Services(ctx, host)
	if err != nil || len(services) != 2 || services[0].Watched || !services[1].Watched {
		t.Fatalf("services = %+v, %v", services, err)
	}

	if err := hosts.SetWatchedServices(ctx, host, nil); err != nil {
		t.Fatal(err)
	}
	if watched, err := hosts.WatchedServices(ctx, host); err != nil || len(watched) != 0 {
		t.Fatalf("after clearing: %v, %v", watched, err)
	}
	if watched, _ := hosts.WatchedServices(ctx, other); fmt.Sprint(watched) != "[a.service]" {
		t.Fatalf("other host's selection changed: %v", watched)
	}
	if err := hosts.SetWatchedServices(ctx, org, []string{"x"}); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("unknown host: err=%v, want ErrNotFound", err)
	}
}

// Servis sayacı arttıkça [an, önceki, yeni] eklenir; ilk görülen değer ve geri giden sayaç artış sayılmaz; değişmeyen
// rapor geçmişe dokunmaz; 1 saatten eski kayıtlar sonraki artışta budanır.
func TestServiceRestartHistory(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	hosts := store.NewHosts(pool, testdb.SecretBox(t))
	host := testdb.PushHost(t, pool, testdb.Org(t, pool, "o"), "h", "x")
	save := func(restarts *int) {
		t.Helper()
		if err := hosts.SaveServices(ctx, host, &model.Services{Items: []model.Service{{Name: "app.service", Active: "active", Restarts: restarts}}}); err != nil {
			t.Fatal(err)
		}
	}
	history := func() [][3]int64 {
		t.Helper()
		services, err := hosts.Services(ctx, host)
		if err != nil || len(services) != 1 {
			t.Fatalf("services = %+v, %v", services, err)
		}
		return services[0].RestartHistory
	}

	save(ptr(3))
	if h := history(); len(h) != 0 {
		t.Fatalf("first seen = %v, want empty (the counter may have grown before)", h)
	}
	save(ptr(5))
	h := history()
	if len(h) != 1 || h[0][1] != 3 || h[0][2] != 5 || time.Since(time.Unix(h[0][0], 0)) > time.Minute {
		t.Fatalf("after 3→5 = %v", h)
	}
	save(ptr(5))
	save(nil) // sayaç bilinmiyor: geçmiş ve son bilinen değer korunur
	if h := history(); len(h) != 1 {
		t.Fatalf("unchanged/unknown counter changed history: %v", h)
	}
	if services, _ := hosts.Services(ctx, host); services[0].Restarts == nil || *services[0].Restarts != 5 {
		t.Fatalf("restarts after an unknown report = %v, want the last known 5", services[0].Restarts)
	}
	save(ptr(6))
	if h := history(); len(h) != 2 || h[1][1] != 5 || h[1][2] != 6 {
		t.Fatalf("after 5→6 = %v", h)
	}
	save(ptr(1)) // sayaç geri gitti
	if h := history(); len(h) != 0 {
		t.Fatalf("after the counter went back = %v, want empty", h)
	}

	// 1 saatten eski kayıt bir sonraki artışta budanır.
	old := time.Now().Add(-2 * time.Hour).Unix()
	if _, err := pool.Exec(ctx, `UPDATE host_services SET restart_history = jsonb_build_array(jsonb_build_array($2::bigint, 0, 1)) WHERE host_id = $1`, host, old); err != nil {
		t.Fatal(err)
	}
	save(ptr(2))
	if h := history(); len(h) != 1 || h[0][1] != 1 || h[0][2] != 2 {
		t.Fatalf("after pruning = %v, want only the new 1→2 entry", h)
	}
}
