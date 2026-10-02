package main

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"healthbeat-server/internal/alertengine"
	"healthbeat-server/internal/authsvc"
	"healthbeat-server/internal/clientip"
	"healthbeat-server/internal/config"
	"healthbeat-server/internal/db"
	"healthbeat-server/internal/httpapi"
	"healthbeat-server/internal/jobs"
	"healthbeat-server/internal/logging"
	"healthbeat-server/internal/model"
	"healthbeat-server/internal/notify"
	"healthbeat-server/internal/offlinemonitor"
	"healthbeat-server/internal/pullscheduler"
	"healthbeat-server/internal/retention"
	"healthbeat-server/internal/secretbox"
	"healthbeat-server/internal/settings"
	"healthbeat-server/internal/store"
	"healthbeat-server/internal/tlsreload"
	"healthbeat-server/internal/version"
)

func main() {
	// `healthbeat-server migrate <status|up|baseline N>` şemayı server'ı başlatmadan (ve geri kalan
	// yapılandırması olmadan) yönetir.
	if len(os.Args) > 1 && os.Args[1] == "migrate" {
		os.Exit(runMigrateCommand(os.Args[2:], os.Stdout, os.Stderr))
	}

	cfg, err := config.Load()
	if err != nil {
		fatal("config", err)
	}
	// Log seviyesi ve log dosyasının saklama sınırları veritabanındaki ayarlardan gelir (panelden değişir). Ayarlar
	// okunana kadar (açılış, migration) seviye info'dur ve log dosyasından hiçbir şey silinmez.
	logLevel := new(slog.LevelVar)
	logging.Setup(os.Stderr, logLevel, cfg.LogFormat)
	logFile := openLogFile(cfg, logLevel)
	if logFile != nil {
		defer logFile.Close()
	}
	for _, key := range cfg.Obsolete {
		slog.Warn(key + " is no longer read; manage it from the panel (Settings)")
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	pool, err := db.NewPool(ctx, cfg.DatabaseURL, cfg.DBMaxConns)
	if err != nil {
		fatal("database", err)
	}
	defer pool.Close()
	slog.Info("database pool", "max_conns", pool.Config().MaxConns)
	// Arka plan döngüleri; kapanışta havuz kapanmadan önce bitmeleri beklenir (stopBackground).
	background := jobs.New(ctx)

	if err := prepareSchema(ctx, pool, cfg.AutoMigrate); err != nil {
		fatal("database schema", err)
	}

	secrets, err := secretbox.New(cfg.SecretsEncryptionKey)
	if err != nil {
		fatal("secrets key", err)
	}
	if err := ensureFirstAdmin(ctx, cfg, store.NewUsers(pool)); err != nil {
		fatal("bootstrap admin", err)
	}
	appSettings, err := settings.New(ctx, store.NewSettings(pool))
	if err != nil {
		fatal("settings", err)
	}
	cur := appSettings.Current()

	tokenSvc := authsvc.NewTokenService(cfg.JWTAccessSecret, cfg.JWTRefreshSecret, cur.AccessTokenTTL, cur.RefreshTokenTTL)

	// E-posta (alert'ler, şifre sıfırlama) mail kanalının ayarıyla gönderilir; kanal kapalıysa yalnızca loglanır.
	mailer := notify.New(notify.Config{})
	channels, err := settings.NewChannels(ctx, store.NewNotificationChannels(pool, secrets), mailer)
	if err != nil {
		fatal("notification channels", err)
	}
	email, _ := channels.Get(model.ChannelEmail)
	mailer.SetConfig(settings.MailerConfig(email))
	alertEngine := alertengine.New(pool, mailer, cur.PanelBaseURL)
	// Alert bildirimleri kalıcı kuyruktan (notification_outbox) teslim edilir; gönderilemeyen yeniden denenir.
	background.Go("alert notifications", alertEngine.RunNotifications)

	deps := httpapi.NewDeps(pool, tokenSvc, alertEngine, rateLimits(cur), secrets)

	// İstemci IP'si (hız sınırları, denetim kaydı): X-Forwarded-For yalnızca TRUSTED_PROXIES'teki bir proxy'den
	// gelirse okunur. Host adları (compose'da "panel") arka planda periyodik çözülür.
	clientIPs := clientip.New(cfg.TrustedProxies)
	if cfg.TrustedProxies.Empty() {
		slog.Info("client IPs: taken from the TCP peer (TRUSTED_PROXIES is not set)")
	} else {
		slog.Info("client IPs: X-Forwarded-For is trusted only from the configured proxies", "trusted_proxies", cfg.TrustedProxies.String())
	}
	background.Go("client IP resolver", clientIPs.Run)
	deps.SetClientIPResolver(clientIPs)

	deps.SetSettings(appSettings, channels)
	deps.SetPasswordReset(mailer, cur.PanelBaseURL)
	logPasswordReset(mailer.Enabled(), cur.PanelBaseURL)
	followEmailChannel(channels, mailer, appSettings)

	background.Go("password mails", deps.RunMailOutbox)
	background.Go("token purge", retention.NewTokenPurger(store.NewRefreshTokens(pool), store.NewPasswordResets(pool)).Run)
	purger := retention.New(
		retention.Stores{Metrics: store.NewMetrics(pool), Audit: store.NewAudit(pool), Alerts: store.NewAlerts(pool)},
		retentionDays(cur),
	)
	background.Go("retention", purger.Run)

	// Ayarlar panelden değişince yeniden başlatmadan uygulanır.
	targets := settingsTargets{logLevel: logLevel, logFile: logFile, tokens: tokenSvc, deps: deps, alerts: alertEngine, purger: purger}
	targets.apply(cur)
	appSettings.OnChange(func(old, updated model.AppSettings) {
		targets.apply(updated)
		if old.PanelBaseURL != updated.PanelBaseURL {
			logPasswordReset(mailer.Enabled(), updated.PanelBaseURL)
		}
	})

	scheduler := pullscheduler.New(pool, alertEngine, secrets, cfg.PullRootCAs)
	background.Go("pull scheduler", scheduler.Run)

	monitor := offlinemonitor.New(pool, alertEngine)
	background.Go("offline monitor", monitor.Run)

	// Kullanılamaz bir sertifikada açılışta hata ver, sonra yenilemeleri (certbot vb.) yeniden
	// başlatmadan sunmaya devam et.
	certs, err := tlsreload.New(cfg.TLSCertFile, cfg.TLSKeyFile)
	if err != nil {
		fatal("tls", err)
	}

	deps.SetSystemSources(scheduler, certs)

	srv := httpapi.NewServer(cfg.HTTPAddr, httpapi.WithCORS(deps.Router(), cfg.CORSAllowedOrigins), certs.TLSConfig())

	serverErr := make(chan error, 1)
	go func() {
		slog.Info("HealthBeat server listening (TLS)", "addr", cfg.HTTPAddr, "version", version.Version, "log_level", logLevel.Level().String(), "log_format", cfg.LogFormat)
		serverErr <- srv.ListenAndServeTLS("", "") // sertifikalar TLSConfig.GetCertificate'ten gelir
	}()

	select {
	case <-ctx.Done():
		slog.Info("shutting down")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			slog.Warn("server shutdown", "err", err)
		}
	case err := <-serverErr:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			fatal("server", err)
		}
	}
	stop()
	stopBackground(background, alertEngine, deps)
	// Veritabanı havuzu defer'le, bundan sonra kapanır.
}

