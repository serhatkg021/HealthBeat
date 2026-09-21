package store

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"healthbeat-server/internal/model"
)

// Notifications, bildirim kurallarını yönetir ve bir alert'in alıcılarını çözer.
type Notifications struct {
	pool *pgxpool.Pool
}

func NewNotifications(pool *pgxpool.Pool) *Notifications { return &Notifications{pool: pool} }

// routeSelect, kuralı alıcının görünen adı ve kanalın adresiyle birlikte okur.
const routeSelect = `
SELECT r.id, r.organization_id, r.host_id, r.user_id, r.contact_id, r.channel, r.min_level, r.created_at,
       COALESCE(u.full_name, u.email, oc.name, ''),
       COALESCE(CASE r.channel WHEN 'sms' THEN COALESCE(u.phone, oc.phone) ELSE COALESCE(u.email, oc.email) END, '')
FROM notification_routes r
LEFT JOIN users u ON u.id = r.user_id
LEFT JOIN organization_contacts oc ON oc.id = r.contact_id`

func scanRoute(row interface{ Scan(...any) error }) (model.NotificationRoute, error) {
	var r model.NotificationRoute
	err := row.Scan(&r.ID, &r.OrganizationID, &r.HostID, &r.UserID, &r.ContactID, &r.Channel, &r.MinLevel, &r.CreatedAt, &r.RecipientName, &r.RecipientTarget)
	return r, err
}

