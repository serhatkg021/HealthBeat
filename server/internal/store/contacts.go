package store

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"healthbeat-server/internal/model"
)

// Contacts, organizasyonların iletişim kişilerini yönetir: sunucu işleri için kiminle görüşüleceği (paneli olmayan
// müşteri yetkilileri dahil).
type Contacts struct {
	pool *pgxpool.Pool
}

func NewContacts(pool *pgxpool.Pool) *Contacts { return &Contacts{pool: pool} }

const contactColumns = `id, organization_id, department, title, name, manager_contact_id, phone, email, created_at, updated_at`

func scanContact(row interface{ Scan(...any) error }) (model.OrganizationContact, error) {
	var c model.OrganizationContact
	err := row.Scan(&c.ID, &c.OrganizationID, &c.Department, &c.Title, &c.Name, &c.ManagerContactID, &c.Phone, &c.Email, &c.CreatedAt, &c.UpdatedAt)
	return c, err
}

// ContactInput, bir iletişim kişisinin düzenlenebilir alanlarıdır. Boş metin "yok" demektir.
type ContactInput struct {
	Department       *string
	Title            *string
	Name             string
	ManagerContactID *uuid.UUID
	Phone            *string
	Email            *string
}

func mapContactError(err error) error {
	switch pgErrorCode(err) {
	case pgForeignKeyViolation:
		return fmt.Errorf("%w: yönetici aynı organizasyondan bir iletişim kişisi olmalı", ErrConflict)
	case pgCheckViolation:
		return fmt.Errorf("%w: en az bir iletişim yolu (telefon ya da e-posta) gerekli ve kişi kendi yöneticisi olamaz", ErrConflict)
	}
	return err
}

func (s *Contacts) ListByOrganization(ctx context.Context, orgID uuid.UUID) ([]model.OrganizationContact, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+contactColumns+` FROM organization_contacts WHERE organization_id = $1 ORDER BY name`, orgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.OrganizationContact{}
	for rows.Next() {
		c, err := scanContact(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *Contacts) GetByID(ctx context.Context, id uuid.UUID) (model.OrganizationContact, error) {
	c, err := scanContact(s.pool.QueryRow(ctx, `SELECT `+contactColumns+` FROM organization_contacts WHERE id = $1`, id))
	if err != nil {
		if isNoRows(err) {
			return model.OrganizationContact{}, ErrNotFound
		}
		return model.OrganizationContact{}, err
	}
	return c, nil
}

func (s *Contacts) Create(ctx context.Context, orgID uuid.UUID, in ContactInput) (model.OrganizationContact, error) {
	c, err := scanContact(s.pool.QueryRow(ctx,
		`INSERT INTO organization_contacts (organization_id, department, title, name, manager_contact_id, phone, email)
		 VALUES ($1, NULLIF($2::text, ''), NULLIF($3::text, ''), $4, $5, NULLIF($6::text, ''), NULLIF($7::text, ''))
		 RETURNING `+contactColumns,
		orgID, in.Department, in.Title, in.Name, in.ManagerContactID, in.Phone, in.Email))
	if err != nil {
		if pgErrorCode(err) == pgForeignKeyViolation && in.ManagerContactID == nil {
			return model.OrganizationContact{}, fmt.Errorf("%w: organizasyon yok", ErrNotFound)
		}
		return model.OrganizationContact{}, mapContactError(err)
	}
	return c, nil
}

// Update, kişinin tüm alanlarını değiştirir (organizasyonu hariç).
func (s *Contacts) Update(ctx context.Context, id uuid.UUID, in ContactInput) (model.OrganizationContact, error) {
	c, err := scanContact(s.pool.QueryRow(ctx,
		`UPDATE organization_contacts SET
		     department = NULLIF($2::text, ''), title = NULLIF($3::text, ''), name = $4, manager_contact_id = $5,
		     phone = NULLIF($6::text, ''), email = NULLIF($7::text, ''), updated_at = now()
		 WHERE id = $1
		 RETURNING `+contactColumns,
		id, in.Department, in.Title, in.Name, in.ManagerContactID, in.Phone, in.Email))
	if err != nil {
		if isNoRows(err) {
			return model.OrganizationContact{}, ErrNotFound
		}
		return model.OrganizationContact{}, mapContactError(err)
	}
	return c, nil
}

func (s *Contacts) Delete(ctx context.Context, id uuid.UUID) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM organization_contacts WHERE id = $1`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
