package store

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"healthbeat-server/internal/model"
)

// Organizations, organizasyon ağacını yönetir: bir organizasyonun bir üst şirketi olabilir (parent_organization_id),
// böylece ana şirket → alt şirketler → onların alt şirketleri kurulabilir.
type Organizations struct {
	pool *pgxpool.Pool
}

func NewOrganizations(pool *pgxpool.Pool) *Organizations {
	return &Organizations{pool: pool}
}

const orgColumns = `id, parent_organization_id, name, address, created_at`

func scanOrganization(row interface{ Scan(...any) error }) (model.Organization, error) {
	var o model.Organization
	err := row.Scan(&o.ID, &o.ParentOrganizationID, &o.Name, &o.Address, &o.CreatedAt)
	return o, err
}

// mapOrgError, organizasyon yazımlarındaki veritabanı hatalarını uygulama hatalarına çevirir.
func mapOrgError(err error) error {
	switch pgErrorCode(err) {
	case pgUniqueViolation:
		return fmt.Errorf("%w: aynı üst şirketin altında bu ada sahip bir organizasyon zaten var", ErrConflict)
	case pgForeignKeyViolation:
		return fmt.Errorf("%w: üst organizasyon yok", ErrNotFound)
	case pgCheckViolation:
		return fmt.Errorf("%w: organizasyon ağacında döngü oluşturulamaz (bir organizasyon kendi altındaki bir dalın altına taşınamaz)", ErrConflict)
	}
	return err
}

// Create, organizasyon oluşturur. parentID nil = kök (üst şirketi yok); address isteğe bağlıdır.
func (s *Organizations) Create(ctx context.Context, name string, parentID *uuid.UUID, address *string) (model.Organization, error) {
	o, err := scanOrganization(s.pool.QueryRow(ctx,
		`INSERT INTO organizations (name, parent_organization_id, address) VALUES ($1, $2, NULLIF($3::text, '')) RETURNING `+orgColumns,
		name, parentID, address))
	if err != nil {
		return model.Organization{}, mapOrgError(err)
	}
	return o, nil
}

func (s *Organizations) GetByID(ctx context.Context, id uuid.UUID) (model.Organization, error) {
	o, err := scanOrganization(s.pool.QueryRow(ctx, `SELECT `+orgColumns+` FROM organizations WHERE id = $1`, id))
	if err != nil {
		if isNoRows(err) {
			return model.Organization{}, ErrNotFound
		}
		return model.Organization{}, err
	}
	return o, nil
}

func (s *Organizations) List(ctx context.Context) ([]model.Organization, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+orgColumns+` FROM organizations ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanOrganizations(rows)
}

// ListByIDs yalnızca ids içindeki organizasyonları döndürür — bir org_admin'in görünümünü
// erişebildiği organizasyonlarla sınırlamak için kullanılır (bkz. store.UserOrganizations).
func (s *Organizations) ListByIDs(ctx context.Context, ids []uuid.UUID) ([]model.Organization, error) {
	if len(ids) == 0 {
		return []model.Organization{}, nil
	}
	rows, err := s.pool.Query(ctx, `SELECT `+orgColumns+` FROM organizations WHERE id = ANY($1) ORDER BY name`, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanOrganizations(rows)
}

func scanOrganizations(rows interface {
	Next() bool
	Scan(...any) error
	Err() error
}) ([]model.Organization, error) {
	orgs := []model.Organization{}
	for rows.Next() {
		o, err := scanOrganization(rows)
		if err != nil {
			return nil, err
		}
		orgs = append(orgs, o)
	}
	return orgs, rows.Err()
}

// OrgPatch, bir organizasyonun kısmi güncellemesidir. nil alan değişmez; Address "" ise adres temizlenir;
// ParentSet true ise Parent uygulanır (nil Parent = kök yap).
type OrgPatch struct {
	Name      *string
	Address   *string
	ParentSet bool
	Parent    *uuid.UUID
}

func (s *Organizations) Update(ctx context.Context, id uuid.UUID, p OrgPatch) (model.Organization, error) {
	o, err := scanOrganization(s.pool.QueryRow(ctx,
		`UPDATE organizations SET
		     name = COALESCE($2, name),
		     address = CASE WHEN $3::text IS NULL THEN address ELSE NULLIF($3::text, '') END,
		     parent_organization_id = CASE WHEN $4::boolean THEN $5::uuid ELSE parent_organization_id END
		 WHERE id = $1
		 RETURNING `+orgColumns,
		id, p.Name, p.Address, p.ParentSet, p.Parent))
	if err != nil {
		if isNoRows(err) {
			return model.Organization{}, ErrNotFound
		}
		return model.Organization{}, mapOrgError(err)
	}
	return o, nil
}

func (s *Organizations) Delete(ctx context.Context, id uuid.UUID) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM organizations WHERE id = $1`, id)
	if err != nil {
		if pgErrorCode(err) == pgForeignKeyViolation {
			return fmt.Errorf("%w: organizasyonda hâlâ alt organizasyon ya da sunucu var", ErrConflict)
		}
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func scanIDs(rows interface {
	Next() bool
	Scan(...any) error
	Err() error
}) ([]uuid.UUID, error) {
	ids := []uuid.UUID{}
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// WithDescendants, ids'in kendisini ve altındaki tüm dalı (çocuklar, torunlar…) döndürür.
func (s *Organizations) WithDescendants(ctx context.Context, ids []uuid.UUID) ([]uuid.UUID, error) {
	if len(ids) == 0 {
		return []uuid.UUID{}, nil
	}
	rows, err := s.pool.Query(ctx,
		`WITH RECURSIVE t AS (
		     SELECT id FROM organizations WHERE id = ANY($1)
		     UNION
		     SELECT o.id FROM organizations o JOIN t ON o.parent_organization_id = t.id
		 ) SELECT id FROM t`, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanIDs(rows)
}

// Ancestors, ids'in üst zincirini (üst şirket, onun üst şirketi… köke kadar) döndürür; ids'in kendisi dahil değildir.
func (s *Organizations) Ancestors(ctx context.Context, ids []uuid.UUID) ([]uuid.UUID, error) {
	if len(ids) == 0 {
		return []uuid.UUID{}, nil
	}
	rows, err := s.pool.Query(ctx,
		`WITH RECURSIVE t AS (
		     SELECT parent_organization_id AS id FROM organizations WHERE id = ANY($1) AND parent_organization_id IS NOT NULL
		     UNION
		     SELECT o.parent_organization_id FROM organizations o JOIN t ON o.id = t.id WHERE o.parent_organization_id IS NOT NULL
		 ) SELECT id FROM t`, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanIDs(rows)
}

// Chain, orgID'den köke doğru zinciri döndürür (orgID dahil, en yakın önce).
func (s *Organizations) Chain(ctx context.Context, orgID uuid.UUID) ([]uuid.UUID, error) {
	rows, err := s.pool.Query(ctx,
		`WITH RECURSIVE chain AS (
		     SELECT id, parent_organization_id, 0 AS depth FROM organizations WHERE id = $1
		     UNION ALL
		     SELECT o.id, o.parent_organization_id, c.depth + 1 FROM organizations o JOIN chain c ON o.id = c.parent_organization_id
		 ) SELECT id FROM chain ORDER BY depth`, orgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanIDs(rows)
}
