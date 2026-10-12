package store

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"healthbeat-server/internal/model"
)

// Contacts, kişileri yönetir: organizasyonların iletişim kişileri (sunucu işleri için kiminle görüşüleceği, paneli olmayan
// müşteri yetkilileri dahil) ve organizasyonsuz sistem kişileri (bütün kurulumun bildirimlerini alabilen kişi ya da ortak
// adres). İkisi aynı tablodadır; tür organization_id'den çıkar (bkz. model.Contact.Scope).
type Contacts struct {
	pool *pgxpool.Pool
}

func NewContacts(pool *pgxpool.Pool) *Contacts { return &Contacts{pool: pool} }

const contactColumns = `id, organization_id, department, title, name, manager_contact_id, phone, email, created_at, updated_at`

func scanContact(row interface{ Scan(...any) error }) (model.Contact, error) {
	var c model.Contact
	err := row.Scan(&c.ID, &c.OrganizationID, &c.Department, &c.Title, &c.Name, &c.ManagerContactID, &c.Phone, &c.Email, &c.CreatedAt, &c.UpdatedAt)
	return c, err
}

// ContactInput, bir kişinin düzenlenebilir alanlarıdır. Boş metin "yok" demektir. Sistem kişisinin yöneticisi olmaz.
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
		return ErrContactManagerInvalid
	case pgCheckViolation:
		return ErrContactInvalid
	case pgUniqueViolation:
		return ErrContactEmailTaken
	}
	return err
}

func (s *Contacts) ListByOrganization(ctx context.Context, orgID uuid.UUID) ([]model.Contact, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+contactColumns+` FROM contacts WHERE organization_id = $1 ORDER BY name`, orgID)
	if err != nil {
		return nil, err
	}
	return collect(rows, scanContact)
}

// ListSystem, sistem kişilerini (organizasyonsuz) ada göre sıralı döndürür.
func (s *Contacts) ListSystem(ctx context.Context) ([]model.Contact, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+contactColumns+` FROM contacts WHERE organization_id IS NULL ORDER BY lower(name), created_at`)
	if err != nil {
		return nil, err
	}
	return collect(rows, scanContact)
}

func (s *Contacts) GetByID(ctx context.Context, id uuid.UUID) (model.Contact, error) {
	c, err := scanContact(s.pool.QueryRow(ctx, `SELECT `+contactColumns+` FROM contacts WHERE id = $1`, id))
	if err != nil {
		if isNoRows(err) {
			return model.Contact{}, ErrNotFound
		}
		return model.Contact{}, err
	}
	return c, nil
}

// Create, bir kişi ekler; orgID nil ise sistem kişisidir.
func (s *Contacts) Create(ctx context.Context, orgID *uuid.UUID, in ContactInput) (model.Contact, error) {
	c, err := scanContact(s.pool.QueryRow(ctx,
		`INSERT INTO contacts (organization_id, department, title, name, manager_contact_id, phone, email)
		 VALUES ($1, NULLIF($2::text, ''), NULLIF($3::text, ''), $4, $5, NULLIF($6::text, ''), NULLIF($7::text, ''))
		 RETURNING `+contactColumns,
		orgID, in.Department, in.Title, in.Name, in.ManagerContactID, in.Phone, in.Email))
	if err != nil {
		if pgErrorCode(err) == pgForeignKeyViolation && in.ManagerContactID == nil {
			return model.Contact{}, ErrOrganizationMissing
		}
		return model.Contact{}, mapContactError(err)
	}
	return c, nil
}

// Update, kişinin tüm alanlarını değiştirir (organizasyonu, dolayısıyla türü hariç).
func (s *Contacts) Update(ctx context.Context, id uuid.UUID, in ContactInput) (model.Contact, error) {
	c, err := scanContact(s.pool.QueryRow(ctx,
		`UPDATE contacts SET
		     department = NULLIF($2::text, ''), title = NULLIF($3::text, ''), name = $4, manager_contact_id = $5,
		     phone = NULLIF($6::text, ''), email = NULLIF($7::text, ''), updated_at = now()
		 WHERE id = $1
		 RETURNING `+contactColumns,
		id, in.Department, in.Title, in.Name, in.ManagerContactID, in.Phone, in.Email))
	if err != nil {
		if isNoRows(err) {
			return model.Contact{}, ErrNotFound
		}
		return model.Contact{}, mapContactError(err)
	}
	return c, nil
}

// Delete, kişiyi siler; alıcı olduğu bildirim kuralları da silinir (yabancı anahtar).
func (s *Contacts) Delete(ctx context.Context, id uuid.UUID) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM contacts WHERE id = $1`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// SystemRuleCounts, her sistem kişisinin alıcı olduğu bildirim kuralı sayısıdır (kuralı olmayan kişi haritada yoktur).
// Kişi silinince bu kurallar da silinir; panel silmeden önce bunu söyler.
func (s *Contacts) SystemRuleCounts(ctx context.Context) (map[uuid.UUID]int, error) {
	rows, err := s.pool.Query(ctx, `SELECT r.contact_id, count(*) FROM notification_routes r
		JOIN contacts k ON k.id = r.contact_id WHERE k.organization_id IS NULL GROUP BY r.contact_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[uuid.UUID]int{}
	for rows.Next() {
		var id uuid.UUID
		var n int
		if err := rows.Scan(&id, &n); err != nil {
			return nil, err
		}
		out[id] = n
	}
	return out, rows.Err()
}
