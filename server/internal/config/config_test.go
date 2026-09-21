package config

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"healthbeat-server/internal/version"
)

func setRequired(t *testing.T) {
	t.Helper()
	for k, v := range map[string]string{
		"DATABASE_URL": "postgres://x", "TLS_CERT_FILE": "c", "TLS_KEY_FILE": "k",
		"JWT_ACCESS_SECRET": strings.Repeat("a", 32), "JWT_REFRESH_SECRET": strings.Repeat("r", 32),
		"SECRETS_ENCRYPTION_KEY": strings.Repeat("07", 32),
	} {
		t.Setenv(k, v)
	}
}

func TestLoadRequiresValidSecretsKey(t *testing.T) {
	setRequired(t)
	cfg, err := Load()
	if err != nil || len(cfg.SecretsEncryptionKey) != 32 {
		t.Fatalf("valid config: key len=%d err=%v", len(cfg.SecretsEncryptionKey), err)
	}

	t.Setenv("SECRETS_ENCRYPTION_KEY", "")
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "SECRETS_ENCRYPTION_KEY") {
		t.Fatalf("missing key: err=%v, want it to name SECRETS_ENCRYPTION_KEY", err)
	}

	t.Setenv("SECRETS_ENCRYPTION_KEY", "too-short")
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "SECRETS_ENCRYPTION_KEY") {
		t.Fatalf("bad key: err=%v", err)
	}
}

func TestLoadRateLimitDefaultsAndValidation(t *testing.T) {
	setRequired(t)
	cfg, err := Load()
	if err != nil || cfg.AuthFailuresPerMinute != 10 || cfg.IngestPerMinute != 120 || cfg.MetricsRetentionDays != 30 {
		t.Fatalf("defaults = %+v err=%v", cfg, err)
	}
	t.Setenv("METRICS_RETENTION_DAYS", "0")
	if cfg, err = Load(); err != nil || cfg.MetricsRetentionDays != 0 {
		t.Fatalf("0 (keep forever) rejected: %+v err=%v", cfg, err)
	}
	t.Setenv("METRICS_RETENTION_DAYS", "-5")
	if _, err = Load(); err == nil {
		t.Fatal("negative METRICS_RETENTION_DAYS accepted")
	}
	t.Setenv("METRICS_RETENTION_DAYS", "")

	t.Setenv("RATE_LIMIT_INGEST_PER_MINUTE", "0")
	if cfg, err = Load(); err != nil || cfg.IngestPerMinute != 0 {
		t.Fatalf("0 (disabled) rejected: %+v err=%v", cfg, err)
	}
	for _, bad := range []string{"-1", "abc", "1.5"} {
		t.Setenv("RATE_LIMIT_INGEST_PER_MINUTE", bad)
		if _, err := Load(); err == nil {
			t.Errorf("RATE_LIMIT_INGEST_PER_MINUTE=%q accepted", bad)
		}
	}
}

func TestParseAllowedOrigins(t *testing.T) {
	got, err := ParseAllowedOrigins(" https://Panel.Example.com , http://localhost:5173,,")
	if err != nil || len(got) != 2 || got[0] != "https://panel.example.com" || got[1] != "http://localhost:5173" {
		t.Fatalf("ParseAllowedOrigins = %v, %v", got, err)
	}
	if got, err := ParseAllowedOrigins(""); err != nil || len(got) != 0 {
		t.Fatalf("empty = %v, %v", got, err)
	}
	for _, bad := range []string{"*", "panel.example.com", "https://panel.example.com/", "https://panel.example.com/app",
		"ftp://x.test", "https://", "https://u:p@x.test", "https://x.test?q=1"} {
		if _, err := ParseAllowedOrigins(bad); err == nil {
			t.Errorf("ParseAllowedOrigins(%q) accepted an invalid origin", bad)
		}
	}
}

func TestLoadRejectsInvalidCORSOrigins(t *testing.T) {
	setRequired(t)
	if cfg, err := Load(); err != nil || len(cfg.CORSAllowedOrigins) != 0 {
		t.Fatalf("unset: %v %v", cfg.CORSAllowedOrigins, err)
	}
	t.Setenv("CORS_ALLOWED_ORIGINS", "https://panel.example.com, http://localhost:5173")
	if cfg, err := Load(); err != nil || len(cfg.CORSAllowedOrigins) != 2 {
		t.Fatalf("valid: %v %v", cfg.CORSAllowedOrigins, err)
	}
	t.Setenv("CORS_ALLOWED_ORIGINS", "*")
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "CORS_ALLOWED_ORIGINS") {
		t.Fatalf("wildcard: err=%v, want it to name CORS_ALLOWED_ORIGINS", err)
	}
}

