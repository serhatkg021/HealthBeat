package store

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"healthbeat-server/internal/model"
)

// ErrSettingsInvalid, bir ayar değişikliği app_settings'in CHECK kısıtlarından birine takıldığında döner. internal/settings
// değerleri önceden doğruladığı için normalde görülmez; görülürse iki katmanın sınırları ayrışmıştır.
var ErrSettingsInvalid = reason(ErrConflict, "setting value is outside the allowed range")

// SettingsColumns, app_settings'in değiştirilebilir sütunlarıdır (Apply yalnızca bunları kabul eder).
var SettingsColumns = []string{
	"latest_agent_version", "min_supported_agent_version",
	"metrics_retention_days", "audit_retention_days", "resolved_alert_retention_days",
	"access_token_ttl_seconds", "refresh_token_ttl_seconds",
	"rate_limit_auth_failures_per_minute", "rate_limit_ingest_per_minute",
	"panel_base_url",
	"log_level", "log_error_body_bytes", "log_file_max_age_days", "log_file_max_total_mb",
}

var settingsSelect = strings.Join(SettingsColumns, ", ") + ", updated_at, updated_by"

// Settings, çalışma zamanı ayarlarının (app_settings, tek satır) SQL katmanıdır.
type Settings struct {
	pool *pgxpool.Pool
}

func NewSettings(pool *pgxpool.Pool) *Settings { return &Settings{pool: pool} }

func scanSettings(row interface{ Scan(...any) error }) (model.AppSettings, error) {
	var (
		s                  model.AppSettings
		latest, minVersion *string
		accessTTL          int
		refreshTTL         int
	)
	err := row.Scan(&latest, &minVersion,
		&s.MetricsRetentionDays, &s.AuditRetentionDays, &s.ResolvedAlertRetentionDays,
		&accessTTL, &refreshTTL,
		&s.RateLimitAuthFailuresPerMinute, &s.RateLimitIngestPerMinute,
		&s.PanelBaseURL,
		&s.LogLevel, &s.LogErrorBodyBytes, &s.LogFileMaxAgeDays, &s.LogFileMaxTotalMB,
		&s.UpdatedAt, &s.UpdatedBy)
	if latest != nil {
		s.LatestAgentVersion = *latest
	}
	if minVersion != nil {
		s.MinSupportedAgentVersion = *minVersion
	}
	s.AccessTokenTTL = time.Duration(accessTTL) * time.Second
	s.RefreshTokenTTL = time.Duration(refreshTTL) * time.Second
	return s, err
}

// Get, güncel ayarları okur.
func (s *Settings) Get(ctx context.Context) (model.AppSettings, error) {
	return scanSettings(s.pool.QueryRow(ctx, `SELECT `+settingsSelect+` FROM app_settings`))
}

// Defaults, ayarların varsayılanlarını veritabanının kendisinden okur: bir transaction'da bütün sütunlar DEFAULT'a
// çekilir, değerler okunur ve transaction geri alınır. Böylece varsayılanlar hiçbir yerde kopyalanmaz; satır değişmez.
func (s *Settings) Defaults(ctx context.Context) (model.AppSettings, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return model.AppSettings{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }() // bilerek hiç commit edilmez
	set := make([]string, len(SettingsColumns))
	for i, c := range SettingsColumns {
		set[i] = c + " = DEFAULT"
	}
	return scanSettings(tx.QueryRow(ctx, `UPDATE app_settings SET `+strings.Join(set, ", ")+` RETURNING `+settingsSelect))
}

// Apply, set'teki sütunlara verilen değerleri yazar ve reset'teki sütunları varsayılanlarına döndürür; ikisi tek
// transaction'dadır. Satır işlem boyunca kilitlenir ve değişiklikten önceki ve sonraki hâli döner. Değerler veritabanı
// biçimindedir (süreler saniye, tanımsız sürüm nil). Bilinmeyen sütun hatadır.
func (s *Settings) Apply(ctx context.Context, actor *uuid.UUID, set map[string]any, reset []string) (old, updated model.AppSettings, err error) {
	for c := range set {
		if !slices.Contains(SettingsColumns, c) {
			return old, updated, fmt.Errorf("unknown settings column %q", c)
		}
	}
	for _, c := range reset {
		if !slices.Contains(SettingsColumns, c) {
			return old, updated, fmt.Errorf("unknown settings column %q", c)
		}
	}
	var (
		clauses []string
		args    []any
	)
	for _, c := range SettingsColumns { // sabit sıra: aynı değişiklik hep aynı SQL'i üretir
		if v, ok := set[c]; ok {
			args = append(args, v)
			clauses = append(clauses, fmt.Sprintf("%s = $%d", c, len(args)))
		} else if slices.Contains(reset, c) {
			clauses = append(clauses, c+" = DEFAULT")
		}
	}
	if len(clauses) == 0 {
		return old, updated, errors.New("no settings to change")
	}
	args = append(args, actor)
	clauses = append(clauses, "updated_at = now()", fmt.Sprintf("updated_by = $%d", len(args)))

	err = InTx(ctx, s.pool, func(tx pgx.Tx) error {
		var err error
		if old, err = scanSettings(tx.QueryRow(ctx, `SELECT `+settingsSelect+` FROM app_settings FOR UPDATE`)); err != nil {
			return err
		}
		updated, err = scanSettings(tx.QueryRow(ctx,
			`UPDATE app_settings SET `+strings.Join(clauses, ", ")+` RETURNING `+settingsSelect, args...))
		if pgErrorCode(err) == pgCheckViolation {
			return fmt.Errorf("%w (%v)", ErrSettingsInvalid, err)
		}
		return err
	})
	return old, updated, err
}
