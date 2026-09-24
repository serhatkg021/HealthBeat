package main

import (
	"context"
	"errors"
	"log"
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
	"healthbeat-server/internal/notify"
	"healthbeat-server/internal/offlinemonitor"
	"healthbeat-server/internal/pullscheduler"
	"healthbeat-server/internal/retention"
	"healthbeat-server/internal/secretbox"
	"healthbeat-server/internal/store"
	"healthbeat-server/internal/tlsreload"
)

func main() {
	// `healthbeat-server migrate <status|up|baseline N>` şemayı server'ı başlatmadan (ve geri kalan
	// yapılandırması olmadan) yönetir.
	if len(os.Args) > 1 && os.Args[1] == "migrate" {
		os.Exit(runMigrateCommand(os.Args[2:], os.Stdout, os.Stderr))
	}

	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	pool, err := db.NewPool(ctx, cfg.DatabaseURL)
	if err != nil {
		log.Fatalf("database: %v", err)
	}
	defer pool.Close()

	if err := prepareSchema(ctx, pool, cfg.AutoMigrate); err != nil {
		log.Fatalf("database schema: %v", err)
	}

	secrets, err := secretbox.New(cfg.SecretsEncryptionKey)
	if err != nil {
		log.Fatalf("secrets key: %v", err)
	}
	if err := ensureFirstAdmin(ctx, cfg, store.NewUsers(pool)); err != nil {
		log.Fatalf("bootstrap admin: %v", err)
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
			log.Printf("alert engine: mail queue not fully drained at shutdown: %v", err)
		}
	}()

	deps := httpapi.NewDeps(pool, tokenSvc, alertEngine, httpapi.RateLimits{
		AuthFailuresPerMinute: cfg.AuthFailuresPerMinute,
		IngestPerMinute:       cfg.IngestPerMinute,
	}, secrets)

	deps.SetAgentPolicy(httpapi.AgentPolicy{Latest: cfg.LatestAgentVersion, Min: cfg.MinSupportedAgentVersion})

	// İstemci IP'si (hız sınırları, denetim kaydı): X-Forwarded-For yalnızca TRUSTED_PROXIES'teki bir proxy'den
	// gelirse okunur. Host adları (compose'da "panel") arka planda periyodik çözülür.
	clientIPs := clientip.New(cfg.TrustedProxies)
	if cfg.TrustedProxies.Empty() {
		log.Printf("client IPs: taken from the TCP peer (TRUSTED_PROXIES is not set)")
	} else {
		log.Printf("client IPs: X-Forwarded-For is trusted only from %s", cfg.TrustedProxies)
	}
	go clientIPs.Run(ctx)
	deps.SetClientIPResolver(clientIPs)

	deps.SetPasswordReset(mailer, cfg.PanelBaseURL)
	switch {
	case mailer.Enabled() && cfg.PanelBaseURL != "":
		log.Printf("password reset by e-mail: enabled (links point to %s)", cfg.PanelBaseURL)
	case mailer.Enabled():
		log.Printf("password reset by e-mail: disabled — SMTP is configured but PANEL_BASE_URL is not set")
	case cfg.PanelBaseURL != "":
		log.Printf("password reset by e-mail: disabled — PANEL_BASE_URL is set but SMTP_HOST is not")
	default:
		log.Printf("password reset by e-mail: disabled (set SMTP_HOST and PANEL_BASE_URL to enable it)")
	}

	go deps.RunTokenPurge(ctx)
	go retention.New(store.NewMetrics(pool), cfg.MetricsRetentionDays).Run(ctx)

	scheduler := pullscheduler.New(pool, alertEngine, secrets, cfg.PullRootCAs)
	go scheduler.Run(ctx)

	monitor := offlinemonitor.New(pool, alertEngine)
	go monitor.Run(ctx)

	// Kullanılamaz bir sertifikada açılışta hata ver, sonra yenilemeleri (certbot vb.) yeniden
	// başlatmadan sunmaya devam et.
	certs, err := tlsreload.New(cfg.TLSCertFile, cfg.TLSKeyFile)
	if err != nil {
		log.Fatalf("tls: %v", err)
	}

	srv := httpapi.NewServer(cfg.HTTPAddr, httpapi.WithCORS(deps.Router(), cfg.CORSAllowedOrigins), certs.TLSConfig())

	serverErr := make(chan error, 1)
	go func() {
		log.Printf("HealthBeat server listening on %s (TLS)", cfg.HTTPAddr)
		serverErr <- srv.ListenAndServeTLS("", "") // sertifikalar TLSConfig.GetCertificate'ten gelir
	}()

	select {
	case <-ctx.Done():
		log.Println("shutting down")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			log.Printf("server shutdown: %v", err)
		}
	case err := <-serverErr:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("server: %v", err)
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
			log.Printf("created the first super_admin %s; it must choose a new password at first login (you can now remove BOOTSTRAP_ADMIN_* from the environment)", cfg.BootstrapAdminEmail)
		}
	}
	n, err := users.CountSuperAdmins(ctx)
	if err != nil {
		return err
	}
	if n == 0 {
		log.Printf("WARNING: no super_admin exists and BOOTSTRAP_ADMIN_EMAIL/BOOTSTRAP_ADMIN_PASSWORD are not set: nobody can sign in to the panel")
	}
	return nil
}
