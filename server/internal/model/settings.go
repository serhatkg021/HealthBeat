package model

import (
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
)

// AppSettings, panelden değiştirilebilen çalışma zamanı ayarlarıdır (app_settings tablosunun tek satırı). Varsayılanları
// ve sınırları veritabanındadır; okuma/yazma ve doğrulama internal/settings'tedir.
type AppSettings struct {
	// Agent sürüm politikası (yalnızca bilgilendirir; bkz. docs/COMPATIBILITY.md). "" = tanımsız.
	LatestAgentVersion       string
	MinSupportedAgentVersion string

	// Saklama süreleri (gün); 0 = sonsuza dek sakla.
	MetricsRetentionDays       int
	AuditRetentionDays         int
	ResolvedAlertRetentionDays int

	AccessTokenTTL  time.Duration
	RefreshTokenTTL time.Duration

	// Hız sınırları (dakikada); 0 = kapalı.
	RateLimitAuthFailuresPerMinute int
	RateLimitIngestPerMinute       int

	// PanelBaseURL, panelin kullanıcıya görünen adresidir (scheme://host[:port]); "" = e-posta ile şifre sıfırlama kapalı.
	PanelBaseURL string

	LogLevel          string // debug | info | warn | error
	LogErrorBodyBytes int
	LogFileMaxAgeDays int
	LogFileMaxTotalMB int

	// Timezone, kurulumun saat dilimidir (IANA adı, ör. Europe/Istanbul): tekrarlı bakım pencereleri ve bildirimlerdeki
	// zamanlar buna göredir. "" = server sürecinin TZ'si, o da yoksa UTC (bkz. internal/tz).
	Timezone string

	UpdatedAt time.Time
	UpdatedBy *uuid.UUID
}

// NotificationChannel, sistem düzeyindeki bir bildirim kanalıdır (notification_channels satırı). Secret çözülmüş şifre ya da
// token'dır: yalnızca server içinde kullanılır, API'ye asla verilmez.
type NotificationChannel struct {
	Channel   string
	Provider  string // kanalın gerçekleştirimi (email → smtp)
	Enabled   bool
	Config    json.RawMessage // sır olmayan ayarlar; biçimini provider belirler
	Secret    string
	SecretSet bool
	// SecretUnreadable, kayıtlı şifrenin bu server'ın anahtarıyla çözülemediğini söyler (SECRETS_ENCRYPTION_KEY değişmiş);
	// şifre yeniden girilene ya da silinene kadar kanal gönderemez.
	SecretUnreadable bool
	VerifiedAt       *time.Time
	UpdatedAt        time.Time
	UpdatedBy        *uuid.UUID
}

var phonePattern = regexp.MustCompile(`^\+?[0-9]{7,15}$`)

// NormalizePhone, boşluk, tire, nokta ve parantezleri atar; kalan "+905551234567" gibi 7–15 rakam (başta isteğe bağlı +)
// olmalıdır. Boş girdi "" döner.
func NormalizePhone(s string) (string, error) {
	s = strings.NewReplacer(" ", "", "-", "", ".", "", "(", "", ")", "").Replace(strings.TrimSpace(s))
	if s == "" {
		return "", nil
	}
	if !phonePattern.MatchString(s) {
		return "", errors.New("telefon +905551234567 gibi 7–15 rakamdan oluşmalı")
	}
	return s, nil
}
