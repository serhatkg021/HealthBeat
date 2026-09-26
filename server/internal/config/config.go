// Package config, HealthBeat server yapılandırmasını ortamdan yükler (geliştirme için isteğe
// bağlı olarak yerel bir .env dosyasından beslenir).
package config

import (
	"bufio"
	"crypto/x509"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"healthbeat-server/internal/clientip"
	"healthbeat-server/internal/logging"
	"healthbeat-server/internal/model"
	"healthbeat-server/internal/secretbox"
	"healthbeat-server/internal/version"
)

type Config struct {
	DatabaseURL      string
	HTTPAddr         string
	TLSCertFile      string
	TLSKeyFile       string
	JWTAccessSecret  []byte
	JWTRefreshSecret []byte
	// SecretsEncryptionKey (32 bayt) pull secret'ları at-rest şifreler. Zorunlu: server onsuz
	// açılmayı reddeder, secret'ları açık saklamaz. Kaybedilirse saklı pull secret'lar geri
	// alınamaz (kurtarmak için etkilenen host'ların kimlik bilgilerini yenile).
	SecretsEncryptionKey []byte
	AccessTokenTTL       time.Duration
	RefreshTokenTTL      time.Duration

	// SMTP isteğe bağlıdır: boş SMTPHost, notify.Mailer'ın hata vermek yerine yalnızca log
	// modunda çalışması demektir (bkz. docs/MIMARI.md bölüm 8).
	SMTPHost     string
	SMTPPort     string
	SMTPUsername string
	SMTPPassword string
	SMTPFrom     string

	// PanelBaseURL, panelin kullanıcıya görünen adresidir (scheme://host[:port]); şifre sıfırlama e-postalarındaki
	// bağlantının kökü olarak kullanılır. Boşsa e-posta ile şifre sıfırlama kapalıdır. İsteğin Host başlığından
	// türetilmez: aksi halde saldırgan sıfırlama bağlantısını kendi alan adına yönlendirebilirdi.
	PanelBaseURL string

	// Hız sınırları (dakikada). Sıfır ilgili sınırlayıcıyı kapatır. AuthFailuresPerMinute, başarısız
	// panel girişleri/yenilemeleri için kaynak IP başına ve ayrıca başarısız push-host
	// kimlik doğrulamaları için ayrı olarak sayılır. IngestPerMinute doğrulanmış push host
	// başına sayılır.
	AuthFailuresPerMinute int
	IngestPerMinute       int

	// CORSAllowedOrigins, API'yi çağırmasına izin verilen tarayıcı origin'lerini listeler; panel
	// farklı bir origin'den (static host) sunulduğunda gerekir. Boş = CORS başlığı yok.
	// httpapi.ParseAllowedOrigins ile doğrulanır.
	CORSAllowedOrigins []string

	// TrustedProxies, X-Forwarded-For'una güvenilen reverse proxy'lerdir (IP, CIDR ya da docker'daki "panel" gibi bir
	// host adı). İstemci IP'si (hız sınırları, denetim kaydı) yalnızca istek bunlardan birinden geldiğinde başlıktan
	// okunur; boşsa her zaman TCP eşidir. Bkz. clientip.
	TrustedProxies clientip.Proxies

	// BootstrapAdminEmail/Password, açılışta ilk super_admin'i oluşturur, ama yalnızca hiç
	// super_admin yokken (bu yüzden ayarlı bırakılabilir ya da ilk açılıştan sonra kaldırılabilir).
	// Hesap ilk girişte yeni bir şifre seçmek zorundadır. İkisi birden ya da hiçbiri.
	BootstrapAdminEmail    string
	BootstrapAdminPassword string

	// PullRootCAs, PULL_CA_CERT_FILE'dan (bir PEM dosyası) gelir ve pull scheduler'ın poll ettiği
	// agent'ların sertifikalarını doğrulamasını sağlar — yalnızca bu otoriteler güvenilir sayılır.
	// Nil (varsayılan) doğrulama kapalı demektir; bkz. pullscheduler.New.
	PullRootCAs *x509.CertPool

	// AutoMigrate: server açılırken bekleyen veritabanı migration'larını uygula (varsayılan).
	// False ise bekleyen herhangi biri varken server açılmayı reddeder ve migration'lar
	// `healthbeat-server migrate up` ile açıkça uygulanır.
	AutoMigrate bool

	// MetricsRetentionDays, metrik örneklerinin retention işi onları silmeden önce ne kadar
	// saklandığıdır. 0 hepsini sonsuza dek tutar.
	MetricsRetentionDays int

	// LatestAgentVersion/MinSupportedAgentVersion, panelin agent'ları "güncel / güncellenmeli /
	// desteklenmiyor" diye sınıflandırdığı sürüm politikasıdır (bkz. docs/COMPATIBILITY.md).
	// Latest varsayılanı server derlemesinin bildiği en güncel AGENT sürümüdür (version.LatestAgent; server'ın kendi
	// sürümü değil: iki bağımsız sürüm hattı vardır); Min boşsa
	// "desteklenmiyor" durumu hiç üretilmez. Politika yalnızca bilgilendirir: hiçbir agent
	// reddedilmez.
	LatestAgentVersion       string
	MinSupportedAgentVersion string

	// LogLevel (LOG_LEVEL: debug|info|warn|error, varsayılan info) ve LogFormat (LOG_FORMAT: text|json, varsayılan
	// text) server logunu ayarlar. Başarılı ingest ve /healthz istekleri yalnızca debug'da görünür.
	LogLevel  slog.Level
	LogFormat string
	// LogErrorBodyBytes (LOG_ERROR_BODY_BYTES, varsayılan 4096), hata alan (4xx/5xx) bir isteğin loga yazılan
	// istek/yanıt gövdesinin azami boyutudur; 0 gövde yazmaz. Şifre/token/secret alanları her zaman maskelenir.
	LogErrorBodyBytes int

	// LogFile (LOG_FILE), logun stdout'a ek olarak yazıldığı kalıcı dosyadır; o dizinde günlük dosyalar tutulur
	// (server-YYYY-MM-DD.log, eski günler gzip'li). Boş = kapalı (bare-metal varsayılanı; Docker Compose açar).
	// LogFileMaxAgeDays (varsayılan 14) bugün dahil kaç günün tutulacağı, LogFileMaxTotalMB (varsayılan 1024) bütün log
	// dosyalarının toplam üst sınırıdır. Bkz. logging.FileWriter.
	LogFile           string
	LogFileMaxAgeDays int
	LogFileMaxTotalMB int
}

