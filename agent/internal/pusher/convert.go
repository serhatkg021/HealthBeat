package pusher

import "healthbeat-agent/internal/collector"

// FromDiskUsages ve FromDockerContainers, collector çıktısını tel üzerindeki payload
// biçimine uyarlar. Push döngüsü ve pull modu HTTP işleyicisi tarafından paylaşılır; böylece
// ikisi de server'a aynı JSON biçimini gönderir.

func FromDiskUsages(in []collector.DiskUsage) []DiskUsage {
	out := make([]DiskUsage, len(in))
	for i, d := range in {
		out[i] = DiskUsage{Mount: d.Mount, UsedPct: d.UsedPct, Total: d.Total, Free: d.Free, InodesUsedPct: d.InodesUsedPct}
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
		}
	}
	return out
}
