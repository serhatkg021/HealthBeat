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
	"healthbeat-server/internal/logging"
	"healthbeat-server/internal/notify"
	"healthbeat-server/internal/offlinemonitor"
	"healthbeat-server/internal/pullscheduler"
	"healthbeat-server/internal/retention"
	"healthbeat-server/internal/secretbox"
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
	logging.Setup(os.Stderr, cfg.LogLevel, cfg.LogFormat)
	if logFile := openLogFile(cfg); logFile != nil {
		defer logFile.Close()
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	pool, err := db.NewPool(ctx, cfg.DatabaseURL)
	if err != nil {
		fatal("database", err)
	}
	defer pool.Close()

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

	tokenSvc := authsvc.NewTokenService(cfg.JWTAccessSecret, cfg.JWTRefreshSecret, cfg.AccessTokenTTL, cfg.RefreshTokenTTL)

	mailer := notify.New(notify.Config{
		Host:     cfg.SMTPHost,
		Port:     cfg.SMTPPort,
		Username: cfg.SMTPUsername,
		Password: cfg.SMTPPassword,
		From:     cfg.SMTPFrom,
	})
	alertEngine := alertengine.New(pool, mailer, cfg.PanelBaseURL)
	// pool.Close'un defer'inden sonra kaydedildiği için önce çalışır: kuyruktaki alert e-postaları
	// veritabanı kapanmadan önce (sınırlı sürede) teslim edilir.
	defer func() {
		drain, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := alertEngine.Close(drain); err != nil {
			slog.Warn("alert engine: mail queue not fully drained at shutdown", "err", err)
		}
	}()

	deps := httpapi.NewDeps(pool, tokenSvc, alertEngine, httpapi.RateLimits{
		AuthFailuresPerMinute: cfg.AuthFailuresPerMinute,
		IngestPerMinute:       cfg.IngestPerMinute,
	}, secrets)

	deps.SetErrorBodyLogging(cfg.LogErrorBodyBytes)
	deps.SetAgentPolicy(httpapi.AgentPolicy{Latest: cfg.LatestAgentVersion, Min: cfg.MinSupportedAgentVersion})

	// İstemci IP'si (hız sınırları, denetim kaydı): X-Forwarded-For yalnızca TRUSTED_PROXIES'teki bir proxy'den
	// gelirse okunur. Host adları (compose'da "panel") arka planda periyodik çözülür.
	clientIPs := clientip.New(cfg.TrustedProxies)
	if cfg.TrustedProxies.Empty() {
		slog.Info("client IPs: taken from the TCP peer (TRUSTED_PROXIES is not set)")
	} else {
		slog.Info("client IPs: X-Forwarded-For is trusted only from the configured proxies", "trusted_proxies", cfg.TrustedProxies.String())
	}
	go logging.RunLoop(ctx, "client IP resolver", clientIPs.Run)
	deps.SetClientIPResolver(clientIPs)

	deps.SetPasswordReset(mailer, cfg.PanelBaseURL)
	switch {
	case mailer.Enabled() && cfg.PanelBaseURL != "":
		slog.Info("password reset by e-mail: enabled", "panel_base_url", cfg.PanelBaseURL)
	case mailer.Enabled():
		slog.Info("password reset by e-mail: disabled — SMTP is configured but PANEL_BASE_URL is not set")
	case cfg.PanelBaseURL != "":
		slog.Info("password reset by e-mail: disabled — PANEL_BASE_URL is set but SMTP_HOST is not")
	default:
		slog.Info("password reset by e-mail: disabled (set SMTP_HOST and PANEL_BASE_URL to enable it)")
	}

	go logging.RunLoop(ctx, "token purge", deps.RunTokenPurge)
	go logging.RunLoop(ctx, "metrics retention", retention.New(store.NewMetrics(pool), cfg.MetricsRetentionDays).Run)

	scheduler := pullscheduler.New(pool, alertEngine, secrets, cfg.PullRootCAs)
	go logging.RunLoop(ctx, "pull scheduler", scheduler.Run)

	monitor := offlinemonitor.New(pool, alertEngine)
	go logging.RunLoop(ctx, "offline monitor", monitor.Run)

	// Kullanılamaz bir sertifikada açılışta hata ver, sonra yenilemeleri (certbot vb.) yeniden
	// başlatmadan sunmaya devam et.
	certs, err := tlsreload.New(cfg.TLSCertFile, cfg.TLSKeyFile)
	if err != nil {
		fatal("tls", err)
	}

	srv := httpapi.NewServer(cfg.HTTPAddr, httpapi.WithCORS(deps.Router(), cfg.CORSAllowedOrigins), certs.TLSConfig())

	serverErr := make(chan error, 1)
	go func() {
		slog.Info("HealthBeat server listening (TLS)", "addr", cfg.HTTPAddr, "version", version.Version, "log_level", cfg.LogLevel.String(), "log_format", cfg.LogFormat)
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
// yol) server yine başlar ve bunu ERROR olarak loglar: log dosyası yüzünden izleme durmamalı.
func openLogFile(cfg *config.Config) *logging.FileWriter {
	if cfg.LogFile == "" {
		return nil
	}
	fw, err := logging.OpenFile(logging.FileOptions{
		Path:          cfg.LogFile,
		MaxAgeDays:    cfg.LogFileMaxAgeDays,
		MaxTotalBytes: int64(cfg.LogFileMaxTotalMB) << 20,
	})
	if err != nil {
		slog.Error("log file disabled: logging to stdout only", "path", cfg.LogFile, "err", err)
		return nil
	}
	logging.Setup(io.MultiWriter(os.Stderr, fw), cfg.LogLevel, cfg.LogFormat)
	slog.Info("logging to file", "path", cfg.LogFile, "max_age_days", cfg.LogFileMaxAgeDays, "max_total_mb", cfg.LogFileMaxTotalMB)
	return fw
}

// fatal, açılışı durduran bir hatayı loglar ve süreci sonlandırır.
func fatal(what string, err error) {
	slog.Error(what, "err", err)
	os.Exit(1)
}