func TestLoadRejectsWeakOrIdenticalJWTSecrets(t *testing.T) {
	setRequired(t)
	for name, tc := range map[string][2]string{
		"access too short":  {strings.Repeat("a", 31), strings.Repeat("r", 32)},
		"refresh too short": {strings.Repeat("a", 32), "short"},
		"identical":         {strings.Repeat("s", 40), strings.Repeat("s", 40)},
	} {
		t.Setenv("JWT_ACCESS_SECRET", tc[0])
		t.Setenv("JWT_REFRESH_SECRET", tc[1])
		if _, err := Load(); err == nil || !strings.Contains(err.Error(), "JWT_") {
			t.Errorf("%s: err=%v, want a JWT secret error", name, err)
		}
	}
	t.Setenv("JWT_ACCESS_SECRET", strings.Repeat("a", 32))
	t.Setenv("JWT_REFRESH_SECRET", strings.Repeat("r", 32))
	if _, err := Load(); err != nil {
		t.Fatalf("exactly 32 distinct bytes rejected: %v", err)
	}
}

func TestLoadBootstrapAdmin(t *testing.T) {
	setRequired(t)
	if cfg, err := Load(); err != nil || cfg.BootstrapAdminEmail != "" {
		t.Fatalf("unset: %+v err=%v", cfg, err)
	}

	t.Setenv("BOOTSTRAP_ADMIN_EMAIL", "  Root@Example.COM ")
	t.Setenv("BOOTSTRAP_ADMIN_PASSWORD", "a-long-enough-password")
	cfg, err := Load()
	if err != nil || cfg.BootstrapAdminEmail != "root@example.com" || cfg.BootstrapAdminPassword != "a-long-enough-password" {
		t.Fatalf("valid: %+v err=%v", cfg, err)
	}

	t.Setenv("BOOTSTRAP_ADMIN_PASSWORD", "")
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "together") {
		t.Fatalf("email without password: err=%v", err)
	}
	t.Setenv("BOOTSTRAP_ADMIN_PASSWORD", "short")
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "BOOTSTRAP_ADMIN_PASSWORD") {
		t.Fatalf("weak password: err=%v", err)
	}
	t.Setenv("BOOTSTRAP_ADMIN_PASSWORD", "a-long-enough-password")
	t.Setenv("BOOTSTRAP_ADMIN_EMAIL", "not-an-email")
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "BOOTSTRAP_ADMIN_EMAIL") {
		t.Fatalf("bad email: err=%v", err)
	}
	t.Setenv("BOOTSTRAP_ADMIN_EMAIL", "")
	t.Setenv("BOOTSTRAP_ADMIN_PASSWORD", "a-long-enough-password")
	if _, err := Load(); err == nil {
		t.Fatal("password without email accepted")
	}
}

func TestLoadAutoMigrate(t *testing.T) {
	setRequired(t)
	if cfg, err := Load(); err != nil || !cfg.AutoMigrate {
		t.Fatalf("default: %+v err=%v, want AutoMigrate on", cfg, err)
	}
	for in, want := range map[string]bool{"true": true, "1": true, "YES": true, "on": true, "false": false, "0": false, "No": false, "off": false} {
		t.Setenv("AUTO_MIGRATE", in)
		if cfg, err := Load(); err != nil || cfg.AutoMigrate != want {
			t.Errorf("AUTO_MIGRATE=%q: %v err=%v, want %v", in, cfg != nil && cfg.AutoMigrate, err, want)
		}
	}
	t.Setenv("AUTO_MIGRATE", "maybe")
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "AUTO_MIGRATE") {
		t.Fatalf("invalid value: err=%v", err)
	}
}

