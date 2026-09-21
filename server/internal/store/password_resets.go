package store

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"healthbeat-server/internal/model"
)

// PasswordResets, e-posta ile şifre sıfırlama bağlantılarının kaydıdır. Yalnızca token'ın SHA-256 özeti
// saklanır; ham token yalnızca kullanıcıya giden e-postada bulunur.
type PasswordResets struct {
	pool *pgxpool.Pool
}

func NewPasswordResets(pool *pgxpool.Pool) *PasswordResets {
	return &PasswordResets{pool: pool}
}

// Issue, kullanıcı için yeni bir sıfırlama kaydı açar ve kullanıcının önceki, kullanılmamış kayıtlarını
// geçersiz kılar: aynı anda yalnızca en son gönderilen bağlantı çalışır (eski e-postalar sızsa da işe yaramaz).
func (s *PasswordResets) Issue(ctx context.Context, userID uuid.UUID, tokenHash string, expiresAt time.Time) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx, `DELETE FROM password_reset_tokens WHERE user_id = $1 AND used_at IS NULL`, userID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO password_reset_tokens (user_id, token_hash, expires_at) VALUES ($1, $2, $3)`,
		userID, tokenHash, expiresAt); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// Complete, geçerli (kullanılmamış, süresi dolmamış) bir sıfırlama token'ını TEK bir işlemde tüketir, kullanıcının
// şifresini değiştirir ve tüm oturumlarını (refresh token'larını) iptal eder. Kullanıcı şifreyi kendisi seçtiği için
// must_change_password da temizlenir. Token bilinmiyor, kullanılmış ya da süresi dolmuşsa ErrNotFound döner ve
// hiçbir şey değişmez. İşlem atomiktir: şifre yazılamazsa token yanmaz.
func (s *PasswordResets) Complete(ctx context.Context, tokenHash, passwordHash string) (model.User, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return model.User{}, err
	}
	defer tx.Rollback(ctx)

	var userID uuid.UUID
	err = tx.QueryRow(ctx,
		`UPDATE password_reset_tokens SET used_at = now()
		 WHERE token_hash = $1 AND used_at IS NULL AND expires_at > now()
		 RETURNING user_id`, tokenHash).Scan(&userID)
	if isNoRows(err) {
		return model.User{}, ErrNotFound
	}
	if err != nil {
		return model.User{}, err
	}

	var u model.User
	if err := tx.QueryRow(ctx,
		`UPDATE users SET password_hash = $2, must_change_password = false WHERE id = $1 RETURNING `+userColumns,
		userID, passwordHash).Scan(userDest(&u)...); err != nil {
		return model.User{}, err
	}
	if _, err := tx.Exec(ctx,
		`UPDATE refresh_tokens SET revoked_at = now() WHERE user_id = $1 AND revoked_at IS NULL`, userID); err != nil {
		return model.User{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return model.User{}, err
	}
	return u, nil
}

// PurgeExpired, süresi çoktan dolmuş ya da kullanılmış kayıtları siler (kullanıcı başına bir kayıt kadar küçük bir
// tablo; yalnızca düzen için). Silinen kayıt sayısını döndürür.
func (s *PasswordResets) PurgeExpired(ctx context.Context) (int64, error) {
	tag, err := s.pool.Exec(ctx,
		`DELETE FROM password_reset_tokens WHERE expires_at < now() - interval '1 day' OR used_at < now() - interval '1 day'`)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}
