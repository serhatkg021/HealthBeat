package model

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func parseV4Fixture(t *testing.T) MetricsIngestRequest {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(payloadDir, "v4_health_performance.json"))
	if err != nil {
		t.Fatal(err)
	}
	req, unknown, err := ParseMetricsIngest(data)
	if err != nil || unknown != nil {
		t.Fatalf("parse v4 fixture: unknown=%v err=%v", unknown, err)
	}
	return req
}

// Protokol 4 fixture'ının her bölümü gönderildiği değerle çözülür.
func TestParseMetricsIngestV4Fixture(t *testing.T) {
	r := parseV4Fixture(t)
	if r.CPUDetail == nil || *r.CPUDetail.IOWaitPct != 0.1670843776106934 {
		t.Errorf("cpu_detail = %+v", r.CPUDetail)
	}
	if r.MemoryDetail == nil || *r.MemoryDetail.OOMKills != 2 || *r.MemoryDetail.AvailableMB != 11269 {
		t.Errorf("memory_detail = %+v", r.MemoryDetail)
	}
	if r.Pressure == nil || r.Pressure.IO == nil || r.Pressure.IO.Some10 != 0.49 || r.Pressure.CPU.Full10 != nil {
		t.Errorf("pressure = %+v", r.Pressure)
	}
	if len(r.DiskIO) != 1 || r.DiskIO[0].AwaitMs != 0.8333333333333334 || len(r.NetIO) != 2 || r.TCP == nil {
		t.Errorf("disk_io/net_io/tcp = %+v %+v %+v", r.DiskIO, r.NetIO, r.TCP)
	}
	if len(r.Temperatures) != 2 || len(r.RAID) != 1 || r.RAID[0].State != "recovering" {
		t.Errorf("temperatures/raid = %+v %+v", r.Temperatures, r.RAID)
	}
	if r.Capacity == nil || *r.Capacity.FileHandlesMax != 9223372036854775807 {
		t.Errorf("capacity = %+v", r.Capacity)
	}
	if r.Processes == nil || r.Processes.Total != 412 || len(r.Processes.TopRAM) != 2 {
		t.Errorf("processes = %+v", r.Processes)
	}
	if r.Updates == nil || r.Updates.Security != 3 || r.Updates.ListsUpdatedAt != "2026-10-05T16:20:48Z" {
		t.Errorf("updates = %+v", r.Updates)
	}
	if r.Services == nil || !r.Services.Full || len(r.Services.Items) != 3 || *r.Services.Items[1].Restarts != 5 {
		t.Errorf("services = %+v", r.Services)
	}
	if r.TimeSync == nil || r.TimeSync.Daemon != "timesyncd" || len(r.TimeSync.Sources) != 1 || *r.TimeSync.OffsetMs != 2.2285 {
		t.Errorf("time_sync = %+v", r.TimeSync)
	}
	if ro := r.Disk[0].ReadOnly; ro == nil || *ro {
		t.Errorf("disk read_only = %v", ro)
	}
	db := r.DockerContainers[1]
	if db.Health != "unhealthy" || db.HealthFailingStreak == nil || *db.HealthFailingStreak != 3 {
		t.Errorf("docker health = %+v", db)
	}
	if m := r.DockerContainers[2]; m.ExitCode == nil || *m.ExitCode != 137 || m.OOMKilled == nil || !*m.OOMKilled {
		t.Errorf("docker exit/oom = %+v", m)
	}
}

// Yanlış tipteki bir protokol 4 bölümü raporu reddettirmez: yalnızca o bölüm atılır, gerisi alınır.
func TestParseMetricsIngestDropsOnlyBrokenV4Sections(t *testing.T) {
	body := `{"cpu_usage_pct": 10, "ram_usage_pct": 20, "disk": [], "docker_containers": [],
		"cpu_detail": "yüksek", "disk_io": {"name": "sda"}, "services": [1, 2], "time_sync": 5,
		"tcp": {"established": 10}, "net_io": [{"interface": "eth0", "rx_bps": 1, "tx_bps": 2}]}`
	r, unknown, err := ParseMetricsIngest([]byte(body))
	if err != nil || unknown != nil {
		t.Fatalf("err=%v unknown=%v, want the report accepted", err, unknown)
	}
	if r.CPUUsagePct != 10 || r.CPUDetail != nil || r.DiskIO != nil || r.Services != nil || r.TimeSync != nil {
		t.Errorf("broken sections kept: %+v", r)
	}
	if r.TCP == nil || *r.TCP.Established != 10 || len(r.NetIO) != 1 {
		t.Errorf("valid sections lost: tcp=%+v net_io=%+v", r.TCP, r.NetIO)
	}
}