func scanRoutes(rows interface {
	Next() bool
	Scan(...any) error
	Err() error
}) ([]model.NotificationRoute, error) {
	out := []model.NotificationRoute{}
	for rows.Next() {
		r, err := scanRoute(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Notifications) ListByOrganization(ctx context.Context, orgID uuid.UUID) ([]model.NotificationRoute, error) {
	rows, err := s.pool.Query(ctx, routeSelect+` WHERE r.organization_id = $1 ORDER BY r.created_at`, orgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanRoutes(rows)
}

func (s *Notifications) ListByHost(ctx context.Context, hostID uuid.UUID) ([]model.NotificationRoute, error) {
	rows, err := s.pool.Query(ctx, routeSelect+` WHERE r.host_id = $1 ORDER BY r.created_at`, hostID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanRoutes(rows)
}

func (s *Notifications) GetByID(ctx context.Context, id uuid.UUID) (model.NotificationRoute, error) {
	r, err := scanRoute(s.pool.QueryRow(ctx, routeSelect+` WHERE r.id = $1`, id))
	if err != nil {
		if isNoRows(err) {
			return model.NotificationRoute{}, ErrNotFound
		}
		return model.NotificationRoute{}, err
	}
	return r, nil
}

// Create, bir kural ekler. Kapsam (organizasyon ya da sunucu) ve alıcı (kullanıcı ya da iletişim kişisi) tam olarak
// birer tane dolu olmalıdır.
func (s *Notifications) Create(ctx context.Context, in model.NotificationRoute) (model.NotificationRoute, error) {
	var id uuid.UUID
	err := s.pool.QueryRow(ctx,
		`INSERT INTO notification_routes (organization_id, host_id, user_id, contact_id, channel, min_level)
		 VALUES ($1, $2, $3, $4, $5, $6) RETURNING id`,
		in.OrganizationID, in.HostID, in.UserID, in.ContactID, in.Channel, in.MinLevel).Scan(&id)
	if err != nil {
		switch pgErrorCode(err) {
		case pgUniqueViolation:
			return model.NotificationRoute{}, fmt.Errorf("%w: bu alıcı ve kanal için bu kapsamda zaten bir kural var", ErrConflict)
		case pgForeignKeyViolation:
			return model.NotificationRoute{}, fmt.Errorf("%w: organizasyon, sunucu, kullanıcı ya da iletişim kişisi yok", ErrNotFound)
		case pgCheckViolation:
			return model.NotificationRoute{}, fmt.Errorf("%w: kuralın tam olarak bir kapsamı ve bir alıcısı olmalı", ErrConflict)
		}
		return model.NotificationRoute{}, err
	}
	return s.GetByID(ctx, id)
}

// Update, kuralın kanalını ve en düşük seviyesini değiştirir.
func (s *Notifications) Update(ctx context.Context, id uuid.UUID, channel, minLevel string) (model.NotificationRoute, error) {
	tag, err := s.pool.Exec(ctx, `UPDATE notification_routes SET channel = $2, min_level = $3 WHERE id = $1`, id, channel, minLevel)
	if err != nil {
		if pgErrorCode(err) == pgUniqueViolation {
			return model.NotificationRoute{}, fmt.Errorf("%w: bu alıcı ve kanal için bu kapsamda zaten bir kural var", ErrConflict)
		}
		return model.NotificationRoute{}, err
	}
	if tag.RowsAffected() == 0 {
		return model.NotificationRoute{}, ErrNotFound
	}
	return s.GetByID(ctx, id)
}

func (s *Notifications) Delete(ctx context.Context, id uuid.UUID) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM notification_routes WHERE id = $1`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// Recipient, bir alert bildiriminin tek bir alıcısıdır.
type Recipient struct {
	Channel string
	Address string // e-posta adresi ya da telefon numarası
	Name    string
}

// ResolveRecipients, bir alert'in alıcılarını belirler. En özel kapsamdaki kurallar geçerlidir: önce sunucunun kendi
// kuralları; yoksa sunucunun organizasyonunun, o da yoksa üst organizasyonların (en yakın önce) kuralları. Bir kapsamda
// kural varsa YALNIZCA o kurallar uygulanır (üst kapsamdaki kurallar ve varsayılan alıcılar yok sayılır): "buranın
// bildirimi yalnızca şu kişiye gitsin". Kuralın en düşük seviyesi alert seviyesinin altındaysa o kural alıcı üretmez.
// Hiçbir kapsamda kural yoksa varsayılan alıcılar kullanılır: her super_admin ile organizasyona (ya da üst
// organizasyonlarından birine) atanmış her org_admin; e-posta, warning ve üzeri seviyeler.
func (s *Notifications) ResolveRecipients(ctx context.Context, hostID, orgID uuid.UUID, level string) ([]Recipient, error) {
	routes, err := s.effectiveRoutes(ctx, hostID, orgID)
	if err != nil {
		return nil, err
	}
	if routes == nil {
		return s.defaultRecipients(ctx, orgID, level)
	}
	rank := model.LevelRank(level)
	seen := map[string]bool{}
	out := []Recipient{}
	for _, r := range routes {
		if model.LevelRank(r.MinLevel) > rank || r.RecipientTarget == "" {
			continue
		}
		key := r.Channel + "\x00" + r.RecipientTarget
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, Recipient{Channel: r.Channel, Address: r.RecipientTarget, Name: r.RecipientName})
	}
	return out, nil
}

// effectiveRoutes, alert için geçerli en özel kapsamın kurallarını döndürür; hiç kural yoksa nil.
func (s *Notifications) effectiveRoutes(ctx context.Context, hostID, orgID uuid.UUID) ([]model.NotificationRoute, error) {
	rows, err := s.pool.Query(ctx, routeSelect+` WHERE r.host_id = $1 ORDER BY r.created_at`, hostID)
	if err != nil {
		return nil, err
	}
	hostRoutes, err := scanRoutes(rows)
	rows.Close()
	if err != nil {
		return nil, err
	}
	if len(hostRoutes) > 0 {
		return hostRoutes, nil
	}

	rows, err = s.pool.Query(ctx,
		orgChainCTE(1)+` `+routeSelect+` JOIN chain c ON c.id = r.organization_id ORDER BY c.depth, r.created_at`, orgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.NotificationRoute
	var firstScope *uuid.UUID
	for rows.Next() {
		r, err := scanRoute(rows)
		if err != nil {
			return nil, err
		}
		if firstScope == nil {
			firstScope = r.OrganizationID
		}
		if r.OrganizationID != nil && *r.OrganizationID != *firstScope {
			break // daha üst bir organizasyonun kuralları: en yakın kapsam kazandı
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// defaultRecipients, kural olmayan sunucuların alıcılarıdır.
func (s *Notifications) defaultRecipients(ctx context.Context, orgID uuid.UUID, level string) ([]Recipient, error) {
	if model.LevelRank(level) < model.LevelRank(model.AlertLevelWarning) {
		return []Recipient{}, nil // info alert'leri varsayılan olarak bildirim üretmez
	}
	rows, err := s.pool.Query(ctx,
		orgChainCTE(1)+`
		 SELECT u.email, COALESCE(u.full_name, u.email) FROM users u WHERE u.role = 'super_admin'
		 UNION
		 SELECT u.email, COALESCE(u.full_name, u.email) FROM users u
		 JOIN user_organizations uo ON uo.user_id = u.id
		 JOIN chain c ON c.id = uo.organization_id
		 WHERE u.role = 'org_admin'`, orgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Recipient{}
	for rows.Next() {
		r := Recipient{Channel: model.ChannelEmail}
		if err := rows.Scan(&r.Address, &r.Name); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// Candidate, bir kapsam için alıcı olabilecek bir panel kullanıcısı ya da iletişim kişisidir.
type Candidate struct {
	UserID    *uuid.UUID `json:"user_id,omitempty"`
	ContactID *uuid.UUID `json:"contact_id,omitempty"`
	Name      string     `json:"name"`
	Email     string     `json:"email,omitempty"`
	Phone     string     `json:"phone,omitempty"`
	// Source alıcının nereden geldiğini söyler: "super_admin", "org_admin", "operator" ya da "contact".
	Source string `json:"source"`
}

// Candidates, orgID kapsamındaki (hostID verilmişse o sunucunun) bildirim alıcı adaylarını döndürür: her super_admin,
// organizasyon zincirine atanmış org_admin'ler, sunucuya atanmış operatörler (hostID verilmişse) ve zincirdeki
// organizasyonların iletişim kişileri. Kural yalnızca bu adaylara yazılabilir: bir yönetici, erişimi olmadığı bir
// kullanıcıya alert yönlendiremez.
func (s *Notifications) Candidates(ctx context.Context, orgID uuid.UUID, hostID *uuid.UUID) ([]Candidate, error) {
	out := []Candidate{}
	rows, err := s.pool.Query(ctx,
		orgChainCTE(1)+`
		 SELECT u.id, COALESCE(u.full_name, u.email), u.email, COALESCE(u.phone, ''), u.role FROM users u WHERE u.role = 'super_admin'
		 UNION
		 SELECT u.id, COALESCE(u.full_name, u.email), u.email, COALESCE(u.phone, ''), u.role FROM users u
		 JOIN user_organizations uo ON uo.user_id = u.id JOIN chain c ON c.id = uo.organization_id WHERE u.role = 'org_admin'
		 UNION
		 SELECT u.id, COALESCE(u.full_name, u.email), u.email, COALESCE(u.phone, ''), u.role FROM users u
		 JOIN user_hosts uh ON uh.user_id = u.id WHERE $2::uuid IS NOT NULL AND uh.host_id = $2 AND u.role = 'operator'
		 ORDER BY 2`, orgID, hostID)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var c Candidate
		var id uuid.UUID
		if err := rows.Scan(&id, &c.Name, &c.Email, &c.Phone, &c.Source); err != nil {
			rows.Close()
			return nil, err
		}
		c.UserID = &id
		out = append(out, c)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	crows, err := s.pool.Query(ctx,
		orgChainCTE(1)+`
		 SELECT oc.id, oc.name, COALESCE(oc.email, ''), COALESCE(oc.phone, '')
		 FROM organization_contacts oc JOIN chain c ON c.id = oc.organization_id ORDER BY oc.name`, orgID)
	if err != nil {
		return nil, err
	}
	defer crows.Close()
	for crows.Next() {
		var c Candidate
		var id uuid.UUID
		if err := crows.Scan(&id, &c.Name, &c.Email, &c.Phone); err != nil {
			return nil, err
		}
		c.ContactID, c.Source = &id, "contact"
		out = append(out, c)
	}
	return out, crows.Err()
}
