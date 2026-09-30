package main

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"healthbeat-server/internal/alertengine"
	"healthbeat-server/internal/authsvc"
	"healthbeat-server/internal/httpapi"
	"healthbeat-server/internal/logging"
	"healthbeat-server/internal/model"
	"healthbeat-server/internal/notify"
	"healthbeat-server/internal/retention"
	"healthbeat-server/internal/settings"
	"healthbeat-server/internal/store"
	"healthbeat-server/internal/testdb"
	"healthbeat-server/internal/testsmtp"
)

// Açılışta veritabanındaki ayarlar bileşenlere uygulanır; panelden (settings.Service) yapılan değişiklik yeniden
// başlatmadan etki eder. main.go'daki bağlamanın aynısı gerçek bileşenlerle kurulur.
func TestSettingsReachTheComponentsWithoutARestart(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)

	// Log dosyası sınırlar gelmeden açılır: 20 günlük dosya ayarlar okunana kadar durur, varsayılan 14 günle silinir.
	dir := t.TempDir()
	old := filepath.Join(dir, "server-"+time.Now().AddDate(0, 0, -20).Format(time.DateOnly)+".log.gz")
	if err := os.WriteFile(old, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	logFile, err := logging.OpenFile(logging.FileOptions{Path: filepath.Join(dir, "server.log")})
	if err != nil {
		t.Fatal(err)
	}
	defer logFile.Close()
	if _, err := os.Stat(old); err != nil {
		t.Fatalf("the log file pruned before the settings were read: %v", err)
	}

	appSettings, err := settings.New(ctx, store.NewSettings(pool))
	if err != nil {
		t.Fatal(err)
	}
	tokens := authsvc.NewTokenService([]byte("access-secret-access-secret-12345"), []byte("refresh-secret-refresh-secret-123"), time.Minute, time.Hour)
	engine := alertengine.New(pool, notify.New(notify.Config{}), "")
	deps := httpapi.NewDeps(pool, tokens, engine, httpapi.RateLimits{}, testdb.SecretBox(t))
	targets := settingsTargets{
		logLevel: new(slog.LevelVar), logFile: logFile, tokens: tokens, deps: deps, alerts: engine,
		purger: retention.New(retention.Stores{}, retention.Days{}),
	}
	targets.apply(appSettings.Current())
	appSettings.OnChange(func(_, updated model.AppSettings) { targets.apply(updated) })

	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Fatalf("20-day-old log file after the default 14-day limit was applied: err=%v, want it deleted", err)
	}
	admin := testdb.User(t, pool, "root@x.test", "super_admin", "pw")
	meta := func() (latest, minVersion string) {
		t.Helper()
		tok, _, err := tokens.IssueAccessToken(admin, "root@x.test", "super_admin", false)
		if err != nil {
			t.Fatal(err)
		}
		req := httptest.NewRequest(http.MethodGet, "/api/v1/meta", nil)
		req.Header.Set("Authorization", "Bearer "+tok)
		rec := httptest.NewRecorder()
		deps.Router().ServeHTTP(rec, req)
		var m struct {
			Latest string `json:"latest_agent_version"`
			Min    string `json:"min_agent_version"`
		}
		if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &m) != nil {
			t.Fatalf("GET /api/v1/meta = %d %s", rec.Code, rec.Body)
		}
		return m.Latest, m.Min
	}
	if latest, minVersion := meta(); latest != "1.0.0" || minVersion != "" {
		t.Fatalf("meta at start = %q/%q, want the database defaults 1.0.0/empty", latest, minVersion)
	}
	if targets.logLevel.Level() != slog.LevelInfo {
		t.Fatalf("log level at start = %v", targets.logLevel.Level())
	}

	if _, err := appSettings.Update(ctx, &admin, settings.Patch{
		LatestAgentVersion:       ptr("1.4.0"),
		MinSupportedAgentVersion: ptr("1.2.0"),
		LogLevel:                 ptr("debug"),
		AccessTokenTTLSeconds:    ptr(1800),
	}); err != nil {
		t.Fatal(err)
	}
	if latest, minVersion := meta(); latest != "1.4.0" || minVersion != "1.2.0" {
		t.Errorf("meta after the change = %q/%q, want 1.4.0/1.2.0", latest, minVersion)
	}
	if targets.logLevel.Level() != slog.LevelDebug {
		t.Errorf("log level after the change = %v, want debug", targets.logLevel.Level())
	}
	if _, exp, _ := tokens.IssueAccessToken(admin, "root@x.test", "super_admin", false); time.Until(exp) < 29*time.Minute {
		t.Errorf("access token after the change expires in %v, want ~30m", time.Until(exp))
	}
}

func ptr[T any](v T) *T { return &v }

// Mail kanalı panelden açılınca e-postalar yeni SMTP sunucusuna gider ve "Şifremi unuttum" açılır; kapatılınca ikisi de
// yeniden başlatmadan kapanır.
func TestEmailChannelReachesTheMailerWithoutARestart(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	appSettings, err := settings.New(ctx, store.NewSettings(pool))
	if err != nil {
		t.Fatal(err)
	}
	admin := testdb.User(t, pool, "root@x.test", "super_admin", "pw")
	if _, err := appSettings.Update(ctx, &admin, settings.Patch{PanelBaseURL: ptr("https://panel.test")}); err != nil {
		t.Fatal(err)
	}
	mailer := notify.New(notify.Config{})
	channels, err := settings.NewChannels(ctx, store.NewNotificationChannels(pool, testdb.SecretBox(t)), mailer)
	if err != nil {
		t.Fatal(err)
	}
	followEmailChannel(channels, mailer, appSettings)

	deps := httpapi.NewDeps(pool, nil, nil, httpapi.RateLimits{}, testdb.SecretBox(t))
	deps.SetPasswordReset(mailer, appSettings.Current().PanelBaseURL)
	resetEnabled := func() bool {
		t.Helper()
		rec := httptest.NewRecorder()
		deps.Router().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/auth/options", nil))
		var o struct {
			Enabled bool `json:"password_reset_enabled"`
		}
		if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &o) != nil {
			t.Fatalf("GET /api/v1/auth/options = %d %s", rec.Code, rec.Body)
		}
		return o.Enabled
	}
	if mailer.Enabled() || resetEnabled() {
		t.Fatal("e-mail on before the channel was set up")
	}

	srv := testsmtp.Start(t)
	if _, err := channels.Update(ctx, &admin, model.ChannelEmail, settings.ChannelPatch{
		Enabled: ptr(true),
		Config:  json.RawMessage(`{"host": "` + srv.Host + `", "port": ` + srv.Port + `, "from": "hb@x.test"}`),
	}); err != nil {
		t.Fatal(err)
	}
	if !resetEnabled() {
		t.Fatal("password reset still off after the e-mail channel was turned on")
	}
	if err := mailer.Send(ctx, []string{"a@x.test"}, "konu", "gövde"); err != nil || len(srv.Messages()) != 1 {
		t.Fatalf("send after enabling: err=%v, %d messages", err, len(srv.Messages()))
	}

	if _, err := channels.Update(ctx, &admin, model.ChannelEmail, settings.ChannelPatch{Enabled: ptr(false)}); err != nil {
		t.Fatal(err)
	}
	if mailer.Enabled() || resetEnabled() {
		t.Fatal("e-mail still on after the channel was turned off")
	}
}