func ptr[T any](v T) *T { return &v }

// Aralık dışı sayılar yalnızca kendi alanını ya da girdisini düşürür; metinler temizlenir, listeler sınırlanır.
func TestSanitizeV4(t *testing.T) {
	r := MetricsIngestRequest{
		CPUDetail:    &CPUDetail{IOWaitPct: ptr(140.0), StealPct: ptr(2.0), ProcsBlocked: ptr(-1)},
		MemoryDetail: &MemoryDetail{AvailableMB: ptr(int64(-5))},
		Pressure:     &Pressure{CPU: &PressureStall{Some10: 101}, IO: &PressureStall{Some10: 1, Some60: 2, Full10: ptr(-1.0)}},
		DiskIO: []DiskIO{
			{Name: "sda", UtilPct: 120}, {Name: " nvme0n1\x00 ", AwaitMs: 3}, {Name: "nvme0n1", AwaitMs: 4}, {Name: ""},
		},
		NetIO:        []NetIO{{Interface: "eth0", RxBps: -1}, {Interface: "eth1", RxBps: 5}},
		TCP:          &TCP{RetransPct: ptr(200.0)},
		Temperatures: []Temperature{{Sensor: "a", Kind: "gpu", Celsius: 50, Crit: ptr(9000.0)}, {Sensor: "b", Celsius: -300}},
		RAID:         []RAID{{Name: "md0", State: ""}, {Name: "md1", State: "degraded", Devices: 2, Active: 1, SyncPct: ptr(150.0)}},
		Capacity:     &Capacity{Conntrack: ptr(int64(-1))},
		Processes:    &Processes{Total: 5, TopCPU: []ProcessGroup{{Name: "x", Count: 0}, {Name: "y", Count: 1, CPUPct: 3}}},
		Updates:      &Updates{Pending: 3, Security: -1},
		Services:     &Services{Full: false, Items: []Service{{Name: "a.service", Active: "failed", Since: "dün", Restarts: ptr(-2)}, {Name: "b.service"}}},
		TimeSync:     &TimeSync{Daemon: "systemd-magic", Leap: "alarm", Stratum: ptr(17), Sources: []TimeSource{{Name: "x"}}},
	}
	sanitizeV4(&r)

	if c := r.CPUDetail; c == nil || c.IOWaitPct != nil || *c.StealPct != 2 || c.ProcsBlocked != nil {
		t.Errorf("cpu_detail = %+v", c)
	}
	if r.MemoryDetail != nil {
		t.Errorf("memory_detail = %+v, want nil (nothing valid)", r.MemoryDetail)
	}
	if p := r.Pressure; p == nil || p.CPU != nil || p.IO == nil || p.IO.Full10 != nil {
		t.Errorf("pressure = %+v", p)
	}
	if len(r.DiskIO) != 1 || r.DiskIO[0].Name != "nvme0n1" || r.DiskIO[0].AwaitMs != 3 {
		t.Errorf("disk_io = %+v, want only the first nvme0n1 (cleaned, deduplicated)", r.DiskIO)
	}
	if len(r.NetIO) != 1 || r.NetIO[0].Interface != "eth1" {
		t.Errorf("net_io = %+v", r.NetIO)
	}
	if r.TCP != nil {
		t.Errorf("tcp = %+v, want nil", r.TCP)
	}
	if len(r.Temperatures) != 1 || r.Temperatures[0].Kind != "other" || r.Temperatures[0].Crit != nil {
		t.Errorf("temperatures = %+v", r.Temperatures)
	}
	if len(r.RAID) != 1 || r.RAID[0].Name != "md1" || r.RAID[0].SyncPct != nil {
		t.Errorf("raid = %+v", r.RAID)
	}
	if r.Capacity != nil || r.Updates != nil {
		t.Errorf("capacity=%+v updates=%+v, want nil", r.Capacity, r.Updates)
	}
	if r.Processes == nil || len(r.Processes.TopCPU) != 1 || r.Processes.TopCPU[0].Name != "y" {
		t.Errorf("processes = %+v", r.Processes)
	}
	if s := r.Services; s == nil || len(s.Items) != 1 || s.Items[0].Since != "" || s.Items[0].Restarts != nil {
		t.Errorf("services = %+v", s)
	}
	if ts := r.TimeSync; ts == nil || ts.Daemon != "" || ts.Leap != "alarm" || ts.Stratum != nil || len(ts.Sources) != 0 {
		t.Errorf("time_sync = %+v", ts)
	}
}

