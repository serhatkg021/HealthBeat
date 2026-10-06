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
	// servicesTimeout, bir servis toplamasının (list-units + show) tamamıdır; yüzlerce servisli makinede ~0,5 sn sürer.
	servicesTimeout = 10 * time.Second
	// processesEvery: süreç özeti 60 sn'de bir; CPU sıralaması bu aralığın ortalamasıdır.
	processesEvery   = time.Minute
	processesTimeout = 10 * time.Second
	// updatesEvery: bekleyen güncellemeler saatte bir (paket listeleri zaten günde bir güncellenir).
	updatesEvery   = time.Hour
	updatesTimeout = 35 * time.Second
	// FirstReportWait, açılıştaki ilk raporun yavaş kaynakların ilk sonucunu en çok ne kadar beklediğidir.
	FirstReportWait = 3 * time.Second
)

// Builder, raporu kuran toplayıcıları taşır. Eşzamanlı kullanılabilir (pull modunda istekler paralel gelir).
type Builder struct {
	diskMounts []string
	cpu        *collector.CPUCollector
	memory     *collector.MemoryCollector
	io         *collector.IOCollector
	docker     *collector.Background[[]collector.DockerContainer]
	host       *collector.HostInfoCollector
	services   *collector.Background[[]collector.ServiceState]
	svcReport  *serviceReporter
	processes  *collector.Background[collector.ProcessSummary]
	updates    *collector.Background[*collector.UpdatesInfo]
}

// New, toplayıcıları kurar; interval, yavaş kaynakların her rapor aralığında toplananlarının (Docker) aralığıdır.
// Start çağrılana kadar arka planda hiçbir şey toplanmaz.
func New(diskMounts []string, interval func() time.Duration) *Builder {
	docker := collector.NewDockerCollector()
	return &Builder{
		diskMounts: diskMounts,
		cpu:        collector.NewCPUCollector(),
		memory:     collector.NewMemoryCollector(),
		io:         collector.NewIOCollector(),
		docker:     collector.NewBackground("docker", interval, dockerTimeout, docker.Sample),
		host:       collector.NewHostInfoCollector(docker),
		services:   collector.NewBackground("services", interval, servicesTimeout, collector.NewServiceCollector().Collect),
		svcReport:  newServiceReporter(),
		processes: collector.NewBackground("processes", every(processesEvery), processesTimeout,
			collector.NewProcessCollector().Collect),
		updates: collector.NewBackground("updates", every(updatesEvery), updatesTimeout,
			collector.NewUpdatesCollector().Collect),
	}
}

func every(d time.Duration) func() time.Duration { return func() time.Duration { return d } }

// Start, arka plan toplayıcılarını ctx bitene kadar çalıştırır.
func (b *Builder) Start(ctx context.Context) {
	b.docker.Start(ctx)
	b.services.Start(ctx)
	b.processes.Start(ctx)
	b.updates.Start(ctx)
	b.host.Start(ctx)
}

// WaitReady, arka plan toplayıcılarının ilk sonucunu toplamda en çok d kadar bekler.
func (b *Builder) WaitReady(ctx context.Context, d time.Duration) {
	deadline := time.Now().Add(d)
	for _, wait := range []func(context.Context, time.Duration){
		b.docker.WaitReady, b.services.WaitReady, b.processes.WaitReady, b.updates.WaitReady, b.host.WaitReady,
	} {
		left := time.Until(deadline)
		if left <= 0 {
			return
		}
		wait(ctx, left)
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
	physical := collector.PhysicalDisks(collector.MountsOf(disks))
	physNames := make([]string, len(physical))
	for i, d := range physical {
		physNames[i] = d.Name
	}
	ioSample := b.io.Sample(physNames)
	containers, _ := b.docker.Latest()
	services, servicesKnown := b.services.Latest()
	hostInfo := b.host.Collect(ctx)
	var processes *pusher.Processes
	if p, ok := b.processes.Latest(); ok {
		processes = pusher.FromProcesses(p)
	}
	updates, _ := b.updates.Latest()
	// Sıcaklık yalnızca fiziksel makinede: sanal makinelerin bildirdiği (acpitz gibi) sensörler sahte değerdir.
	var temperatures []pusher.Temperature
	if v := hostInfo.Virtualization; v == nil || (v.Kind != "vm" && v.Kind != "container") {
		temperatures = pusher.FromTemperatures(collector.ReadTemperatures(""))
	}

	return pusher.MetricsPayload{
		CPUUsagePct:      cpuPct,
		RAMUsagePct:      ramPct,
		Disk:             pusher.FromDiskUsages(disks),
		CPUCores:         collector.CPUCores(),
		RAMTotalMB:       collector.TotalMemoryMB(),
		PhysicalDisks:    pusher.FromPhysicalDisks(physical),
		HostInfo:         hostInfo,
		DockerContainers: pusher.FromDockerContainers(containers),

		CPUDetail:    pusher.FromCPUBreakdown(b.cpu.Breakdown()),
		MemoryDetail: pusher.FromMemoryStats(b.memory.Sample()),
		Pressure:     pusher.FromPressure(collector.ReadPressure("")),
		RAID:         pusher.FromRAID(collector.ReadRAID("")),
		DiskIO:       pusher.FromDiskIO(ioSample.Disks),
		NetIO:        pusher.FromNetIO(ioSample.Net),
		TCP:          pusher.FromTCP(ioSample.TCP),
		Services:     b.svcReport.next(services, servicesKnown),
		Temperatures: temperatures,
		Capacity:     pusher.FromCapacity(collector.ReadCapacity("")),
		Processes:    processes,
		Updates:      pusher.FromUpdates(updates),
	}
}
