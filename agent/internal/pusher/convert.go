package pusher

import (
	"time"

	"healthbeat-agent/internal/collector"
)

// FromDiskUsages ve FromDockerContainers, collector çıktısını tel üzerindeki payload
// biçimine uyarlar. Push döngüsü ve pull modu HTTP işleyicisi tarafından paylaşılır; böylece
// ikisi de server'a aynı JSON biçimini gönderir.

func FromDiskUsages(in []collector.DiskUsage) []DiskUsage {
	out := make([]DiskUsage, len(in))
	for i, d := range in {
		readOnly := d.ReadOnly
		out[i] = DiskUsage{Mount: d.Mount, UsedPct: d.UsedPct, Total: d.Total, Free: d.Free, InodesUsedPct: d.InodesUsedPct, ReadOnly: &readOnly}
	}
	return out
}

func FromPhysicalDisks(in []collector.PhysicalDisk) []PhysicalDisk {
	if len(in) == 0 {
		return nil // omitempty: "bilinmiyor" hiç gönderilmez
	}
	out := make([]PhysicalDisk, len(in))
	for i, d := range in {
		out[i] = PhysicalDisk{Name: d.Name, Model: d.Model, SizeBytes: d.SizeBytes, Kind: d.Kind, Mounts: d.Mounts}
	}
	return out
}

func FromDockerContainers(in []collector.DockerContainer) []DockerContainer {
	out := make([]DockerContainer, len(in))
	for i, c := range in {
		out[i] = DockerContainer{
			Name:          c.Name,
			Image:         c.Image,
			Status:        c.Status,
			CPUPct:        c.CPUPct,
			RAMMB:         c.RAMMB,
			RestartCount:  c.RestartCount,
			UptimeSeconds: c.UptimeSeconds,

			Health:              c.Health,
			HealthFailingStreak: c.HealthFailingStreak,
			ExitCode:            c.ExitCode,
			OOMKilled:           c.OOMKilled,
		}
	}
	return out
}

// Protokol 4 dönüştürücüleri. Değerler ölçüldüğü gibi gönderilir: yuvarlama bir saklama kararıdır ve server'da yapılır
// (alert'ler ham değere bakar; hassasiyet değişince agent'ı yeniden yayınlamak gerekmez). Bütün alanları bilinmeyen
// nesne nil döner ve gönderilmez.

func FromCPUBreakdown(b collector.CPUBreakdown) *CPUDetail {
	if b.IOWaitPct == nil && b.StealPct == nil && b.ProcsBlocked == nil {
		return nil
	}
	return &CPUDetail{IOWaitPct: b.IOWaitPct, StealPct: b.StealPct, ProcsBlocked: b.ProcsBlocked}
}

func FromMemoryStats(m collector.MemoryStats) *MemoryDetail {
	if m == (collector.MemoryStats{}) {
		return nil
	}
	return &MemoryDetail{AvailableMB: m.AvailableMB, CachedMB: m.CachedMB, SwapInPerS: m.SwapInPerS,
		SwapOutPerS: m.SwapOutPerS, OOMKills: m.OOMKills}
}

func FromPressure(p collector.SystemPressure) *Pressure {
	conv := func(s *collector.PressureStall) *PressureStall {
		if s == nil {
			return nil
		}
		return &PressureStall{Some10: s.Some10, Some60: s.Some60, Full10: s.Full10, Full60: s.Full60}
	}
	if p.CPU == nil && p.Memory == nil && p.IO == nil {
		return nil
	}
	return &Pressure{CPU: conv(p.CPU), Memory: conv(p.Memory), IO: conv(p.IO)}
}

func FromRAID(in []collector.RAIDArray) []RAID {
	if len(in) == 0 {
		return nil
	}
	out := make([]RAID, len(in))
	for i, a := range in {
		out[i] = RAID{Name: a.Name, Level: a.Level, State: a.State, Devices: a.Devices, Active: a.Active, SyncPct: a.SyncPct}
	}
	return out
}

// FromServices, servis listesini tel biçimine çevirir; full, listenin tam olup olmadığıdır.
func FromServices(in []collector.ServiceState, full bool) *Services {
	items := make([]Service, len(in))
	for i, s := range in {
		items[i] = Service{Name: s.Name, Description: s.Description, Active: s.Active, Sub: s.Sub, Restarts: s.Restarts, Enabled: s.Enabled}
		if !s.Since.IsZero() {
			items[i].Since = s.Since.UTC().Format(time.RFC3339)
		}
	}
	return &Services{Full: full, Items: items}
}