// maxLogErrorBodyBytes, LOG_ERROR_BODY_BYTES'ın üst sınırıdır: API zaten 1 MiB'tan büyük gövde kabul etmez.
const maxLogErrorBodyBytes = 1 << 20

func Load() (*Config, error) {
	loadDotEnv(".env")

	var missing []string
	req := func(key string) string {
		v := os.Getenv(key)
		if v == "" {
			missing = append(missing, key)
		}
		return v
	}

	cfg := &Config{
		DatabaseURL:      req("DATABASE_URL"),
		HTTPAddr:         getDefault("HTTP_ADDR", ":8443"),
		TLSCertFile:      req("TLS_CERT_FILE"),
		TLSKeyFile:       req("TLS_KEY_FILE"),
		JWTAccessSecret:  []byte(req("JWT_ACCESS_SECRET")),
		JWTRefreshSecret: []byte(req("JWT_REFRESH_SECRET")),

		SMTPHost:     os.Getenv("SMTP_HOST"),
		SMTPPort:     getDefault("SMTP_PORT", "587"),
		SMTPUsername: os.Getenv("SMTP_USERNAME"),
		SMTPPassword: os.Getenv("SMTP_PASSWORD"),
		SMTPFrom:     os.Getenv("SMTP_FROM"),
	}

	secretsKey := req("SECRETS_ENCRYPTION_KEY")

	if len(missing) > 0 {
		return nil, fmt.Errorf("missing required env vars: %s", strings.Join(missing, ", "))
	}

	if err := validateJWTSecrets(cfg.JWTAccessSecret, cfg.JWTRefreshSecret); err != nil {
		return nil, err
	}

	key, err := secretbox.ParseKey(secretsKey)
	if err != nil {
		return nil, fmt.Errorf("invalid SECRETS_ENCRYPTION_KEY: %w", err)
	}
	cfg.SecretsEncryptionKey = key

	accessTTL, err := getDurationDefault("ACCESS_TOKEN_TTL", 15*time.Minute)
	if err != nil {
		return nil, err
	}
	refreshTTL, err := getDurationDefault("REFRESH_TOKEN_TTL", 7*24*time.Hour)
	if err != nil {
		return nil, err
	}
	cfg.AccessTokenTTL = accessTTL
	cfg.RefreshTokenTTL = refreshTTL

	if cfg.CORSAllowedOrigins, err = ParseAllowedOrigins(os.Getenv("CORS_ALLOWED_ORIGINS")); err != nil {
		return nil, fmt.Errorf("invalid CORS_ALLOWED_ORIGINS: %w", err)
	}
	if cfg.PanelBaseURL, err = ParsePanelBaseURL(os.Getenv("PANEL_BASE_URL")); err != nil {
		return nil, fmt.Errorf("invalid PANEL_BASE_URL: %w", err)
	}
	if cfg.TrustedProxies, err = clientip.ParseProxies(os.Getenv("TRUSTED_PROXIES")); err != nil {
		return nil, fmt.Errorf("invalid TRUSTED_PROXIES: %w", err)
	}
	bootstrapEmail, bootstrapPassword := os.Getenv("BOOTSTRAP_ADMIN_EMAIL"), os.Getenv("BOOTSTRAP_ADMIN_PASSWORD")
	if (bootstrapEmail == "") != (bootstrapPassword == "") {
		return nil, fmt.Errorf("BOOTSTRAP_ADMIN_EMAIL and BOOTSTRAP_ADMIN_PASSWORD must be set together")
	}
	if bootstrapEmail != "" {
		normalized, err := model.NormalizeEmail(bootstrapEmail)
		if err != nil {
			return nil, fmt.Errorf("invalid BOOTSTRAP_ADMIN_EMAIL: %w", err)
		}
		if err := model.ValidatePassword(bootstrapPassword); err != nil {
			return nil, fmt.Errorf("invalid BOOTSTRAP_ADMIN_PASSWORD: %w", err)
		}
		cfg.BootstrapAdminEmail, cfg.BootstrapAdminPassword = normalized, bootstrapPassword
	}
	if caFile := os.Getenv("PULL_CA_CERT_FILE"); caFile != "" {
		if cfg.PullRootCAs, err = loadCertPool(caFile); err != nil {
			return nil, fmt.Errorf("invalid PULL_CA_CERT_FILE: %w", err)
		}
	}
	if cfg.AutoMigrate, err = getBoolDefault("AUTO_MIGRATE", true); err != nil {
		return nil, err
	}
	if cfg.LatestAgentVersion, err = semverDefault("LATEST_AGENT_VERSION", version.LatestAgent); err != nil {
		return nil, err
	}
	if cfg.MinSupportedAgentVersion, err = semverDefault("MIN_SUPPORTED_AGENT_VERSION", ""); err != nil {
		return nil, err
	}
	if cfg.MetricsRetentionDays, err = getIntDefault("METRICS_RETENTION_DAYS", 30); err != nil {
		return nil, err
	}
	if cfg.AuthFailuresPerMinute, err = getIntDefault("RATE_LIMIT_AUTH_FAILURES_PER_MINUTE", 10); err != nil {
		return nil, err
	}
	if cfg.IngestPerMinute, err = getIntDefault("RATE_LIMIT_INGEST_PER_MINUTE", 120); err != nil {
		return nil, err
	}
	if cfg.LogLevel, err = logging.ParseLevel(os.Getenv("LOG_LEVEL")); err != nil {
		return nil, fmt.Errorf("invalid LOG_LEVEL: %w", err)
	}
	if cfg.LogFormat, err = logging.ParseFormat(os.Getenv("LOG_FORMAT")); err != nil {
		return nil, fmt.Errorf("invalid LOG_FORMAT: %w", err)
	}
	if cfg.LogErrorBodyBytes, err = getIntDefault("LOG_ERROR_BODY_BYTES", 4096); err != nil {
		return nil, err
	}
	if cfg.LogErrorBodyBytes > maxLogErrorBodyBytes {
		return nil, fmt.Errorf("invalid LOG_ERROR_BODY_BYTES: must be at most %d", maxLogErrorBodyBytes)
	}
	cfg.LogFile = strings.TrimSpace(os.Getenv("LOG_FILE"))
	if cfg.LogFileMaxAgeDays, err = getIntDefault("LOG_FILE_MAX_AGE_DAYS", 14); err != nil || cfg.LogFileMaxAgeDays < 1 {
		return nil, fmt.Errorf("invalid LOG_FILE_MAX_AGE_DAYS: must be a positive number of days")
	}
	if cfg.LogFileMaxTotalMB, err = getIntDefault("LOG_FILE_MAX_TOTAL_MB", 1024); err != nil || cfg.LogFileMaxTotalMB < 1 {
		return nil, fmt.Errorf("invalid LOG_FILE_MAX_TOTAL_MB: must be a positive number of megabytes")
	}

	return cfg, nil
}

