// Package report, agent raporunu tek yerde kurar; push gövdesi ile pull yanıtı aynıdır. Hızlı okumalar (/proc, /sys,
// statfs) rapor anında yapılır; yavaş kaynaklar (Docker, envanterin komut gerektiren alanları) arka planda kendi
// aralıklarıyla toplanır ve rapor yalnızca son sonuçlarını okur: yavaş bir kaynak raporu geciktiremez ve kaybettiremez
// (bkz. docs/AGENT.md, "Toplama düzeni").
package report

import (
	"context"
	"log"
	"time"

	"healthbeat-agent/internal/collector"
	"healthbeat-agent/internal/pusher"
)

const (
	// dockerTimeout, bir Docker toplamasının tamamıdır. İlk kez görülen her çalışan container'ın istatistiği ~2 sn sürer
	// (16'şar paralel); sonraki toplamalar one-shot olduğu için milisaniyeler sürer.
	dockerTimeout = 20 * time.Second
	// FirstReportWait, açılıştaki ilk raporun yavaş kaynakların ilk sonucunu en çok ne kadar beklediğidir.
	FirstReportWait = 3 * time.Second
)

// Builder, raporu kuran toplayıcıları taşır. Eşzamanlı kullanılabilir (pull modunda istekler paralel gelir).
type Builder struct {
	diskMounts []string
	cpu        *collector.CPUCollector
	memory     *collector.MemoryCollector
	docker     *collector.Background[[]collector.DockerContainer]
	host       *collector.HostInfoCollector
}

// New, toplayıcıları kurar; interval, yavaş kaynakların her rapor aralığında toplananlarının (Docker) aralığıdır.
// Start çağrılana kadar arka planda hiçbir şey toplanmaz.
func New(diskMounts []string, interval func() time.Duration) *Builder {
	docker := collector.NewDockerCollector()
	return &Builder{
		diskMounts: diskMounts,
		cpu:        collector.NewCPUCollector(),
		memory:     collector.NewMemoryCollector(),
		docker:     collector.NewBackground("docker", interval, dockerTimeout, docker.Sample),
		host:       collector.NewHostInfoCollector(docker),
	}
}

// Start, arka plan toplayıcılarını ctx bitene kadar çalıştırır.
func (b *Builder) Start(ctx context.Context) {
	b.docker.Start(ctx)
	b.host.Start(ctx)
}

// WaitReady, arka plan toplayıcılarının ilk sonucunu toplamda en çok d kadar bekler.
func (b *Builder) WaitReady(ctx context.Context, d time.Duration) {
	deadline := time.Now().Add(d)
	b.docker.WaitReady(ctx, d)
	if left := time.Until(deadline); left > 0 {
		b.host.WaitReady(ctx, left)
	}
}

// Build, raporu kurar. Arka plandaki bir kaynağın sonucu henüz yoksa ya da bayatsa o kısım "bilinmiyor" gider
// (Docker için boş liste: server boş listeyi "değişiklik yok" sayar).
func (b *Builder) Build(ctx context.Context) pusher.MetricsPayload {
	cpuPct, err := b.cpu.Sample()
	if err != nil {
		log.Printf("collect cpu: %v", err)
	}
	ramPct, err := collector.SampleMemory()
	if err != nil {
		log.Printf("collect memory: %v", err)
	}
	disks := collector.SampleDisk(b.diskMounts)
	containers, _ := b.docker.Latest()

	return pusher.MetricsPayload{
		CPUUsagePct:      cpuPct,
		RAMUsagePct:      ramPct,
		Disk:             pusher.FromDiskUsages(disks),
		CPUCores:         collector.CPUCores(),
		RAMTotalMB:       collector.TotalMemoryMB(),
		PhysicalDisks:    pusher.FromPhysicalDisks(collector.PhysicalDisks(collector.MountsOf(disks))),
		HostInfo:         b.host.Collect(ctx),
		DockerContainers: pusher.FromDockerContainers(containers),

		CPUDetail:    pusher.FromCPUBreakdown(b.cpu.Breakdown()),
		MemoryDetail: pusher.FromMemoryStats(b.memory.Sample()),
		Pressure:     pusher.FromPressure(collector.ReadPressure("")),
		RAID:         pusher.FromRAID(collector.ReadRAID("")),
	}
}
