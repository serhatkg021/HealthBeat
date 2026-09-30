package model

import (
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

	UpdatedAt time.Time
	UpdatedBy *uuid.UUID
}
