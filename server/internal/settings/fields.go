package settings

import (
	"fmt"
	"math"
	"slices"
	"strings"
	"time"

	"healthbeat-server/internal/config"
	"healthbeat-server/internal/model"
	"healthbeat-server/internal/version"
)

// Field, bir ayarın adıdır; app_settings'teki sütun adı ve API'deki alan adıyla aynıdır. Süreler saniye cinsindendir.
type Field string

const (
	FieldLatestAgentVersion             Field = "latest_agent_version"
	FieldMinSupportedAgentVersion       Field = "min_supported_agent_version"
	FieldMetricsRetentionDays           Field = "metrics_retention_days"
	FieldAuditRetentionDays             Field = "audit_retention_days"
	FieldResolvedAlertRetentionDays     Field = "resolved_alert_retention_days"
	FieldAccessTokenTTLSeconds          Field = "access_token_ttl_seconds"
	FieldRefreshTokenTTLSeconds         Field = "refresh_token_ttl_seconds"
	FieldRateLimitAuthFailuresPerMinute Field = "rate_limit_auth_failures_per_minute"
	FieldRateLimitIngestPerMinute       Field = "rate_limit_ingest_per_minute"
	FieldPanelBaseURL                   Field = "panel_base_url"
	FieldLogLevel                       Field = "log_level"
	FieldLogErrorBodyBytes              Field = "log_error_body_bytes"
	FieldLogFileMaxAgeDays              Field = "log_file_max_age_days"
	FieldLogFileMaxTotalMB              Field = "log_file_max_total_mb"
)

// Fields, bütün ayarlardır (sabit sıra; store.SettingsColumns ile aynı küme — bkz. TestFieldsMatchStoreColumns).
var Fields = []Field{
	FieldLatestAgentVersion, FieldMinSupportedAgentVersion,
	FieldMetricsRetentionDays, FieldAuditRetentionDays, FieldResolvedAlertRetentionDays,
	FieldAccessTokenTTLSeconds, FieldRefreshTokenTTLSeconds,
	FieldRateLimitAuthFailuresPerMinute, FieldRateLimitIngestPerMinute,
	FieldPanelBaseURL,
	FieldLogLevel, FieldLogErrorBodyBytes, FieldLogFileMaxAgeDays, FieldLogFileMaxTotalMB,
}

// intRange, sayısal bir ayarın izin verilen aralığıdır; migration 000005'teki CHECK kısıtlarıyla aynıdır
// (bkz. TestLimitsMatchDatabase). Üst sınırı olmayanlar INTEGER'ın üst sınırını kullanır.
type intRange struct{ min, max int }

const noMax = math.MaxInt32

var intRanges = map[Field]intRange{
	FieldMetricsRetentionDays:           {0, noMax},
	FieldAuditRetentionDays:             {0, noMax},
	FieldResolvedAlertRetentionDays:     {0, noMax},
	FieldAccessTokenTTLSeconds:          {60, 86400},
	FieldRefreshTokenTTLSeconds:         {3600, 7776000},
	FieldRateLimitAuthFailuresPerMinute: {0, noMax},
	FieldRateLimitIngestPerMinute:       {0, noMax},
	FieldLogErrorBodyBytes:              {0, 1 << 20},
	FieldLogFileMaxAgeDays:              {1, noMax},
	FieldLogFileMaxTotalMB:              {1, noMax},
}

var logLevels = []string{"debug", "info", "warn", "error"}

// FieldError, bir ayarın neden kabul edilmediğini söyler; Message istemciye gösterilir.
type FieldError struct {
	Field   Field
	Message string
}

func (e *FieldError) Error() string { return string(e.Field) + ": " + e.Message }

// Patch, bir ayar değişikliğidir: nil alan "dokunma" demektir. Sürüm alanlarında "" tanımsız (NULL) demektir.
type Patch struct {
	LatestAgentVersion             *string `json:"latest_agent_version"`
	MinSupportedAgentVersion       *string `json:"min_supported_agent_version"`
	MetricsRetentionDays           *int    `json:"metrics_retention_days"`
	AuditRetentionDays             *int    `json:"audit_retention_days"`
	ResolvedAlertRetentionDays     *int    `json:"resolved_alert_retention_days"`
	AccessTokenTTLSeconds          *int    `json:"access_token_ttl_seconds"`
	RefreshTokenTTLSeconds         *int    `json:"refresh_token_ttl_seconds"`
	RateLimitAuthFailuresPerMinute *int    `json:"rate_limit_auth_failures_per_minute"`
	RateLimitIngestPerMinute       *int    `json:"rate_limit_ingest_per_minute"`
	PanelBaseURL                   *string `json:"panel_base_url"`
	LogLevel                       *string `json:"log_level"`
	LogErrorBodyBytes              *int    `json:"log_error_body_bytes"`
	LogFileMaxAgeDays              *int    `json:"log_file_max_age_days"`
	LogFileMaxTotalMB              *int    `json:"log_file_max_total_mb"`
}