func TestLoadPullCACertFile(t *testing.T) {
	setRequired(t)
	if cfg, err := Load(); err != nil || cfg.PullRootCAs != nil {
		t.Fatalf("unset: %+v err=%v, want no pool (verification off)", cfg, err)
	}

	dir := t.TempDir()
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "ca"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), IsCA: true, BasicConstraintsValid: true}
	der, _ := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	good := filepath.Join(dir, "ca.pem")
	os.WriteFile(good, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600)
	junk := filepath.Join(dir, "junk.pem")
	os.WriteFile(junk, []byte("not a certificate"), 0o600)

	t.Setenv("PULL_CA_CERT_FILE", good)
	if cfg, err := Load(); err != nil || cfg.PullRootCAs == nil {
		t.Fatalf("valid CA file: %+v err=%v", cfg, err)
	}
	for name, path := range map[string]string{"missing": filepath.Join(dir, "nope.pem"), "not PEM": junk} {
		t.Setenv("PULL_CA_CERT_FILE", path)
		if _, err := Load(); err == nil || !strings.Contains(err.Error(), "PULL_CA_CERT_FILE") {
			t.Errorf("%s: err=%v, want it to name PULL_CA_CERT_FILE (a typo must stop the server, not silently disable verification)", name, err)
		}
	}
}

// Sürüm politikası: Latest varsayılanı server'ın bildiği en güncel AGENT sürümü (server'ın kendi sürümü değil), Min boş (= "desteklenmiyor" yok);
// geçersiz bir değer açılışta yüksek sesle reddedilir.
func TestLoadAgentVersionPolicy(t *testing.T) {
	setRequired(t)
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.LatestAgentVersion != version.LatestAgent || cfg.MinSupportedAgentVersion != "" {
		t.Errorf("defaults = latest %q min %q, want %q and empty", cfg.LatestAgentVersion, cfg.MinSupportedAgentVersion, version.LatestAgent)
	}

	// Varsayılan, server'ın KENDİ sürümü değil bildiği en güncel agent sürümüdür: ikisi farklıyken ayırt edilir
	// (yeni bir server çıkınca değişmemiş agent'lar "güncelleme var" görünmemeli).
	oldVersion, oldAgent := version.Version, version.LatestAgent
	t.Cleanup(func() { version.Version, version.LatestAgent = oldVersion, oldAgent })
	version.Version, version.LatestAgent = "5.0.0", "1.4.2"
	if cfg, err = Load(); err != nil || cfg.LatestAgentVersion != "1.4.2" {
		t.Errorf("with server 5.0.0 and latest agent 1.4.2, default latest = %q (err %v); want 1.4.2", cfg.LatestAgentVersion, err)
	}
	version.Version, version.LatestAgent = oldVersion, oldAgent

	t.Setenv("LATEST_AGENT_VERSION", "2.0.1")
	t.Setenv("MIN_SUPPORTED_AGENT_VERSION", " 1.0.0 ")
	cfg, err = Load()
	if err != nil || cfg.LatestAgentVersion != "2.0.1" || cfg.MinSupportedAgentVersion != "1.0.0" {
		t.Fatalf("explicit = %+v err %v", cfg, err)
	}

	for _, bad := range []string{"v1.0.0", "1.0", "latest", "1.0.0; x"} {
		for _, key := range []string{"LATEST_AGENT_VERSION", "MIN_SUPPORTED_AGENT_VERSION"} {
			t.Setenv("LATEST_AGENT_VERSION", "1.0.0")
			t.Setenv("MIN_SUPPORTED_AGENT_VERSION", "1.0.0")
			t.Setenv(key, bad)
			if _, err := Load(); err == nil || !strings.Contains(err.Error(), key) {
				t.Errorf("%s=%q: err = %v, want it to name %s", key, bad, err, key)
			}
		}
	}
}

func TestParsePanelBaseURL(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"", ""},
		{"   ", ""},
		{"https://panel.example.com", "https://panel.example.com"},
		{"  https://Panel.Example.com/  ", "https://panel.example.com"},
		{"http://localhost:8080", "http://localhost:8080"},
		{"https://panel.example.com:8443///", "https://panel.example.com:8443"},
	} {
		got, err := ParsePanelBaseURL(tc.in)
		if err != nil || got != tc.want {
			t.Errorf("ParsePanelBaseURL(%q) = %q, %v; want %q", tc.in, got, err, tc.want)
		}
	}
	for _, bad := range []string{
		"panel.example.com",           // şema yok
		"ftp://panel.example.com",     // http/https dışı
		"https://panel.example.com/x", // yol
		"https://panel.example.com?a=1",
		"https://panel.example.com/#f",
		"https://user:pw@panel.example.com", // kimlik bilgisi
		"https://",                          // ana makine yok
		"javascript:alert(1)",
	} {
		if got, err := ParsePanelBaseURL(bad); err == nil {
			t.Errorf("ParsePanelBaseURL(%q) = %q, want an error", bad, got)
		}
	}
}
