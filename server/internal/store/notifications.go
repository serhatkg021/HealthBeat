package store

import (
	"context"
	"strings"

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

func (s *Notifications) ListByOrganization(ctx context.Context, orgID uuid.UUID) ([]model.NotificationRoute, error) {
	rows, err := s.pool.Query(ctx, routeSelect+` WHERE r.organization_id = $1 ORDER BY r.created_at`, orgID)
	if err != nil {
		return nil, err
	}
	return collect(rows, scanRoute)
}

func (s *Notifications) ListByHost(ctx context.Context, hostID uuid.UUID) ([]model.NotificationRoute, error) {
	rows, err := s.pool.Query(ctx, routeSelect+` WHERE r.host_id = $1 ORDER BY r.created_at`, hostID)
	if err != nil {
		return nil, err
	}
	return collect(rows, scanRoute)
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
			return model.NotificationRoute{}, ErrRouteExists
		case pgForeignKeyViolation:
			return model.NotificationRoute{}, ErrRouteTargetMissing
		case pgCheckViolation:
			return model.NotificationRoute{}, ErrRouteShape
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
			return model.NotificationRoute{}, ErrRouteExists
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
	// ChannelOff, alıcıyı veren kuralın kanalı kapalı (ya da ayarı yapılmamış) demektir: gönderilmez, alert motoru
	// bunu loglar. Sistem sahipleri kapalı kanaldan hiç gelmez.
	ChannelOff bool
}

// ResolveRecipients, level seviyesindeki bir alert'in alıcılarını belirler (bkz. docs/MIMARI.md bölüm 8). Bildirimin
// amacı sistem sahibidir; organizasyon ve sunucu kuralları "bunlara da gitsin" der ve toplanır, hiçbiri diğerini ezmez:
//
//  1. sistem sahipleri: her açık kanaldan, seviye kanalın sahip seviyesine (owner_min_level) ulaşıyorsa ve sahip o
//     kanaldan almak istiyorsa (email_enabled / sms_enabled);
//  2. sunucunun organizasyonunun ve bütün üst organizasyonlarının kuralları;
//  3. sunucunun kendi kuralları.
//
// Kuralın en düşük seviyesi alert seviyesinin altındaysa kural alıcı üretmez. Aynı kanal ve adres bir kez döner (ilk
// geldiği yerle: önce sahipler). Hiç sahip ve kural yoksa sonuç boştur.
func (s *Notifications) ResolveRecipients(ctx context.Context, hostID, orgID uuid.UUID, level string) ([]Recipient, error) {
	rows, err := s.pool.Query(ctx, orgChainCTE(2)+`,
	lv AS (SELECT array_position(ARRAY['info', 'warning', 'critical'], $3::text) AS rank)
	SELECT channel, address, name, channel_off FROM (
		SELECT c.channel,
		       CASE c.channel WHEN 'sms' THEN CASE WHEN o.sms_enabled THEN o.phone END
		                      ELSE CASE WHEN o.email_enabled THEN o.email END END AS address,
		       o.name, false AS channel_off, 0 AS src, lower(o.name) AS sort_name, o.created_at
		FROM notification_owners o
		JOIN notification_channels c ON c.enabled
		WHERE array_position(ARRAY['info', 'warning', 'critical'], c.owner_min_level) <= (SELECT rank FROM lv)
		UNION ALL
		SELECT r.channel,
		       CASE r.channel WHEN 'sms' THEN COALESCE(u.phone, oc.phone) ELSE COALESCE(u.email, oc.email) END,
		       COALESCE(u.full_name, u.email, oc.name, ''), NOT c.enabled, 1, lower(COALESCE(u.full_name, u.email, oc.name, '')),
		       r.created_at
		FROM notification_routes r
		JOIN notification_channels c ON c.channel = r.channel
		LEFT JOIN users u ON u.id = r.user_id
		LEFT JOIN organization_contacts oc ON oc.id = r.contact_id
		WHERE (r.host_id = $1 OR r.organization_id IN (SELECT id FROM chain))
		  AND array_position(ARRAY['info', 'warning', 'critical'], r.min_level) <= (SELECT rank FROM lv)
	) x
	WHERE COALESCE(address, '') <> ''
	ORDER BY src, sort_name, created_at`, hostID, orgID, level)
	if err != nil {
		return nil, err
	}
	all, err := collect(rows, func(row interface{ Scan(...any) error }) (Recipient, error) {
		var r Recipient
		err := row.Scan(&r.Channel, &r.Address, &r.Name, &r.ChannelOff)
		return r, err
	})
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	out := []Recipient{}
	for _, r := range all {
		key := r.Channel + "\x00" + strings.ToLower(r.Address)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, r)
	}
	return out, nil
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
		 SELECT u.id, COALESCE(u.full_name, u.email), u.email, COALESCE(u.phone, ''), u.role FROM users u WHERE u.role = $3
		 UNION
		 SELECT u.id, COALESCE(u.full_name, u.email), u.email, COALESCE(u.phone, ''), u.role FROM users u
		 JOIN user_organizations uo ON uo.user_id = u.id JOIN chain c ON c.id = uo.organization_id WHERE u.role = $4
		 UNION
		 SELECT u.id, COALESCE(u.full_name, u.email), u.email, COALESCE(u.phone, ''), u.role FROM users u
		 JOIN user_hosts uh ON uh.user_id = u.id WHERE $2::uuid IS NOT NULL AND uh.host_id = $2 AND u.role = $5
		 ORDER BY 2`, orgID, hostID, model.RoleSuperAdmin, model.RoleOrgAdmin, model.RoleOperator)
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
