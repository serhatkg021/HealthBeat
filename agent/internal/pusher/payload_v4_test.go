package pusher

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"healthbeat-agent/internal/collector"
	"healthbeat-agent/internal/version"
)

// v4Keys, protokol 4'ün eklediği JSON alan adlarıdır (üst düzey ve iç içe).
var v4Keys = []string{
	"cpu_detail", "memory_detail", "pressure", "disk_io", "net_io", "tcp", "temperatures", "raid", "capacity",
	"processes", "updates", "services", "time_sync", "read_only", "health", "health_failing_streak", "exit_code", "oom_killed",
}

func ptr[T any](v T) *T { return &v }

// withV4, payload'ın her protokol 4 alanını doldurur (tasarım belgesindeki örnek).
func withV4(p *MetricsPayload) {
	for i := range p.Disk {
		p.Disk[i].ReadOnly = ptr(true)
	}
	containers := make([]DockerContainer, len(p.DockerContainers))
	copy(containers, p.DockerContainers)
	for i := range containers {
		containers[i].Health, containers[i].HealthFailingStreak = "unhealthy", ptr(3)
		containers[i].ExitCode, containers[i].OOMKilled = ptr(137), ptr(true)
	}
	p.DockerContainers = containers
	p.CPUDetail = &CPUDetail{IOWaitPct: ptr(1.2), StealPct: ptr(0.4), ProcsBlocked: ptr(0)}
	p.MemoryDetail = &MemoryDetail{AvailableMB: ptr(int64(9210)), CachedMB: ptr(int64(5120)), SwapInPerS: ptr(0.0), SwapOutPerS: ptr(0.0), OOMKills: ptr(uint64(3))}
	p.Pressure = &Pressure{CPU: &PressureStall{Some10: 2.1, Some60: 1.4}, IO: &PressureStall{Some10: 4, Some60: 3.2, Full10: ptr(1.1), Full60: ptr(0.8)}}
	p.DiskIO = []DiskIO{{Name: "nvme0n1", ReadIOPS: 120.5, WriteIOPS: 340.2, ReadBps: 8912896, WriteBps: 26214400, UtilPct: 18.4, AwaitMs: 0.4, QueueDepth: 0.3}}
	p.NetIO = []NetIO{{Interface: "enp3s0", RxBps: 42e6, TxBps: 8.1e6, RxDrops: 12}}
	p.TCP = &TCP{RetransPct: ptr(0.3), Established: ptr(182), TimeWait: ptr(40)}
	p.Temperatures = []Temperature{{Sensor: "coretemp/Package id 0", Kind: "cpu", Celsius: 64, Max: ptr(100.0), Crit: ptr(100.0)}}
	p.RAID = []RAID{{Name: "md0", Level: "raid1", State: "degraded", Devices: 2, Active: 1, SyncPct: ptr(37.5)}}
	p.Capacity = &Capacity{FileHandles: ptr(int64(12705)), FileHandlesMax: ptr(int64(1048576)), Processes: ptr(int64(312)), PIDMax: ptr(int64(4194304))}
	p.Processes = &Processes{Total: 312, TopCPU: []ProcessGroup{{Name: "postgres", Count: 23, CPUPct: 38.2, RSSMB: 4198}}}
	p.Updates = &Updates{Pending: 14, Security: 3, CheckedAt: "2026-10-06T18:00:00Z"}
	p.Services = &Services{Items: []Service{{Name: "postgresql.service", Active: "failed", Sub: "failed", Restarts: ptr(3)}}}
	p.TimeSync = &TimeSync{Enabled: ptr(true), Synchronized: ptr(true), Daemon: "timesyncd", OffsetMs: ptr(1.841),
		Sources: []TimeSource{{Name: "185.125.190.57", State: "selected", Reach: ptr(255)}}}
}

// hasV4, çekirdek dışı protokol 4 alanlarından herhangi biri dolu mu.
func hasV4(p MetricsPayload) bool {
	if p.CPUDetail != nil || p.MemoryDetail != nil || p.Pressure != nil || p.DiskIO != nil || p.NetIO != nil || p.TCP != nil ||
		p.Temperatures != nil || p.RAID != nil || p.Capacity != nil || p.Processes != nil || p.Updates != nil ||
		p.Services != nil || p.TimeSync != nil {
		return true
	}
	for _, d := range p.Disk {
		if d.ReadOnly != nil {
			return true
		}
	}
	for _, c := range p.DockerContainers {
		if c.Health != "" || c.HealthFailingStreak != nil || c.ExitCode != nil || c.OOMKilled != nil {
			return true
		}
	}
	return false
}

