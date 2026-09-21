package store

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrTokenReuse, zaten döndürülmüş (ve tolerans penceresi geçmiş) bir refresh token'ın
// yeniden sunulduğu anlamına gelir — çalınmış bir token'ın imzası. Consume döndüğünde tüm
// aileyi çoktan iptal etmiş olur.
var ErrTokenReuse = errors.New("refresh token reuse detected")

type RefreshTokens struct {
	pool *pgxpool.Pool
}

func NewRefreshTokens(pool *pgxpool.Pool) *RefreshTokens {
	return &RefreshTokens{pool: pool}
}

func (s *RefreshTokens) Create(ctx context.Context, jti, userID, familyID uuid.UUID, expiresAt time.Time) error {
	_, err := s.pool.Exec(ctx,
		`INSERT INTO refresh_tokens (jti, user_id, family_id, expires_at) VALUES ($1, $2, $3, $4)`,
		jti, userID, familyID, expiresAt)
	return err
}

// ConsumedRefreshToken, başarıyla kullanılan bir token'ın kime ait olduğunu belirtir.
type ConsumedRefreshToken struct {
	UserID   uuid.UUID
	FamilyID uuid.UUID
}

// Consume, bir refresh token'ı veritabanına karşı doğrular ve değiştirilmiş işaretler.
// Bilinmeyen, iptal edilmiş ya da süresi dolmuş bir token için ErrNotFound, grace'ten daha
// önce zaten değiştirilmiş biri için ErrTokenReuse (ailesini iptal ettikten sonra) döndürür.
// Grace içinde ikinci bir değişim tolere edilir; böylece yenilemek için yarışan iki tarayıcı
// sekmesi kullanıcının oturumunu kapatmaz.
func (s *RefreshTokens) Consume(ctx context.Context, jti uuid.UUID, grace time.Duration) (ConsumedRefreshToken, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return ConsumedRefreshToken{}, err
	}
	defer tx.Rollback(ctx) // commit edildikten sonra etkisiz

	var (
		out                  ConsumedRefreshToken
		expiresAt            time.Time
		rotatedAt, revokedAt *time.Time
	)
	err = tx.QueryRow(ctx,
		`SELECT user_id, family_id, expires_at, rotated_at, revoked_at
		 FROM refresh_tokens WHERE jti = $1 FOR UPDATE`, jti,
	).Scan(&out.UserID, &out.FamilyID, &expiresAt, &rotatedAt, &revokedAt)
	if err != nil {
		if isNoRows(err) {
			return ConsumedRefreshToken{}, ErrNotFound
		}
		return ConsumedRefreshToken{}, err
	}

	now := time.Now()
	if revokedAt != nil || !expiresAt.After(now) {
		return ConsumedRefreshToken{}, ErrNotFound
	}

	if rotatedAt != nil && now.Sub(*rotatedAt) > grace {
		if _, err := tx.Exec(ctx,
			`UPDATE refresh_tokens SET revoked_at = $2 WHERE family_id = $1 AND revoked_at IS NULL`,
			out.FamilyID, now); err != nil {
			return ConsumedRefreshToken{}, err
		}
		if err := tx.Commit(ctx); err != nil {
			return ConsumedRefreshToken{}, err
		}
		return out, ErrTokenReuse
	}

	if rotatedAt == nil {
		if _, err := tx.Exec(ctx, `UPDATE refresh_tokens SET rotated_at = $2 WHERE jti = $1`, jti, now); err != nil {
			return ConsumedRefreshToken{}, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return ConsumedRefreshToken{}, err
	}
	return out, nil
}

// RevokeFamilyOf, jti ile aynı girişten türeyen her token'ı iptal eder (çıkış). Bilinmeyen
// jti hata değildir — çıkış idempotenttir.
func (s *RefreshTokens) RevokeFamilyOf(ctx context.Context, jti uuid.UUID) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE refresh_tokens SET revoked_at = now()
		 WHERE revoked_at IS NULL
		   AND family_id = (SELECT family_id FROM refresh_tokens WHERE jti = $1)`, jti)
	return err
}

// RevokeAllForUser, bir kullanıcının tüm oturumlarını sonlandırır (şifre değişikliği).
func (s *RefreshTokens) RevokeAllForUser(ctx context.Context, userID uuid.UUID) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE refresh_tokens SET revoked_at = now() WHERE user_id = $1 AND revoked_at IS NULL`, userID)
	return err
}

// PurgeExpired artık önemi kalmayan satırları siler. Süre bitiminden bir gün sonrasına kadar
// tutulurlar; böylece süresi dolmuş bir token'ın geç tekrarı yine satırını bulur.
func (s *RefreshTokens) PurgeExpired(ctx context.Context) (int64, error) {
	tag, err := s.pool.Exec(ctx, `DELETE FROM refresh_tokens WHERE expires_at < now() - interval '1 day'`)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}