// Kapanış süreleri: arka plan işlerinin durması ve teslim zamanı gelmiş bildirimlerin gönderilmesi için. HTTP'nin 5
// sn'siyle toplam 20 sn'yi aşmaz (docker-compose.yml stop_grace_period).
const (
	jobsStopTimeout = 5 * time.Second
	drainTimeout    = 10 * time.Second
)

// stopBackground, kapanışın HTTP'den sonraki adımlarıdır: arka plan işlerinin bitmesi beklenir (yarıda kalan sorgular
// kapanmış bir havuza çarpmasın), sonra teslim zamanı gelmiş bildirimler gönderilmeye çalışılır. Gönderilemeyenler
// kaybolmaz: kuyrukta kalır ve bir sonraki açılışta gönderilir.
func stopBackground(background *jobs.Runner, alertEngine *alertengine.Engine, deps *httpapi.Deps) {
	wait, cancel := context.WithTimeout(context.Background(), jobsStopTimeout)
	defer cancel()
	if err := background.Wait(wait); err != nil {
		slog.Warn("background jobs did not stop in time", "err", err)
	} else {
		slog.Info("background jobs stopped")
	}

	drain, cancelDrain := context.WithTimeout(context.Background(), drainTimeout)
	defer cancelDrain()
	if err := alertEngine.Close(drain); err != nil {
		slog.Warn("alert notifications: not all due notifications were sent at shutdown; they stay queued", "err", err)
	}
	if err := deps.WaitForMail(drain); err != nil {
		slog.Warn("password mails: not all due mails were sent at shutdown; they stay queued", "err", err)
	}
}

// ensureFirstAdmin, hiç yoksa BOOTSTRAP_ADMIN_EMAIL / BOOTSTRAP_ADMIN_PASSWORD'den ilk
// super_admin'i oluşturur; hiç yoksa ve oluşturacak bir kaynak da yoksa yüksek sesle uyarır
// (o zaman kimse panele giriş yapamaz).
func ensureFirstAdmin(ctx context.Context, cfg *config.Config, users *store.Users) error {
	if cfg.BootstrapAdminEmail != "" {
		hash, err := authsvc.HashPassword(cfg.BootstrapAdminPassword)
		if err != nil {
			return err
		}
		created, err := users.BootstrapSuperAdmin(ctx, cfg.BootstrapAdminEmail, hash)
		if err != nil {
			return err
		}
		if created {
			slog.Info("created the first super_admin; it must choose a new password at first login (you can now remove BOOTSTRAP_ADMIN_* from the environment)", "email", cfg.BootstrapAdminEmail)
		}
	}
	n, err := users.CountSuperAdmins(ctx)
	if err != nil {
		return err
	}
	if n == 0 {
		slog.Warn("no super_admin exists and BOOTSTRAP_ADMIN_EMAIL/BOOTSTRAP_ADMIN_PASSWORD are not set: nobody can sign in to the panel")
	}
	return nil
}

