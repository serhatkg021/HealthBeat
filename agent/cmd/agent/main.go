package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os/signal"
	"syscall"
	"time"

	"healthbeat-agent/internal/collector"
	"healthbeat-agent/internal/config"
	"healthbeat-agent/internal/pullserver"
	"healthbeat-agent/internal/pusher"
	"healthbeat-agent/internal/report"
	"healthbeat-agent/internal/version"
)

// sendTimeout, bir raporun gönderiminin süresidir. Toplama ayrıdır ve yavaş kaynakları beklemez (bkz. report).
const sendTimeout = 10 * time.Second

func main() {
	configPath := flag.String("config", "config.json", "path to agent config file")
	showVersion := flag.Bool("version", false, "print the agent version and exit")
	printInventory := flag.Bool("print-inventory", false, "print the machine inventory this agent would report (JSON) and exit; needs no config")
	printReport := flag.Bool("print-report", false, "print a full report as this agent would send it (JSON; takes a few seconds to measure rates) and exit; reads -config if it exists for disk_mounts")
	checkConfig := flag.Bool("check-config", false, "validate the config file and exit (0 = valid); install.sh upgrade uses it to test an existing config against a new binary")
	flag.Parse()

	if *showVersion {
		fmt.Println(version.String())
		return
	}

	if *printInventory {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		out, err := json.MarshalIndent(collector.NewHostInfoCollector(collector.NewDockerCollector()).Collect(ctx), "", "  ")
		if err != nil {
			log.Fatalf("inventory: %v", err)
		}
		fmt.Println(string(out))
		return
	}

	if *printReport {
		mounts := []string{"/"}
		if cfg, err := config.Load(*configPath); err == nil {
			mounts = cfg.DiskMounts
		}
		printFullReport(mounts)
		return
	}

	cfg, err := config.Load(*configPath)
	if err != nil {
		log.Fatalf("config: %v", err)
	}
	if *checkConfig {
		fmt.Printf("config OK (mode: %s)\n", cfg.Mode)
		return
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if cfg.Mode == config.ModePull {
		runPullMode(ctx, cfg)
		return
	}
	runPushMode(ctx, cfg)
}

// printFullReport, gönderilecek raporu yazdırır. Oranlar (disk G/Ç, ağ, swap, süreç CPU'su) iki ölçüm arasındaki farktır:
// ilk rapordan 2 sn sonra ikinci bir rapor kurulup o yazdırılır; süreç CPU sıralaması 60 sn'lik aralık gerektirdiği için
// burada görünmez.
func printFullReport(mounts []string) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	builder := report.New(mounts, func() time.Duration { return 30 * time.Second })
	builder.Start(ctx)
	builder.WaitReady(ctx, 10*time.Second)
	builder.Build(ctx)
	time.Sleep(2 * time.Second)
	out, err := json.MarshalIndent(builder.Build(ctx), "", "  ")
	if err != nil {
		log.Fatalf("report: %v", err)
	}
	fmt.Println(string(out))
}

func runPullMode(ctx context.Context, cfg *config.Config) {
	srv, err := pullserver.New(cfg)
	if err != nil {
		log.Fatalf("pull server: %v", err)
	}
	srv.Start(ctx)

	errCh := make(chan error, 1)
	go func() { errCh <- srv.ListenAndServe() }()

	select {
	case <-ctx.Done():
		log.Println("shutting down")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			log.Printf("pull server shutdown: %v", err)
		}
	case err := <-errCh:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("pull server: %v", err)
		}
	}
}

func runPushMode(ctx context.Context, cfg *config.Config) {
	roots, err := cfg.RootCAs()
	if err != nil {
		log.Fatalf("config: %v", err) // config.Load tarafından zaten doğrulanmış; güvenlik ağı olarak bırakıldı
	}
	p := pusher.New(cfg.ServerURL, cfg.HostID, cfg.APIToken, pusher.TLSOptions{RootCAs: roots, InsecureSkipVerify: cfg.InsecureSkipVerify})

	interval := time.Duration(cfg.IntervalSeconds) * time.Second
	log.Printf("HealthBeat agent %s (protocol %d) starting: pushing to %s every %s", version.Version, version.Protocol, cfg.ServerURL, interval)

	builder := report.New(cfg.DiskMounts, func() time.Duration { return interval })
	builder.Start(ctx)
	builder.WaitReady(ctx, report.FirstReportWait)

	session := &pushSession{p: p, compat: pusher.NewCompat(p)}
	runPushCycle(ctx, builder, session)

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			log.Println("shutting down")
			return
		case <-ticker.C:
			runPushCycle(ctx, builder, session)
		}
	}
}

// pushSession, push döngüsünün oturum boyunca taşıdığı durumdur: gönderici, eski server'lara karşı
// geri dönüş (Compat) ve server sürümü bilgisinin son bildirilen hâli.
type pushSession struct {
	p      *pusher.Pusher
	compat *pusher.Compat

	announced bool
	lastInfo  pusher.ServerInfo
}

// announceServer, server'ın bildirdiği sürüm bilgisi değiştiğinde (ve ilk kez) notları loglar; her
// döngüde tekrar etmez.
func (s *pushSession) announceServer() {
	cur := s.p.ServerInfo()
	if s.announced && cur == s.lastInfo {
		return
	}
	s.announced, s.lastInfo = true, cur
	for _, note := range pusher.ServerNotes(cur, version.Version, version.Protocol) {
		log.Print(note)
	}
}

func runPushCycle(ctx context.Context, builder *report.Builder, session *pushSession) {
	payload := builder.Build(ctx)

	sendCtx, cancel := context.WithTimeout(ctx, sendTimeout)
	defer cancel()
	if err := session.compat.Push(sendCtx, payload); err != nil {
		log.Printf("push metrics: %v", err)
		return
	}
	session.announceServer()
	mode := ""
	if session.compat.Degraded() {
		mode = " (core metrics only: the server does not accept the full payload)"
	}
	log.Printf("pushed metrics: cpu=%.1f%% ram=%.1f%% disks=%d containers=%d%s",
		payload.CPUUsagePct, payload.RAMUsagePct, len(payload.Disk), len(payload.DockerContainers), mode)
}
