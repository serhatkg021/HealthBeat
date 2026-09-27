package store

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"healthbeat-server/internal/model"
)

type Users struct {
	pool *pgxpool.Pool
}

func NewUsers(pool *pgxpool.Pool) *Users {
	return &Users{pool: pool}
}

// userColumns şifre hash'ini içermez: hash yalnızca onu doğrulayan okumalarda (GetByEmailWithHash, GetByIDWithHash) seçilir.
const userColumns = `id, email, full_name, role, phone, two_factor_enabled, two_factor_channel, created_at, last_login_at, must_change_password`

// userDest, userColumns ile eşleşen tarama hedeflerini listeler; böylece bir kolon eklemek
// tek yerde değişikliktir.
func userDest(u *model.User) []any {
	return []any{&u.ID, &u.Email, &u.FullName, &u.Role, &u.Phone, &u.TwoFactorEnabled, &u.TwoFactorChannel, &u.CreatedAt, &u.LastLoginAt, &u.MustChangePassword}
}

func scanUser(row interface{ Scan(...any) error }) (model.User, error) {
	var u model.User
	err := row.Scan(userDest(&u)...)
	return u, err
}

// Create, kullanıcı oluşturur. fullName ve phone isteğe bağlıdır (nil/boş = yok).
func (s *Users) Create(ctx context.Context, email, passwordHash, role string, fullName, phone *string) (model.User, error) {
	email = strings.ToLower(email) // şema küçük harf zorunlu kılar (users_email_lowercase_chk)
	var u model.User
	// Bir yöneticinin oluşturduğu hesabın ilk şifresini o yönetici biliyor; hesap sahibi ilk girişte
	// kendi şifresini seçmek zorundadır.
	err := s.pool.QueryRow(ctx,
		`INSERT INTO users (email, password_hash, role, full_name, phone, must_change_password)
		 VALUES ($1, $2, $3, NULLIF($4::text, ''), NULLIF($5::text, ''), true)
		 RETURNING `+userColumns,
		email, passwordHash, role, fullName, phone,
	).Scan(userDest(&u)...)
	if err != nil {
		if pgErrorCode(err) == pgUniqueViolation {
			return model.User{}, ErrEmailTaken
		}
		return model.User{}, err
	}
	return u, nil
}

func (s *Users) GetByEmail(ctx context.Context, email string) (model.User, error) {
	u, _, err := s.get(ctx, `email = lower($1)`, email, false)
	return u, err
}

func (s *Users) GetByID(ctx context.Context, id uuid.UUID) (model.User, error) {
	u, _, err := s.get(ctx, `id = $1`, id, false)
	return u, err
}

// GetByEmailWithHash, girişte şifreyi doğrulamak için kullanıcıyı şifre hash'iyle birlikte döndürür.
func (s *Users) GetByEmailWithHash(ctx context.Context, email string) (model.User, string, error) {
	return s.get(ctx, `email = lower($1)`, email, true)
}

// GetByIDWithHash, şifre değiştirmede mevcut şifreyi doğrulamak için kullanıcıyı şifre hash'iyle birlikte döndürür.
func (s *Users) GetByIDWithHash(ctx context.Context, id uuid.UUID) (model.User, string, error) {
	return s.get(ctx, `id = $1`, id, true)
}

// get, where koşuluna uyan tek kullanıcıyı okur; withHash ise password_hash'i de seçer.
func (s *Users) get(ctx context.Context, where string, arg any, withHash bool) (model.User, string, error) {
	var (
		u    model.User
		hash string
	)
	cols, dest := userColumns, userDest(&u)
	if withHash {
		cols, dest = cols+", password_hash", append(dest, &hash)
	}
	err := s.pool.QueryRow(ctx, `SELECT `+cols+` FROM users WHERE `+where, arg).Scan(dest...)
	if err != nil {
		if isNoRows(err) {
			return model.User{}, "", ErrNotFound
		}
		return model.User{}, "", err
	}
	return u, hash, nil
}

