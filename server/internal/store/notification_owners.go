package store

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"healthbeat-server/internal/model"
)

// ErrOwnerEmailTaken, aynı e-postayla ikinci bir sistem sahibi eklenmek istendiğinde döner.
var ErrOwnerEmailTaken = reason(ErrConflict, "a system owner with this e-mail already exists")

// NotificationOwners, her alert'in bildirimini alan sistem sahiplerini (notification_owners) yönetir. Alanlar yazılmadan
// önce model.NotificationOwner.Normalize ile doğrulanmış olmalıdır.
type NotificationOwners struct {
	pool *pgxpool.Pool
}

func NewNotificationOwners(pool *pgxpool.Pool) *NotificationOwners { return &NotificationOwners{pool: pool} }

const ownerColumns = `id, name, email, phone, email_enabled, sms_enabled, created_at, updated_at`

func scanOwner(row interface{ Scan(...any) error }) (model.NotificationOwner, error) {
	var o model.NotificationOwner
	err := row.Scan(&o.ID, &o.Name, &o.Email, &o.Phone, &o.EmailEnabled, &o.SMSEnabled, &o.CreatedAt, &o.UpdatedAt)
	return o, err
}

// List, sahipleri ada göre sıralı döndürür.
func (s *NotificationOwners) List(ctx context.Context) ([]model.NotificationOwner, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+ownerColumns+` FROM notification_owners ORDER BY lower(name), created_at`)
	if err != nil {
		return nil, err
	}
	return collect(rows, scanOwner)
}

func (s *NotificationOwners) Get(ctx context.Context, id uuid.UUID) (model.NotificationOwner, error) {
	o, err := scanOwner(s.pool.QueryRow(ctx, `SELECT `+ownerColumns+` FROM notification_owners WHERE id = $1`, id))
	if isNoRows(err) {
		return o, ErrNotFound
	}
	return o, err
}

func (s *NotificationOwners) Create(ctx context.Context, o model.NotificationOwner) (model.NotificationOwner, error) {
	created, err := scanOwner(s.pool.QueryRow(ctx,
		`INSERT INTO notification_owners (name, email, phone, email_enabled, sms_enabled)
		 VALUES ($1, $2, $3, $4, $5) RETURNING `+ownerColumns,
		o.Name, o.Email, o.Phone, o.EmailEnabled, o.SMSEnabled))
	return created, ownerError(err)
}

// Update, sahibin bütün alanlarını yazar.
func (s *NotificationOwners) Update(ctx context.Context, o model.NotificationOwner) (model.NotificationOwner, error) {
	updated, err := scanOwner(s.pool.QueryRow(ctx,
		`UPDATE notification_owners SET name = $2, email = $3, phone = $4, email_enabled = $5, sms_enabled = $6, updated_at = now()
		 WHERE id = $1 RETURNING `+ownerColumns,
		o.ID, o.Name, o.Email, o.Phone, o.EmailEnabled, o.SMSEnabled))
	if isNoRows(err) {
		return updated, ErrNotFound
	}
	return updated, ownerError(err)
}

func (s *NotificationOwners) Delete(ctx context.Context, id uuid.UUID) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM notification_owners WHERE id = $1`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func ownerError(err error) error {
	switch pgErrorCode(err) {
	case pgUniqueViolation:
		return ErrOwnerEmailTaken
	case pgCheckViolation:
		return ErrSettingsInvalid
	}
	return err
}
