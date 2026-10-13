package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"healthbeat-agent/internal/collector"
	"healthbeat-agent/internal/config"
	"healthbeat-agent/internal/conncheck"
	"healthbeat-agent/internal/pullserver"
	"healthbeat-agent/internal/pusher"
	"healthbeat-agent/internal/report"
	"healthbeat-agent/internal/version"
)

// sendTimeout, bir raporun gönderiminin süresidir. Toplama ayrıdır ve yavaş kaynakları beklemez (bkz. report).
const sendTimeout = 10 * time.Second

// usageText, -help çıktısıdır. Bayrak adları İngilizce kalır (betikler ve belgeler onları kullanır).
const usageText = `Kullanım: healthbeat-agent [seçenekler]

  -config DOSYA        Yapılandırma dosyası (varsayılan config.json)
  -check-config        Yapılandırmayı doğrula ve çık (0 = geçerli)
  -check-connection    Server'a bir rapor gönderip bağlantıyı dene ve çık
                       (yalnızca push modu)
  -list-mounts         Raporlanabilecek diskleri listele ve çık
  -print-inventory     Raporlanacak makine bilgisini JSON olarak yaz
                       ve çık (yapılandırma gerekmez)
  -print-report        Gönderilecek tam raporu JSON olarak yaz ve çık
                       (oranlar için ~4 sn ölçer)
  -encrypt-token       stdin'deki sırrı bu makineye bağlı şifreleyip
                       yaz ve çık
  -version             Sürümü yaz ve çık

Kurulum ve yapılandırma: healthbeat-agent-setup (bkz. docs/AGENT.md).
`

func main() {
	flag.Usage = func() { fmt.Fprint(flag.CommandLine.Output(), usageText) }
	configPath := flag.String("config", "config.json", "yapılandırma dosyası")
	showVersion := flag.Bool("version", false, "sürümü yaz ve çık")
	printInventory := flag.Bool("print-inventory", false, "makine bilgisini JSON olarak yaz ve çık; yapılandırma gerekmez")
	printReport := flag.Bool("print-report", false, "gönderilecek tam raporu JSON olarak yaz ve çık; disk_mounts için -config'i okur")
	checkConfig := flag.Bool("check-config", false, "yapılandırmayı doğrula ve çık (0 = geçerli); install.sh upgrade yeni binary'yi mevcut yapılandırmayla dener")
	checkConnection := flag.Bool("check-connection", false, "server'a bir rapor gönderip bağlantıyı aşama aşama dene ve çık; çıkış kodu hatanın sınıfıdır (kurulum sihirbazı kullanır)")
	listMounts := flag.Bool("list-mounts", false, "raporlanabilecek diskleri (\"auto\"nun kapsadıkları) sekmeyle ayrılmış listele ve çık: yol, tür, bayt, doluluk %")
	encryptToken := flag.Bool("encrypt-token", false, "stdin'deki sırrı (API token ya da pull secret) bu makineye bağlı şifreleyip yaz ve çık; install.sh kullanır")
	flag.Parse()

	if *encryptToken {
		printEncryptedSecret()
		return
	}

	if *showVersion {
		fmt.Println(version.String())
		return
	}

	if *listMounts {
		printMounts()
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
		log.Fatalf("yapılandırma hatası: %v", err)
	}
	if cfg.PlaintextSecret != "" {
		log.Printf("warning: %s is stored in plain text in %s; encrypt it with: %s", cfg.PlaintextSecret, *configPath, config.EncryptHint)
	}
	if *checkConfig {
		fmt.Printf("yapılandırma geçerli (mod: %s)\n", cfg.Mode)
		return
	}
	if *checkConnection {
		os.Exit(int(checkServerConnection(cfg)))
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if cfg.Mode == config.ModePull {
		runPullMode(ctx, cfg)
		return
	}
	runPushMode(ctx, cfg)
}

// printEncryptedSecret, stdin'deki sırrı (sondaki satır sonu atılır) bu makineye bağlı şifreli biçimde yazdırır. Sır
// komut satırı argümanı olarak alınmaz: süreç listesinde ve kabuk geçmişinde görünürdü.
func printEncryptedSecret() {
	data, err := io.ReadAll(io.LimitReader(os.Stdin, 4096))
	if err != nil {
		log.Fatalf("encrypt-token: stdin okunamadı: %v", err)
	}
	enc, err := config.EncryptSecret(strings.TrimSpace(string(data)))
	if err != nil {
		log.Fatalf("encrypt-token: %v", err)
	}
	fmt.Println(enc)
}

// printMounts, "auto"nun raporlayacağı diskleri yazdırır (kurulum sihirbazının disk listesi): yol, dosya sistemi
// türü, toplam bayt ve doluluk yüzdesi, sekmeyle ayrılmış.
func printMounts() {
	types := collector.FilesystemTypes()
	for _, d := range collector.SampleDisk([]string{collector.AutoMounts}) {
		fmt.Printf("%s\t%s\t%d\t%.0f\n", d.Mount, types[d.Mount], d.Total, d.UsedPct)
	}
}

// checkServerConnection, push yapılandırmasıyla server'a ulaşmayı aşama aşama dener ve sonucu döndürür (çıkış kodu).
// Toplayıcıların ve geri dönüşün logları susturulur: çıktı yalnızca denemenin ✓/✗ satırlarıdır.
func checkServerConnection(cfg *config.Config) conncheck.Result {
	if cfg.Mode != config.ModePush {
		fmt.Println("Bağlantı denemesi yalnızca push modunda yapılır (pull modunda bağlantıyı server başlatır).")
		os.Exit(2)
	}
	log.SetOutput(io.Discard)
	roots, err := cfg.RootCAs()
	if err != nil {
		fmt.Printf("  ✗ %v\n", err)
		os.Exit(1)
	}
	tlsOpts := pusher.TLSOptions{RootCAs: roots, InsecureSkipVerify: cfg.InsecureSkipVerify}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	send := func(ctx context.Context) (pusher.ServerInfo, error) {
		p := pusher.New(cfg.ServerURL, cfg.HostID, cfg.APIToken, tlsOpts)
		interval := time.Duration(cfg.IntervalSeconds) * time.Second
		builder := report.New(cfg.DiskMounts, func() time.Duration { return interval })
		builder.Start(ctx)
		builder.WaitReady(ctx, report.FirstReportWait)
		sendCtx, cancelSend := context.WithTimeout(ctx, sendTimeout)
		defer cancelSend()
		err := pusher.NewCompat(p).Push(sendCtx, builder.Build(ctx))
		return p.ServerInfo(), err
	}
	return conncheck.Run(ctx, cfg.ServerURL, tlsOpts, send, os.Stdout)
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