// List, e-postaya göre bir alt dize araması (ListParams.Search) ve isteğe bağlı sayfalama ile
// kullanıcıları döndürür. total, filtreye uyan toplam satır sayısıdır (Limit>0 iken sayfa
// numaralı bir arayüz için gerekir); Limit==0 ise tüm satırlar döner ve total = len(users).
func (s *Users) List(ctx context.Context, p ListParams) (users []model.User, total int, err error) {
	where := ""
	args := []any{}
	if p.Search != "" {
		args = append(args, p.SearchPattern())
		where = fmt.Sprintf("WHERE email ILIKE $%d OR full_name ILIKE $%d", len(args), len(args))
	}

	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM users `+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}

	query := `SELECT ` + userColumns + ` FROM users ` + where + ` ORDER BY created_at`
	if p.Limit > 0 {
		args = append(args, p.Limit, p.Offset)
		query += fmt.Sprintf(" LIMIT $%d OFFSET $%d", len(args)-1, len(args))
	}
	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, 0, err
	}
	users, err = collect(rows, scanUser)
	return users, total, err
}

// guardLastSuperAdmin, id kalan tek super_admin ise ErrLastSuperAdmin ile başarısız olur.
// Her super_admin satırını (id sırasıyla, böylece eşzamanlı çağıranlar kilitlenmeye
// girmez) transaction'ın geri kalanı için kilitler; iki yöneticinin aynı anda birbirini
// düşürmesi/silmesini güvenli kılan budur — ikincisi bekler, sonra birincinin sonucunu görür.
func guardLastSuperAdmin(ctx context.Context, tx pgx.Tx, id uuid.UUID) error {
	rows, err := tx.Query(ctx, `SELECT id FROM users WHERE role = $1 ORDER BY id FOR UPDATE`, model.RoleSuperAdmin)
	if err != nil {
		return err
	}
	defer rows.Close()

	count, isTarget := 0, false
	for rows.Next() {
		var sid uuid.UUID
		if err := rows.Scan(&sid); err != nil {
			return err
		}
		count++
		isTarget = isTarget || sid == id
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if isTarget && count == 1 {
		return ErrLastSuperAdmin
	}
	return nil
}

// UserProfile, kullanıcının profil alanlarıdır; nil alan değişmez, boş metin alanı temizler.
type UserProfile struct {
	FullName         *string
	Phone            *string
	TwoFactorEnabled *bool
	TwoFactorChannel *string
}

// UpdateProfile profil alanlarını günceller (ad, telefon, iki faktörlü doğrulama tercihi).
func (s *Users) UpdateProfile(ctx context.Context, id uuid.UUID, p UserProfile) (model.User, error) {
	var u model.User
	err := s.pool.QueryRow(ctx,
		`UPDATE users SET
		     full_name = CASE WHEN $2::text IS NULL THEN full_name ELSE NULLIF($2::text, '') END,
		     phone = CASE WHEN $3::text IS NULL THEN phone ELSE NULLIF($3::text, '') END,
		     two_factor_enabled = COALESCE($4::boolean, two_factor_enabled),
		     two_factor_channel = CASE WHEN $5::text IS NULL THEN two_factor_channel ELSE NULLIF($5::text, '') END
		 WHERE id = $1
		 RETURNING `+userColumns,
		id, p.FullName, p.Phone, p.TwoFactorEnabled, p.TwoFactorChannel,
	).Scan(userDest(&u)...)
	if err != nil {
		if isNoRows(err) {
			return model.User{}, ErrNotFound
		}
		if pgErrorCode(err) == pgCheckViolation {
			return model.User{}, ErrTwoFactorNoChannel
		}
		return model.User{}, err
	}
	return u, nil
}

func (s *Users) Update(ctx context.Context, id uuid.UUID, email, role *string) (model.User, error) {
	if email != nil {
		lowered := strings.ToLower(*email)
		email = &lowered
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return model.User{}, err
	}
	defer tx.Rollback(ctx)

	if role != nil && *role != model.RoleSuperAdmin {
		if err := guardLastSuperAdmin(ctx, tx, id); err != nil {
			return model.User{}, err
		}
	}

	var u model.User
	err = tx.QueryRow(ctx,
		`UPDATE users
		 SET email = COALESCE($2, email),
		     role  = COALESCE($3, role)
		 WHERE id = $1
		 RETURNING `+userColumns,
		id, email, role,
	).Scan(userDest(&u)...)
	if err != nil {
		if isNoRows(err) {
			return model.User{}, ErrNotFound
		}
		if pgErrorCode(err) == pgUniqueViolation {
			return model.User{}, ErrEmailTaken
		}
		return model.User{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return model.User{}, err
	}
	return u, nil
}

func (s *Users) UpdatePassword(ctx context.Context, id uuid.UUID, passwordHash string) error {
	tag, err := s.pool.Exec(ctx, `UPDATE users SET password_hash = $2 WHERE id = $1`, id, passwordHash)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Users) TouchLastLogin(ctx context.Context, id uuid.UUID) error {
	_, err := s.pool.Exec(ctx, `UPDATE users SET last_login_at = now() WHERE id = $1`, id)
	return err
}

func (s *Users) Delete(ctx context.Context, id uuid.UUID) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	if err := guardLastSuperAdmin(ctx, tx, id); err != nil {
		return err
	}
	tag, err := tx.Exec(ctx, `DELETE FROM users WHERE id = $1`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return tx.Commit(ctx)
}

// SetOwnPassword, kullanıcının kendisi için seçtiği şifreyi saklar: hash'i değiştirir ve
// must_change_password'ü temizler. (Bir yöneticinin UpdatePassword ile birinin şifresini
// sıfırlaması bayrağa bilerek dokunmaz.)
func (s *Users) SetOwnPassword(ctx context.Context, id uuid.UUID, passwordHash string) error {
	tag, err := s.pool.Exec(ctx,
		`UPDATE users SET password_hash = $2, must_change_password = false WHERE id = $1`, id, passwordHash)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// CountSuperAdmins, kaç super_admin olduğunu söyler.
func (s *Users) CountSuperAdmins(ctx context.Context) (int, error) {
	var n int
	err := s.pool.QueryRow(ctx, `SELECT count(*) FROM users WHERE role = $1`, model.RoleSuperAdmin).Scan(&n)
	return n, err
}

// BootstrapSuperAdmin ilk super_admin'i oluşturur, ama yalnızca hiç yokken (bu yüzden her
// açılışta çalıştırmak güvenlidir ve birkaç server kopyası aynı anda açıldığında da güvenlidir).
// Hesap ilk girişte kendi şifresini seçmek zorundadır. created bir satır eklenip eklenmediğini bildirir.
func (s *Users) BootstrapSuperAdmin(ctx context.Context, email, passwordHash string) (created bool, err error) {
	tag, err := s.pool.Exec(ctx,
		`INSERT INTO users (email, password_hash, role, must_change_password)
		 SELECT $1, $2, $3, true
		 WHERE NOT EXISTS (SELECT 1 FROM users WHERE role = $3)
		 ON CONFLICT DO NOTHING`,
		strings.ToLower(email), passwordHash, model.RoleSuperAdmin)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() == 1, nil
}
