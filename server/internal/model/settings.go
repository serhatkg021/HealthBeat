package model

import (
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

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

	UpdatedAt time.Time
	UpdatedBy *uuid.UUID
}

// NotificationChannel, sistem düzeyindeki bir bildirim kanalıdır (notification_channels satırı). Secret çözülmüş şifre ya da
// token'dır: yalnızca server içinde kullanılır, API'ye asla verilmez.
type NotificationChannel struct {
	Channel       string
	Provider      string // kanalın gerçekleştirimi (email → smtp)
	Enabled       bool
	Config        json.RawMessage // sır olmayan ayarlar; biçimini provider belirler
	Secret        string
	SecretSet     bool
	OwnerMinLevel string // sistem sahiplerine bu seviye ve üzeri gönderilir
	VerifiedAt    *time.Time
	UpdatedAt     time.Time
	UpdatedBy     *uuid.UUID
}

// NotificationOwner, her alert'in bildirimini alan bir sistem sahibidir (panel kullanıcısı olması gerekmez). E-postası
// e-posta kanalında, telefonu SMS kanalında kullanılır; EmailEnabled/SMSEnabled o kanaldan almak isteyip istemediğidir.
type NotificationOwner struct {
	ID           uuid.UUID `json:"id"`
	Name         string    `json:"name"`
	Email        *string   `json:"email"`
	Phone        *string   `json:"phone"`
	EmailEnabled bool      `json:"email_enabled"`
	SMSEnabled   bool      `json:"sms_enabled"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// Normalize, sahibin alanlarını doğrular ve olağan biçime getirir: ad boş olamaz, e-posta sade ve küçük harfli, telefon
// yalnızca rakam (başta isteğe bağlı +); en az biri dolu olmalı. Boş e-posta/telefon nil'e çevrilir.
func (o *NotificationOwner) Normalize() error {
	o.Name = strings.TrimSpace(o.Name)
	if o.Name == "" || utf8.RuneCountInString(o.Name) > 200 {
		return errors.New("ad boş olamaz ve en fazla 200 karakter olabilir")
	}
	if o.Email != nil && strings.TrimSpace(*o.Email) == "" {
		o.Email = nil
	}
	if o.Email != nil {
		email, err := NormalizeEmail(*o.Email)
		if err != nil {
			return err
		}
		o.Email = &email
	}
	if o.Phone != nil {
		phone, err := NormalizePhone(*o.Phone)
		if err != nil {
			return err
		}
		o.Phone = &phone
		if phone == "" {
			o.Phone = nil
		}
	}
	if o.Email == nil && o.Phone == nil {
		return errors.New("e-posta ya da telefondan en az biri girilmeli")
	}
	return nil
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
