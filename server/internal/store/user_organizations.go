package store

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// UserOrganizations, org_admin'lerin organizasyon atamalarını yönetir. Bir organizasyona atanan yönetici o
// organizasyonun VE altındaki tüm dalın yönetimini alır (atama aşağıya doğru miras kalır). Üst zincirini yalnızca
// bilgi olarak görür: üst şirketin diğer dalları ve içerikleri ona kapalıdır.
type UserOrganizations struct {
	pool *pgxpool.Pool
}

func NewUserOrganizations(pool *pgxpool.Pool) *UserOrganizations {
	return &UserOrganizations{pool: pool}
}

// ListAssignedIDs, kullanıcıya doğrudan atanmış organizasyonları döndürür (miras hariç).
func (s *UserOrganizations) ListAssignedIDs(ctx context.Context, userID uuid.UUID) ([]uuid.UUID, error) {
	rows, err := s.pool.Query(ctx, `SELECT organization_id FROM user_organizations WHERE user_id = $1`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanIDs(rows)
}

// ListOrganizationIDs, kullanıcının TAM erişimi olan tüm organizasyonları döndürür: atandıkları ve altlarındaki
// dalların tamamı. Erişim kapsamlayan her sorgu (sunucu, alert, eşik, dashboard listeleri) bunu kullanır.
func (s *UserOrganizations) ListOrganizationIDs(ctx context.Context, userID uuid.UUID) ([]uuid.UUID, error) {
	rows, err := s.pool.Query(ctx,
		`WITH RECURSIVE t AS (
		     SELECT organization_id AS id FROM user_organizations WHERE user_id = $1
		     UNION
		     SELECT o.id FROM organizations o JOIN t ON o.parent_organization_id = t.id
		 ) SELECT id FROM t`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanIDs(rows)
}

// ListContextIDs, kullanıcının yalnızca bilgi olarak gördüğü organizasyonları döndürür: tam erişimli organizasyonların
// üst zincirinden, kendisinin tam erişimi olmayanlar (ör. üst şirketin adı; kardeş dallar dahil değildir).
func (s *UserOrganizations) ListContextIDs(ctx context.Context, userID uuid.UUID) ([]uuid.UUID, error) {
	rows, err := s.pool.Query(ctx,
		`WITH RECURSIVE full_access AS (
		     SELECT organization_id AS id FROM user_organizations WHERE user_id = $1
		     UNION
		     SELECT o.id FROM organizations o JOIN full_access f ON o.parent_organization_id = f.id
		 ),
		 up AS (
		     SELECT o.parent_organization_id AS id FROM organizations o JOIN user_organizations uo ON uo.organization_id = o.id
		     WHERE uo.user_id = $1 AND o.parent_organization_id IS NOT NULL
		     UNION
		     SELECT o.parent_organization_id FROM organizations o JOIN up ON o.id = up.id WHERE o.parent_organization_id IS NOT NULL
		 )
		 SELECT id FROM up WHERE id NOT IN (SELECT id FROM full_access)`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanIDs(rows)
}

// IsAssigned, kullanıcının orgID üzerinde TAM erişimi olup olmadığını söyler: doğrudan atanmış ya da atandığı bir
// organizasyonun altında (miras).
func (s *UserOrganizations) IsAssigned(ctx context.Context, userID, orgID uuid.UUID) (bool, error) {
	var exists bool
	err := s.pool.QueryRow(ctx,
		`WITH RECURSIVE chain AS (
		     SELECT id, parent_organization_id FROM organizations WHERE id = $2
		     UNION ALL
		     SELECT o.id, o.parent_organization_id FROM organizations o JOIN chain c ON o.id = c.parent_organization_id
		 )
		 SELECT EXISTS(SELECT 1 FROM user_organizations uo JOIN chain c ON c.id = uo.organization_id WHERE uo.user_id = $1)`,
		userID, orgID,
	).Scan(&exists)
	return exists, err
}

// Set, userID'ye atanmış organizasyonların tam kümesini orgIDs ile değiştirir.
func (s *UserOrganizations) Set(ctx context.Context, userID uuid.UUID, orgIDs []uuid.UUID) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx, `DELETE FROM user_organizations WHERE user_id = $1`, userID); err != nil {
		return err
	}

	for _, orgID := range uniqueIDs(orgIDs) {
		if _, err := tx.Exec(ctx,
			`INSERT INTO user_organizations (user_id, organization_id) VALUES ($1, $2)`,
			userID, orgID,
		); err != nil {
			if pgErrorCode(err) == pgForeignKeyViolation {
				return ErrNotFound
			}
			return err
		}
	}

	return tx.Commit(ctx)
}
