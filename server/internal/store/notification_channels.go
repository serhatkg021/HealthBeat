package store

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"healthbeat-server/internal/model"
	"healthbeat-server/internal/secretbox"
)

// NotificationChannels, sistem düzeyindeki bildirim kanallarının (notification_channels) SQL katmanıdır. Kanalın şifresi ya
// da token'ı secretbox ile şifreli saklanır ve kanal adına bağlanır: başka bir satıra kopyalanan değer çözülemez.
type NotificationChannels struct {
	pool *pgxpool.Pool
	box  *secretbox.Box
}

func NewNotificationChannels(pool *pgxpool.Pool, box *secretbox.Box) *NotificationChannels {
	return &NotificationChannels{pool: pool, box: box}
}

const channelSelect = `SELECT channel, provider, enabled, config, secret_enc, owner_min_level, verified_at, updated_at, updated_by
FROM notification_channels`

func channelSecretAAD(channel string) string { return "notification_channels:" + channel }

func (s *NotificationChannels) scan(row interface{ Scan(...any) error }) (model.NotificationChannel, error) {
	var (
		c      model.NotificationChannel
		config []byte
		sealed *string
	)
	if err := row.Scan(&c.Channel, &c.Provider, &c.Enabled, &config, &sealed, &c.OwnerMinLevel, &c.VerifiedAt, &c.UpdatedAt, &c.UpdatedBy); err != nil {
		return c, err
	}
	c.Config = json.RawMessage(config)
	if sealed != nil {
		secret, err := s.box.Open(*sealed, channelSecretAAD(c.Channel))
		if err != nil {
			return c, fmt.Errorf("notification channel %s: %w", c.Channel, err)
		}
		c.Secret, c.SecretSet = secret, true
	}
	return c, nil
}

// List, bütün kanalları adına göre sıralı döndürür.
func (s *NotificationChannels) List(ctx context.Context) ([]model.NotificationChannel, error) {
	rows, err := s.pool.Query(ctx, channelSelect+` ORDER BY channel`)
	if err != nil {
		return nil, err
	}
	return collect(rows, s.scan)
}

// Get, bir kanalı okur; yoksa ErrNotFound.
func (s *NotificationChannels) Get(ctx context.Context, channel string) (model.NotificationChannel, error) {
	c, err := s.scan(s.pool.QueryRow(ctx, channelSelect+` WHERE channel = $1`, channel))
	if isNoRows(err) {
		return c, ErrNotFound
	}
	return c, err
}

// Save, kanalın açık/kapalı durumunu, ayarını ve sahip seviyesini yazar. secret nil ise şifreye dokunulmaz, "" ise
// silinir, başka bir değerse şifrelenip yazılır. Ayar ya da şifre değişirse önceki başarılı deneme (verified_at)
// geçersiz sayılır.
func (s *NotificationChannels) Save(ctx context.Context, actor *uuid.UUID, c model.NotificationChannel, secret *string) (model.NotificationChannel, error) {
	var sealed *string
	if secret != nil && *secret != "" {
		v, err := s.box.Seal(*secret, channelSecretAAD(c.Channel))
		if err != nil {
			return model.NotificationChannel{}, err
		}
		sealed = &v
	}
	updated, err := s.scan(s.pool.QueryRow(ctx,
		`UPDATE notification_channels SET
		   enabled = $2, config = $3::jsonb, owner_min_level = $4,
		   secret_enc = CASE WHEN $5 THEN $6 ELSE secret_enc END,
		   verified_at = CASE WHEN $5 OR config IS DISTINCT FROM $3::jsonb THEN NULL ELSE verified_at END,
		   updated_at = now(), updated_by = $7
		 WHERE channel = $1
		 RETURNING channel, provider, enabled, config, secret_enc, owner_min_level, verified_at, updated_at, updated_by`,
		c.Channel, c.Enabled, string(c.Config), c.OwnerMinLevel, secret != nil, sealed, actor))
	switch {
	case isNoRows(err):
		return updated, ErrNotFound
	case pgErrorCode(err) == pgCheckViolation:
		return updated, fmt.Errorf("%w (%v)", ErrSettingsInvalid, err)
	}
	return updated, err
}

// MarkVerified, kanalın o anki ayarıyla deneme gönderiminin başarılı olduğunu kaydeder.
func (s *NotificationChannels) MarkVerified(ctx context.Context, channel string) (model.NotificationChannel, error) {
	c, err := s.scan(s.pool.QueryRow(ctx,
		`UPDATE notification_channels SET verified_at = now() WHERE channel = $1
		 RETURNING channel, provider, enabled, config, secret_enc, owner_min_level, verified_at, updated_at, updated_by`, channel))
	if isNoRows(err) {
		return c, ErrNotFound
	}
	return c, err
}

// RuleCounts, her kanala bağlı bildirim kuralı sayısıdır (kuralı olmayan kanal yer almaz).
func (s *NotificationChannels) RuleCounts(ctx context.Context) (map[string]int, error) {
	rows, err := s.pool.Query(ctx, `SELECT channel, count(*) FROM notification_routes GROUP BY channel`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var ch string
		var n int
		if err := rows.Scan(&ch, &n); err != nil {
			return nil, err
		}
		out[ch] = n
	}
	return out, rows.Err()
}