func TestSanitizeV4Limits(t *testing.T) {
	var items []Service
	for i := 0; i < maxServices+10; i++ {
		items = append(items, Service{Name: strings.Repeat("s", 300) + string(rune('a'+i%26)) + strings.Repeat("x", i), Active: "active"})
	}
	var temps []Temperature
	for i := 0; i < maxV4Entries+5; i++ {
		temps = append(temps, Temperature{Sensor: "t" + strings.Repeat("x", i), Celsius: 40})
	}
	r := MetricsIngestRequest{Services: &Services{Full: true, Items: items}, Temperatures: temps}
	sanitizeV4(&r)
	if len(r.Temperatures) != maxV4Entries {
		t.Errorf("temperatures = %d, cap %d", len(r.Temperatures), maxV4Entries)
	}
	// Uzun adlar kırpılınca birbirinin aynısı olur: tekrar edenler atılır, liste yine en çok maxServices.
	if r.Services == nil || len(r.Services.Items) > maxServices || len(r.Services.Items[0].Name) > maxServiceText {
		t.Errorf("services not limited: %d items", len(r.Services.Items))
	}

	// Boş tam liste "bilinmiyor"dur (saklanan listeyi silmemeli); boş kısmi liste ise "değişiklik yok".
	empty := MetricsIngestRequest{Services: &Services{Full: true}}
	sanitizeV4(&empty)
	if empty.Services != nil {
		t.Errorf("empty full service list = %+v, want nil", empty.Services)
	}
	partial := MetricsIngestRequest{Services: &Services{Full: false}}
	sanitizeV4(&partial)
	if partial.Services == nil || len(partial.Services.Items) != 0 {
		t.Errorf("empty partial service list = %+v, want kept", partial.Services)
	}
}

// Zaman serisi ölçüldüğü gibi saklanır (yuvarlama panelde); OOM sayacı zaman serisine girmez, anlık durumdadır.
func TestSeriesStoresRawValues(t *testing.T) {
	r := parseV4Fixture(t)
	s := r.Series()
	if s.System == nil || *s.System.CPUDetail.IOWaitPct != 0.1670843776106934 || s.System.Pressure.IO.Some10 != 0.49 ||
		*s.System.MemoryDetail.SwapOutPerS != 0.4166666666666667 || s.System.MemoryDetail.OOMKills != nil || s.System.TCP == nil {
		t.Errorf("system sample = %+v", s.System)
	}
	if r.MemoryDetail.OOMKills == nil {
		t.Error("Series changed the request's oom_kills")
	}
	if d := s.DiskIO[0]; d.AwaitMs != 0.8333333333333334 || d.WriteBps != 12184.055730993337 {
		t.Errorf("disk_io = %+v", d)
	}
	if n := s.NetIO[0]; n.RxBps != 10458.774401897601 {
		t.Errorf("net_io = %+v", n)
	}
	if empty := (MetricsIngestRequest{}).Series(); empty.System != nil || empty.DiskIO != nil || empty.NetIO != nil {
		t.Errorf("series of a protocol 3 report = %+v, want empty", empty)
	}
	// Yalnızca OOM sayacı varsa bellek ayrıntısı zaman serisine girmez.
	if only := (MetricsIngestRequest{MemoryDetail: &MemoryDetail{OOMKills: ptr(uint64(1))}}).Series(); only.System != nil {
		t.Errorf("series with only oom_kills = %+v, want empty", only.System)
	}
}