// loadDotEnv, gerçek ortamda zaten ayarlı hiçbir değişkeni ezmeden, süreç ortam değişkenlerini
// bir .env dosyasından elinden geldiğince doldurur. Dosyanın olmaması hata değildir — .env
// yerel geliştirme kolaylığıdır, şart değil.
func loadDotEnv(path string) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		value = strings.Trim(strings.TrimSpace(value), `"'`)
		if _, exists := os.LookupEnv(key); !exists {
			os.Setenv(key, value)
		}
	}
}

func getDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func getDurationDefault(key string, def time.Duration) (time.Duration, error) {
	v := os.Getenv(key)
	if v == "" {
		return def, nil
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return 0, fmt.Errorf("invalid %s: %w", key, err)
	}
	return d, nil
}

func getIntDefault(key string, def int) (int, error) {
	v := os.Getenv(key)
	if v == "" {
		return def, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 0 {
		return 0, fmt.Errorf("invalid %s: must be a non-negative integer", key)
	}
	return n, nil
}

// ParseAllowedOrigins bir CORS izin listesini doğrular. Her girdi tam bir origin olmalı
// (scheme://host[:port], yol yok, sonda eğik çizgi yok). "*" bilerek reddedilir: panel bearer
// token'larla kimlik doğrular ve her sitenin cross-origin isteğine yanıt veren bir API kazara
// açılacak bir şey değildir.
func ParseAllowedOrigins(raw string) ([]string, error) {
	var out []string
	for _, o := range strings.Split(raw, ",") {
		o = strings.TrimSpace(o)
		if o == "" {
			continue
		}
		u, err := url.Parse(o)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" ||
			u.Path != "" || u.RawQuery != "" || u.Fragment != "" || u.User != nil {
			return nil, fmt.Errorf("%q is not a valid origin (want scheme://host[:port], e.g. https://panel.example.com)", o)
		}
		out = append(out, u.Scheme+"://"+strings.ToLower(u.Host))
	}
	return out, nil
}

// ParsePanelBaseURL, panelin dış adresini doğrular ve olağan biçime getirir: scheme://host[:port] (yol, sorgu ve
// sondaki eğik çizgi yok). Boş girdi geçerlidir ve "" döner (özellik kapalı).
func ParsePanelBaseURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", nil
	}
	u, err := url.Parse(strings.TrimRight(raw, "/"))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" ||
		u.Path != "" || u.RawQuery != "" || u.Fragment != "" || u.User != nil {
		return "", fmt.Errorf("%q is not a valid panel address (want scheme://host[:port], e.g. https://panel.example.com)", raw)
	}
	return u.Scheme + "://" + strings.ToLower(u.Host), nil
}