func TestProtocolIs4(t *testing.T) {
	if version.Protocol != 4 {
		t.Fatalf("Protocol = %d; the v4 fields are defined, the protocol must say so", version.Protocol)
	}
}

// Bilinmeyen (doldurulmamış) protokol 4 alanı JSON'a hiç yazılmaz: server onu "bilinmiyor" sayar, 0/boş ile karıştırmaz.
func TestV4FieldsAreOmittedWhenUnknown(t *testing.T) {
	b, err := json.Marshal(sample)
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range v4Keys {
		if strings.Contains(string(b), `"`+k+`"`) {
			t.Errorf("unknown %s serialized: %s", k, b)
		}
	}
}

// Dolu bir protokol 4 payload'u tasarımdaki alan adlarıyla yazılır; ölçülmüş sıfırlar (swap 0, procs_blocked 0) düşmez.
func TestV4PayloadShape(t *testing.T) {
	p := full()
	b, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range v4Keys {
		if !strings.Contains(string(b), `"`+k+`"`) {
			t.Errorf("v4 field %s missing from %s", k, b)
		}
	}
	for _, want := range []string{
		`"procs_blocked":0`, `"swap_in_per_s":0`, `"oom_kills":3`,
		`"io":{"some10":4,"some60":3.2,"full10":1.1,"full60":0.8}`, `"cpu":{"some10":2.1,"some60":1.4}`,
		`"services":{"full":false,"items":[{"name":"postgresql.service","active":"failed","sub":"failed","restarts":3}]}`,
		`"read_only":true`, `"health":"unhealthy","health_failing_streak":3,"exit_code":137,"oom_killed":true`,
		`"rx_drops":12`, `"sources":[{"name":"185.125.190.57","state":"selected","reach":255}]`,
	} {
		if !strings.Contains(string(b), want) {
			t.Errorf("missing %s in %s", want, b)
		}
	}

	var back MetricsPayload
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(back, p) {
		t.Errorf("v4 payload does not survive a JSON round trip:\n got %+v\nwant %+v", back, p)
	}
}

// Core() girdinin kendisini değiştirmez: geri dönüşten sonra tam payload yine tam gönderilebilmeli (Compat yoklaması).
func TestCoreDoesNotMutateTheFullPayload(t *testing.T) {
	p := full()
	_ = p.Core()
	if !hasV4(p) || p.DockerContainers[0].Health != "unhealthy" || p.Disk[0].ReadOnly == nil {
		t.Fatalf("Core() stripped the v4 fields from the original payload: %+v", p)
	}
}

// Dönüştürücüler değeri değiştirmez (yuvarlama server'da yapılır); bütünüyle bilinmeyen nesneyi göndermez.
func TestV4ConvertersKeepRawValuesAndOmitUnknown(t *testing.T) {
	if FromCPUBreakdown(collector.CPUBreakdown{}) != nil || FromMemoryStats(collector.MemoryStats{}) != nil ||
		FromPressure(collector.SystemPressure{}) != nil || FromRAID(nil) != nil {
		t.Fatal("a completely unknown v4 object must be omitted, not sent empty")
	}
	cpu := FromCPUBreakdown(collector.CPUBreakdown{IOWaitPct: ptr(1.2345), ProcsBlocked: ptr(0)})
	if *cpu.IOWaitPct != 1.2345 || cpu.StealPct != nil || *cpu.ProcsBlocked != 0 {
		t.Errorf("cpu detail = %+v", cpu)
	}
	mem := FromMemoryStats(collector.MemoryStats{SwapInPerS: ptr(0.04), OOMKills: ptr(uint64(2))})
	if *mem.SwapInPerS != 0.04 || *mem.OOMKills != 2 || mem.AvailableMB != nil {
		t.Errorf("memory detail = %+v", mem)
	}
	psi := FromPressure(collector.SystemPressure{IO: &collector.PressureStall{Some10: 3.14159, Some60: 2, Full10: ptr(0.96)}})
	if psi.CPU != nil || psi.IO.Some10 != 3.14159 || *psi.IO.Full10 != 0.96 || psi.IO.Full60 != nil {
		t.Errorf("pressure = %+v / io %+v", psi, psi.IO)
	}
	raid := FromRAID([]collector.RAIDArray{{Name: "md0", Level: "raid1", State: "recovering", Devices: 2, Active: 1, SyncPct: ptr(12.66)}})
	if len(raid) != 1 || *raid[0].SyncPct != 12.66 || raid[0].State != "recovering" {
		t.Errorf("raid = %+v", raid)
	}
}