// openLogFile, LOG_FILE ayarlıysa logu stdout'a ek olarak kalıcı dosyaya da yönlendirir. Dosya açılamazsa (izin, yanlış
// yol) server yine başlar ve bunu ERROR olarak loglar: log dosyası yüzünden izleme durmamalı. Saklama sınırları ayarlar
// okununca verilir (FileWriter.SetLimits); o zamana kadar hiçbir dosya silinmez.
func openLogFile(cfg *config.Config, level slog.Leveler) *logging.FileWriter {
	if cfg.LogFile == "" {
		return nil
	}
	fw, err := logging.OpenFile(logging.FileOptions{Path: cfg.LogFile})
	if err != nil {
		slog.Error("log file disabled: logging to stdout only", "path", cfg.LogFile, "err", err)
		return nil
	}
	logging.Setup(io.MultiWriter(os.Stderr, fw), level, cfg.LogFormat)
	slog.Info("logging to file", "path", cfg.LogFile)
	return fw
}

// settingsTargets, panelden değişen ayarların uygulandığı bileşenlerdir.
type settingsTargets struct {
	logLevel *slog.LevelVar
	logFile  *logging.FileWriter // LOG_FILE boşsa nil
	tokens   *authsvc.TokenService
	deps     *httpapi.Deps
	alerts   *alertengine.Engine
	purger   *retention.Purger
}

// apply, ayarları bileşenlere uygular: açılışta bir kez ve her değişiklikten sonra.
func (t settingsTargets) apply(s model.AppSettings) {
	if level, err := logging.ParseLevel(s.LogLevel); err == nil { // veritabanı yalnızca geçerli seviyeleri kabul eder
		t.logLevel.Set(level)
	}
	if t.logFile != nil {
		if err := t.logFile.SetLimits(s.LogFileMaxAgeDays, int64(s.LogFileMaxTotalMB)<<20); err != nil {
			slog.Error("log file: invalid limits", "err", err)
		}
	}
	t.tokens.SetTTLs(s.AccessTokenTTL, s.RefreshTokenTTL)
	t.deps.SetRateLimits(rateLimits(s))
	t.deps.SetErrorBodyLogging(s.LogErrorBodyBytes)
	t.deps.SetAgentPolicy(httpapi.AgentPolicy{Latest: s.LatestAgentVersion, Min: s.MinSupportedAgentVersion})
	t.deps.SetPanelBaseURL(s.PanelBaseURL)
	t.alerts.SetPanelBaseURL(s.PanelBaseURL)
	t.purger.SetDays(retentionDays(s))
}

func rateLimits(s model.AppSettings) httpapi.RateLimits {
	return httpapi.RateLimits{AuthFailuresPerMinute: s.RateLimitAuthFailuresPerMinute, IngestPerMinute: s.RateLimitIngestPerMinute}
}

func retentionDays(s model.AppSettings) retention.Days {
	return retention.Days{Metrics: s.MetricsRetentionDays, Audit: s.AuditRetentionDays, ResolvedAlerts: s.ResolvedAlertRetentionDays}
}

// followEmailChannel, mail kanalı panelden değişince göndericinin ayarını günceller: alert e-postaları ve şifre
// e-postaları bir sonraki gönderimde yeni ayarı kullanır.
func followEmailChannel(channels *settings.Channels, mailer *notify.Mailer, appSettings *settings.Service) {
	channels.OnChange(func(_, updated model.NotificationChannel) {
		if updated.Channel != model.ChannelEmail {
			return
		}
		wasEnabled := mailer.Enabled()
		mailer.SetConfig(settings.MailerConfig(updated))
		if wasEnabled != mailer.Enabled() {
			logPasswordReset(mailer.Enabled(), appSettings.Current().PanelBaseURL)
		}
	})
}

// logPasswordReset, e-posta ile şifre sıfırlamanın açık olup olmadığını ve neden kapalı olduğunu loglar.
func logPasswordReset(emailEnabled bool, panelBaseURL string) {
	switch {
	case emailEnabled && panelBaseURL != "":
		slog.Info("password reset by e-mail: enabled", "panel_base_url", panelBaseURL)
	case emailEnabled:
		slog.Info("password reset by e-mail: disabled — the e-mail channel is on but the panel address is not set (Settings)")
	case panelBaseURL != "":
		slog.Info("password reset by e-mail: disabled — the panel address is set but the e-mail channel is off or cannot send (Settings)")
	default:
		slog.Info("password reset by e-mail: disabled (turn on the e-mail channel and set the panel address in Settings)")
	}
}

// fatal, açılışı durduran bir hatayı loglar ve süreci sonlandırır.
func fatal(what string, err error) {
	slog.Error(what, "err", err)
	os.Exit(1)
}
