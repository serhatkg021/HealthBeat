package store

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type UserHosts struct {
	pool *pgxpool.Pool
}

func NewUserHosts(pool *pgxpool.Pool) *UserHosts {
	return &UserHosts{pool: pool}
}

func (s *UserHosts) ListHostIDs(ctx context.Context, userID uuid.UUID) ([]uuid.UUID, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT host_id FROM user_hosts WHERE user_id = $1`, userID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

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

func (s *UserHosts) IsAssigned(ctx context.Context, userID, hostID uuid.UUID) (bool, error) {
	var exists bool
	err := s.pool.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM user_hosts WHERE user_id = $1 AND host_id = $2)`,
		userID, hostID,
	).Scan(&exists)
	return exists, err
}

// Set, userID'ye atanmış host'ların tam kümesini hostIDs ile değiştirir
// (docs/MIMARI.md bölüm 4'teki "sunucuları tek tek seç" seçeneği).
func (s *UserHosts) Set(ctx context.Context, userID uuid.UUID, hostIDs []uuid.UUID) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx, `DELETE FROM user_hosts WHERE user_id = $1`, userID); err != nil {
		return err
	}

	for _, hostID := range uniqueIDs(hostIDs) {
		if _, err := tx.Exec(ctx,
			`INSERT INTO user_hosts (user_id, host_id) VALUES ($1, $2)`,
			userID, hostID,
		); err != nil {
			if pgErrorCode(err) == pgForeignKeyViolation {
				return ErrNotFound
			}
			return err
		}
	}

	return tx.Commit(ctx)
}

// AddByOrganization, şu an organizationID'ye ait her host'ı userID'nin atamasına, mevcut
// atamalara dokunmadan ekler ("organizasyondaki tüm sunucuları ekle" toplu seçeneği).
func (s *UserHosts) AddByOrganization(ctx context.Context, userID, organizationID uuid.UUID) (int64, error) {
	tag, err := s.pool.Exec(ctx,
		`INSERT INTO user_hosts (user_id, host_id)
		 SELECT $1, c.id FROM hosts c WHERE c.organization_id = $2
		 ON CONFLICT (user_id, host_id) DO NOTHING`,
		userID, organizationID,
	)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// uniqueIDs, tekrarlanan kimlikleri ilk görülme sırasını koruyarak atar. Aynı kimliği iki kez
// adlandıran bir seçim "atanmış" demektir, "birincil anahtarı ihlal et ve başarısız ol" değil.
func uniqueIDs(ids []uuid.UUID) []uuid.UUID {
	seen := make(map[uuid.UUID]struct{}, len(ids))
	out := make([]uuid.UUID, 0, len(ids))
	for _, id := range ids {
		if _, dup := seen[id]; dup {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out
}