// minJWTSecretBytes: 256 bitlik hash çıktısından kısa HS256 imza anahtarları, ele geçirilmiş
// herhangi bir token'dan çevrimdışı olarak gereksiz yere kolay kırılır.
const minJWTSecretBytes = 32

func validateJWTSecrets(access, refresh []byte) error {
	if len(access) < minJWTSecretBytes || len(refresh) < minJWTSecretBytes {
		return fmt.Errorf("JWT_ACCESS_SECRET and JWT_REFRESH_SECRET must each be at least %d bytes (try: openssl rand -base64 48)", minJWTSecretBytes)
	}
	if string(access) == string(refresh) {
		return fmt.Errorf("JWT_ACCESS_SECRET and JWT_REFRESH_SECRET must differ")
	}
	return nil
}

func getBoolDefault(key string, def bool) (bool, error) {
	v := strings.ToLower(strings.TrimSpace(os.Getenv(key)))
	switch v {
	case "":
		return def, nil
	case "1", "true", "yes", "on":
		return true, nil
	case "0", "false", "no", "off":
		return false, nil
	}
	return false, fmt.Errorf("invalid %s: use true or false", key)
}

// DatabaseURLOnly yalnızca DATABASE_URL'i (ve .env dosyasını) yükler; server yapılandırmasının
// geri kalanı olmadan çalışması gereken `migrate` gibi komutlar içindir.
func DatabaseURLOnly() (string, error) {
	loadDotEnv(".env")
	if v := os.Getenv("DATABASE_URL"); v != "" {
		return v, nil
	}
	return "", fmt.Errorf("missing required env var: DATABASE_URL")
}

// loadCertPool bir PEM dosyasından havuz kurar; dosya okunamıyorsa ya da hiç sertifika
// içermiyorsa (açılışta) yüksek sesle başarısız olur.
func loadCertPool(path string) (*x509.CertPool, error) {
	pem, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(pem) {
		return nil, fmt.Errorf("%s contains no PEM-encoded certificate", path)
	}
	return pool, nil
}

// semverDefault, key'i SemVer olarak okur; boşsa def döner.
func semverDefault(key, def string) (string, error) {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return def, nil
	}
	if !version.ValidSemver(v) {
		return "", fmt.Errorf("invalid %s %q: use MAJOR.MINOR.PATCH, e.g. 1.2.0", key, v)
	}
	return v, nil
}