func FromDiskIO(in []collector.DiskIORate) []DiskIO {
	if len(in) == 0 {
		return nil
	}
	out := make([]DiskIO, len(in))
	for i, d := range in {
		out[i] = DiskIO{Name: d.Name, ReadIOPS: d.ReadIOPS, WriteIOPS: d.WriteIOPS, ReadBps: d.ReadBps, WriteBps: d.WriteBps,
			UtilPct: d.UtilPct, AwaitMs: d.AwaitMs, QueueDepth: d.QueueDepth}
	}
	return out
}

func FromNetIO(in []collector.NetIORate) []NetIO {
	if len(in) == 0 {
		return nil
	}
	out := make([]NetIO, len(in))
	for i, n := range in {
		out[i] = NetIO{Interface: n.Interface, RxBps: n.RxBps, TxBps: n.TxBps, RxErrors: n.RxErrors, TxErrors: n.TxErrors,
			RxDrops: n.RxDrops, TxDrops: n.TxDrops}
	}
	return out
}

func FromTCP(t collector.TCPStats) *TCP {
	if t == (collector.TCPStats{}) {
		return nil
	}
	return &TCP{RetransPct: t.RetransPct, Established: t.Established, TimeWait: t.TimeWait}
}

func FromTemperatures(in []collector.TemperatureReading) []Temperature {
	if len(in) == 0 {
		return nil
	}
	out := make([]Temperature, len(in))
	for i, t := range in {
		out[i] = Temperature{Sensor: t.Sensor, Kind: t.Kind, Celsius: t.Celsius, Max: t.Max, Crit: t.Crit}
	}
	return out
}

func FromProcesses(p collector.ProcessSummary) *Processes {
	conv := func(in []collector.ProcessGroup) []ProcessGroup {
		if len(in) == 0 {
			return nil
		}
		out := make([]ProcessGroup, len(in))
		for i, g := range in {
			out[i] = ProcessGroup{Name: g.Name, Count: g.Count, CPUPct: g.CPUPct, RSSMB: g.RSSMB}
		}
		return out
	}
	return &Processes{Total: p.Total, Zombie: p.Zombie, TopCPU: conv(p.TopCPU), TopRAM: conv(p.TopRAM)}
}

func FromUpdates(u *collector.UpdatesInfo) *Updates {
	if u == nil {
		return nil
	}
	out := &Updates{Pending: u.Pending, Security: u.Security}
	if !u.ListsUpdatedAt.IsZero() {
		out.ListsUpdatedAt = u.ListsUpdatedAt.UTC().Format(time.RFC3339)
	}
	return out
}

func FromCapacity(c collector.CapacityStats) *Capacity {
	if c == (collector.CapacityStats{}) {
		return nil
	}
	return &Capacity{FileHandles: c.FileHandles, FileHandlesMax: c.FileHandlesMax, Conntrack: c.Conntrack,
		ConntrackMax: c.ConntrackMax, Tasks: c.Tasks, PIDMax: c.PIDMax}
}

func FromTimeSync(t *collector.TimeSyncInfo) *TimeSync {
	if t == nil {
		return nil
	}
	out := &TimeSync{Enabled: t.Enabled, Synchronized: t.Synchronized, Daemon: t.Daemon, LocalRTC: t.LocalRTC,
		Server: t.Server, ServerAddress: t.ServerAddress, ConfiguredServers: t.ConfiguredServers, Stratum: t.Stratum,
		Leap: t.Leap, OffsetMs: t.OffsetMs, DelayMs: t.DelayMs, JitterMs: t.JitterMs, RootDistanceMs: t.RootDistanceMs,
		PollS: t.PollS, Ignored: t.Ignored}
	if !t.LastSync.IsZero() {
		out.LastSync = t.LastSync.UTC().Format(time.RFC3339)
	}
	for _, s := range t.Sources {
		out.Sources = append(out.Sources, TimeSource{Name: s.Name, State: s.State, Reach: s.Reach, OffsetMs: s.OffsetMs})
	}
	return out
}