func TestNextSystemState(t *testing.T) {
	r := parseV4Fixture(t)
	t0 := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)

	s := NextSystemState(nil, r, t0)
	if s == nil || *s.OOMKills != 2 || s.OOMLastIncreaseAt != nil {
		t.Fatalf("first state = %+v, want oom_kills=2 without an increase (no baseline)", s)
	}
	if s.Temperatures[0].Celsius != 32.85 || *s.RAID[0].SyncPct != 37.54 || s.Processes.TopCPU[0].CPUPct != 1.2345 ||
		*s.TimeSync.OffsetMs != 2.2285 || s.Updates.Security != 3 || *s.Capacity.Tasks != 2277 {
		t.Errorf("state not stored as reported: %+v", s)
	}

	// Sayaç arttı: artış anı kaydedilir; sonraki raporlar artmadıkça aynı anı taşır.
	r.MemoryDetail.OOMKills = ptr(uint64(3))
	t1 := t0.Add(time.Minute)
	s = NextSystemState(s, r, t1)
	if s.OOMLastIncreaseAt == nil || !s.OOMLastIncreaseAt.Equal(t1) {
		t.Fatalf("after an increase: %+v", s.OOMLastIncreaseAt)
	}
	s = NextSystemState(s, r, t1.Add(time.Minute))
	if !s.OOMLastIncreaseAt.Equal(t1) {
		t.Errorf("unchanged counter moved the increase time: %v", s.OOMLastIncreaseAt)
	}
	// Yeniden açılış: sayaç geri gider, artış sayılmaz.
	r.MemoryDetail.OOMKills = ptr(uint64(0))
	s = NextSystemState(s, r, t1.Add(2*time.Minute))
	if *s.OOMKills != 0 || !s.OOMLastIncreaseAt.Equal(t1) {
		t.Errorf("after a reboot: %+v at %v", *s.OOMKills, s.OOMLastIncreaseAt)
	}

	// Protokol 3 raporu: anlık durum bilinmiyor.
	if got := NextSystemState(s, MetricsIngestRequest{CPUUsagePct: 1}, t1); got != nil {
		t.Errorf("state of a protocol 3 report = %+v, want nil", got)
	}
}

// Protokol 4 alanları bilinen alanlardır: eklenince "server güncellenmeli" listesine düşmemeli.
func TestV4FieldsAreKnown(t *testing.T) {
	var fields []string
	tt := reflect.TypeOf(ingestWire{})
	for i := 0; i < tt.NumField(); i++ {
		if name, _, _ := strings.Cut(tt.Field(i).Tag.Get("json"), ","); name != "" {
			fields = append(fields, name)
			if _, ok := knownIngestFields[name]; !ok {
				t.Errorf("%q is decoded but not a known field", name)
			}
		}
	}
	if len(fields) != 13 {
		t.Errorf("ingestWire has %d protocol 4 sections, want 13", len(fields))
	}
}

// Yeniden başlatma sayısı, pencere içindeki sayaç artışlarının toplamıdır.
func TestHostServiceRestartsSince(t *testing.T) {
	now := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)
	s := HostService{RestartHistory: [][3]int64{
		{now.Add(-20 * time.Minute).Unix(), 0, 4}, // pencere dışı
		{now.Add(-9 * time.Minute).Unix(), 4, 6},
		{now.Add(-time.Minute).Unix(), 6, 7},
	}}
	if got := s.RestartsSince(now.Add(-10 * time.Minute)); got != 3 {
		t.Errorf("restarts in 10 min = %d, want 3", got)
	}
	if got := (HostService{}).RestartsSince(now); got != 0 {
		t.Errorf("no history = %d", got)
	}
}
