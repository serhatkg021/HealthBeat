// Package config, HealthBeat server yapılandırmasını ortamdan yükler (geliştirme için isteğe
// bağlı olarak yerel bir .env dosyasından beslenir).
package config

import (
	"bufio"
	"crypto/x509"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"

	"healthbeat-server/internal/clientip"
	"healthbeat-server/internal/logging"
	"healthbeat-server/internal/model"
	"healthbeat-server/internal/secretbox"
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

	// DBMaxConns (DB_MAX_CONNS), veritabanı bağlantı havuzunun üst sınırıdır. 0 = DATABASE_URL'deki pool_max_conns,
	// o da yoksa pgx varsayılanı (4 ile CPU sayısından büyüğü).
	DBMaxConns int

	// LogFormat (LOG_FORMAT: text|json, varsayılan text) server logunun biçimidir. Log seviyesi ve hata gövdesi
	// loglama panelden değişir (bkz. internal/settings).
	LogFormat string

	// LogFile (LOG_FILE), logun stdout'a ek olarak yazıldığı kalıcı dosyadır; o dizinde günlük dosyalar tutulur
	// (server-YYYY-MM-DD.log, eski günler gzip'li). Boş = kapalı (bare-metal varsayılanı; Docker Compose açar). Saklama
	// sınırları panelden değişir. Bkz. logging.FileWriter.
	LogFile string

	// Obsolete, artık okunmayan (panele taşınan) ama ortamda hâlâ dolu olan değişkenlerin adlarıdır; server bunları
	// açılışta uyarı olarak loglar.
	Obsolete []string
}

// maxDBMaxConns, DB_MAX_CONNS'ın üst sınırıdır; PostgreSQL'in varsayılan max_connections'ı 100'dür.
const maxDBMaxConns = 1000

// ObsoleteVars, 2.0.0'da env'den kaldırılıp panele taşınan değişkenlerdir (app_settings; SMTP ise e-posta kanalı,
// notification_channels).
var ObsoleteVars = []string{
	"LATEST_AGENT_VERSION", "MIN_SUPPORTED_AGENT_VERSION",
	"METRICS_RETENTION_DAYS", "AUDIT_RETENTION_DAYS", "RESOLVED_ALERT_RETENTION_DAYS",
	"ACCESS_TOKEN_TTL", "REFRESH_TOKEN_TTL",
	"RATE_LIMIT_AUTH_FAILURES_PER_MINUTE", "RATE_LIMIT_INGEST_PER_MINUTE",
	"PANEL_BASE_URL",
	"LOG_LEVEL", "LOG_ERROR_BODY_BYTES", "LOG_FILE_MAX_AGE_DAYS", "LOG_FILE_MAX_TOTAL_MB",
	"SMTP_HOST", "SMTP_PORT", "SMTP_USERNAME", "SMTP_PASSWORD", "SMTP_FROM",
}

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

	if cfg.CORSAllowedOrigins, err = ParseAllowedOrigins(os.Getenv("CORS_ALLOWED_ORIGINS")); err != nil {
		return nil, fmt.Errorf("invalid CORS_ALLOWED_ORIGINS: %w", err)
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
	if cfg.DBMaxConns, err = getIntDefault("DB_MAX_CONNS", 0); err != nil {
		return nil, err
	}
	if cfg.DBMaxConns > maxDBMaxConns {
		return nil, fmt.Errorf("invalid DB_MAX_CONNS: must be at most %d", maxDBMaxConns)
	}
	if cfg.LogFormat, err = logging.ParseFormat(os.Getenv("LOG_FORMAT")); err != nil {
		return nil, fmt.Errorf("invalid LOG_FORMAT: %w", err)
	}
	cfg.LogFile = strings.TrimSpace(os.Getenv("LOG_FILE"))
	for _, key := range ObsoleteVars {
		if strings.TrimSpace(os.Getenv(key)) != "" { // compose boş geçirir: yalnızca dolu olanlar uyarılır
			cfg.Obsolete = append(cfg.Obsolete, key)
		}
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