// values, Patch'te verilen alanları döndürür.
func (p Patch) values() map[Field]any {
	out := map[Field]any{}
	str := func(f Field, v *string) {
		if v != nil {
			out[f] = *v
		}
	}
	num := func(f Field, v *int) {
		if v != nil {
			out[f] = *v
		}
	}
	str(FieldLatestAgentVersion, p.LatestAgentVersion)
	str(FieldMinSupportedAgentVersion, p.MinSupportedAgentVersion)
	num(FieldMetricsRetentionDays, p.MetricsRetentionDays)
	num(FieldAuditRetentionDays, p.AuditRetentionDays)
	num(FieldResolvedAlertRetentionDays, p.ResolvedAlertRetentionDays)
	num(FieldAccessTokenTTLSeconds, p.AccessTokenTTLSeconds)
	num(FieldRefreshTokenTTLSeconds, p.RefreshTokenTTLSeconds)
	num(FieldRateLimitAuthFailuresPerMinute, p.RateLimitAuthFailuresPerMinute)
	num(FieldRateLimitIngestPerMinute, p.RateLimitIngestPerMinute)
	str(FieldPanelBaseURL, p.PanelBaseURL)
	str(FieldLogLevel, p.LogLevel)
	num(FieldLogErrorBodyBytes, p.LogErrorBodyBytes)
	num(FieldLogFileMaxAgeDays, p.LogFileMaxAgeDays)
	num(FieldLogFileMaxTotalMB, p.LogFileMaxTotalMB)
	return out
}

// Values, ayarları alan adına göre döndürür (sürümler string, "" = tanımsız; süreler saniye). Değişiklik listesi,
// varsayılanla karşılaştırma ve API yanıtı bu biçimi kullanır.
func Values(s model.AppSettings) map[Field]any {
	return map[Field]any{
		FieldLatestAgentVersion:             s.LatestAgentVersion,
		FieldMinSupportedAgentVersion:       s.MinSupportedAgentVersion,
		FieldMetricsRetentionDays:           s.MetricsRetentionDays,
		FieldAuditRetentionDays:             s.AuditRetentionDays,
		FieldResolvedAlertRetentionDays:     s.ResolvedAlertRetentionDays,
		FieldAccessTokenTTLSeconds:          int(s.AccessTokenTTL / time.Second),
		FieldRefreshTokenTTLSeconds:         int(s.RefreshTokenTTL / time.Second),
		FieldRateLimitAuthFailuresPerMinute: s.RateLimitAuthFailuresPerMinute,
		FieldRateLimitIngestPerMinute:       s.RateLimitIngestPerMinute,
		FieldPanelBaseURL:                   s.PanelBaseURL,
		FieldLogLevel:                       s.LogLevel,
		FieldLogErrorBodyBytes:              s.LogErrorBodyBytes,
		FieldLogFileMaxAgeDays:              s.LogFileMaxAgeDays,
		FieldLogFileMaxTotalMB:              s.LogFileMaxTotalMB,
	}
}

// normalize, verilen değerleri doğrular ve olağan biçime getirir (ör. panel adresi küçük harfli, sondaki / yok). Alanlar
// arası kurallar (validateAll) birleştirilmiş sonuç üzerinde ayrıca denetlenir.
func normalize(in map[Field]any) (map[Field]any, error) {
	out := make(map[Field]any, len(in))
	for _, f := range Fields { // sabit sıra: aynı girdide hep aynı hata
		v, ok := in[f]
		if !ok {
			continue
		}
		switch f {
		case FieldLatestAgentVersion, FieldMinSupportedAgentVersion:
			s := strings.TrimSpace(v.(string))
			if s != "" && !version.ValidSemver(s) {
				return nil, &FieldError{f, fmt.Sprintf("%q geçerli bir sürüm değil (MAJOR.MINOR.PATCH, ör. 1.2.0)", s)}
			}
			out[f] = s
		case FieldPanelBaseURL:
			s, err := config.ParsePanelBaseURL(v.(string))
			if err != nil {
				return nil, &FieldError{f, "geçerli bir adres değil (scheme://alan-adı[:port], ör. https://panel.example.com)"}
			}
			out[f] = s
		case FieldLogLevel:
			s := strings.ToLower(strings.TrimSpace(v.(string)))
			if !slices.Contains(logLevels, s) {
				return nil, &FieldError{f, "debug, info, warn ya da error olmalı"}
			}
			out[f] = s
		default:
			n, r := v.(int), intRanges[f]
			if n < r.min || n > r.max {
				if r.max == noMax {
					return nil, &FieldError{f, fmt.Sprintf("en az %d olmalı", r.min)}
				}
				return nil, &FieldError{f, fmt.Sprintf("%d ile %d arasında olmalı", r.min, r.max)}
			}
			out[f] = n
		}
	}
	return out, nil
}

// validateAll, alanlar arası kuralları birleştirilmiş (değişiklik uygulanmış) ayarlar üzerinde denetler.
func validateAll(v map[Field]any) error {
	if v[FieldRefreshTokenTTLSeconds].(int) <= v[FieldAccessTokenTTLSeconds].(int) {
		return &FieldError{FieldRefreshTokenTTLSeconds, "oturum yenileme süresi erişim süresinden uzun olmalı"}
	}
	latest, minVersion := v[FieldLatestAgentVersion].(string), v[FieldMinSupportedAgentVersion].(string)
	if latest != "" && minVersion != "" && version.Compare(minVersion, latest) > 0 {
		return &FieldError{FieldMinSupportedAgentVersion, fmt.Sprintf("en güncel agent sürümünden (%s) büyük olamaz", latest)}
	}
	return nil
}

// dbValue, bir ayar değerini app_settings'e yazılacak biçime çevirir (tanımsız sürüm NULL).
func dbValue(f Field, v any) any {
	if f == FieldLatestAgentVersion || f == FieldMinSupportedAgentVersion {
		if v.(string) == "" {
			return nil
		}
	}
	return v
}
