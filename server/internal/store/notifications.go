package store

import (
	"context"
	"slices"
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
       COALESCE(CASE r.channel WHEN 'sms' THEN COALESCE(u.phone, oc.phone) ELSE COALESCE(u.email, oc.email) END, ''),
       c.enabled
FROM notification_routes r
JOIN notification_channels c ON c.channel = r.channel
LEFT JOIN users u ON u.id = r.user_id
LEFT JOIN contacts oc ON oc.id = r.contact_id`

func scanRoute(row interface{ Scan(...any) error }) (model.NotificationRoute, error) {
	var r model.NotificationRoute
	err := row.Scan(&r.ID, &r.OrganizationID, &r.HostID, &r.UserID, &r.ContactID, &r.Channel, &r.MinLevel, &r.CreatedAt, &r.RecipientName, &r.RecipientTarget, &r.ChannelEnabled)
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

// ListSystem, sistem kapsamlı kurallardır (organizasyon ve sunucu boş: bütün sunucular için).
func (s *Notifications) ListSystem(ctx context.Context) ([]model.NotificationRoute, error) {
	rows, err := s.pool.Query(ctx, routeSelect+` WHERE r.organization_id IS NULL AND r.host_id IS NULL ORDER BY r.created_at`)
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

// Create, bir kural ekler. Kapsam organizasyon, sunucu ya da hiçbiri (sistem: bütün sunucular); alıcı kullanıcı ya da
// kişiden tam olarak biri olmalıdır.
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

// Alıcı kaynakları: bir alıcının sistem kuralından mı, bir organizasyonun kuralından mı, sunucunun kendi kuralından mı
// geldiği.
const (
	RecipientSourceSystem       = "system"
	RecipientSourceOrganization = "organization"
	RecipientSourceHost         = "host"
)

// RecipientSource, bir alıcının nereden geldiğidir. Name yalnızca organizasyon kuralında doludur: kuralın
// organizasyonunun adı (sunucunun kendi organizasyonu ya da bir üst organizasyon).
type RecipientSource struct {
	Kind string
	Name string
}

// Recipient, bir alert bildiriminin tek bir alıcısıdır.
type Recipient struct {
	Channel string
	Address string // e-posta adresi ya da telefon numarası
	Name    string
	// ChannelOff, alıcıyı veren kuralın kanalı kapalı (ya da ayarı yapılmamış) demektir: gönderilmez, alert motoru
	// bunu loglar.
	ChannelOff bool
	// NoAccess, alıcı bir panel kullanıcısıdır ve bu sunucuyu artık göremiyor demektir (organizasyondan çıkarılmış, rolü
	// düşmüş ya da sunucu ataması kaldırılmış): kural yerinde kalır ama ileti gitmez; alert motoru bunu loglar.
	NoAccess bool
	// Source alıcının ilk geldiği yerdir; Also aynı kanal ve adresi ayrıca veren öteki kaynaklardır (tekrarsız): alıcı
	// yine tek ileti alır, bunlar yalnızca gösterim içindir (bkz. "Bu alert kime gider?" önizlemesi).
	Source RecipientSource
	Also   []RecipientSource
}

// ResolveRecipients, hostID'deki level seviyesindeki bir alert'in alıcılarını belirler (bkz. docs/MIMARI.md bölüm 8).
// Alıcılar yalnızca bildirim kurallarından gelir; kurallar toplanır, hiçbiri diğerini ezmez:
//
//  1. sistem kuralları (bütün sunucular);
//  2. sunucunun organizasyonunun ve bütün üst organizasyonlarının kuralları;
//  3. sunucunun kendi kuralları.
//
// Kuralın en düşük seviyesi alert seviyesinin altındaysa kural alıcı üretmez. Kullanıcı alıcılar gönderim anında da
// panelin erişim kuralıyla süzülür (access.Scope.CanViewHost): süper admin her sunucuyu, org admin atandığı
// organizasyonların (ve alt dallarının) sunucularını, operatör yalnızca kendisine atanmış sunucuları görür; göremeyen
// NoAccess işaretlenir. Kişiler (sistem ya da organizasyon) panel hesabı olmadığı için süzülmez.
//
// Aynı kanal ve adres bir kez döner (ilk geldiği yerle: önce sistem kuralları, sonra organizasyon ve sunucu kuralları
// alıcı adına göre; öteki kaynaklar Also'da). Aynı adresi erişimi olan bir kaynak da veriyorsa ileti gider. Hiç kural
// yoksa sonuç boştur.
func (s *Notifications) ResolveRecipients(ctx context.Context, hostID, orgID uuid.UUID, level string) ([]Recipient, error) {
	rows, err := s.pool.Query(ctx, orgChainCTE(2)+`,
	lv AS (SELECT array_position(ARRAY['info', 'warning', 'critical'], $3::text) AS rank)
	SELECT r.channel,
	       CASE r.channel WHEN 'sms' THEN COALESCE(u.phone, k.phone) ELSE COALESCE(u.email, k.email) END AS address,
	       COALESCE(u.full_name, u.email, k.name, ''),
	       NOT c.enabled,
	       CASE WHEN u.id IS NULL OR u.role = $4 THEN false
	            WHEN u.role = $5 THEN NOT EXISTS (SELECT 1 FROM user_organizations uo JOIN chain ch ON ch.id = uo.organization_id
	                                              WHERE uo.user_id = u.id)
	            WHEN u.role = $6 THEN NOT EXISTS (SELECT 1 FROM user_hosts uh WHERE uh.user_id = u.id AND uh.host_id = $1)
	            ELSE true END,
	       CASE WHEN r.host_id IS NOT NULL THEN 'host' WHEN r.organization_id IS NOT NULL THEN 'organization' ELSE 'system' END,
	       COALESCE(og.name, '')
	FROM notification_routes r
	JOIN notification_channels c ON c.channel = r.channel
	LEFT JOIN users u ON u.id = r.user_id
	LEFT JOIN contacts k ON k.id = r.contact_id
	LEFT JOIN organizations og ON og.id = r.organization_id
	WHERE (r.host_id = $1 OR r.organization_id IN (SELECT id FROM chain) OR (r.organization_id IS NULL AND r.host_id IS NULL))
	  AND array_position(ARRAY['info', 'warning', 'critical'], r.min_level) <= (SELECT rank FROM lv)
	  AND COALESCE(CASE r.channel WHEN 'sms' THEN COALESCE(u.phone, k.phone) ELSE COALESCE(u.email, k.email) END, '') <> ''
	ORDER BY CASE WHEN r.organization_id IS NULL AND r.host_id IS NULL THEN 0 ELSE 1 END,
	         lower(COALESCE(u.full_name, u.email, k.name, '')), r.created_at`,
		hostID, orgID, level, model.RoleSuperAdmin, model.RoleOrgAdmin, model.RoleOperator)
	if err != nil {
		return nil, err
	}
	all, err := collect(rows, func(row interface{ Scan(...any) error }) (Recipient, error) {
		var r Recipient
		err := row.Scan(&r.Channel, &r.Address, &r.Name, &r.ChannelOff, &r.NoAccess, &r.Source.Kind, &r.Source.Name)
		return r, err
	})
	if err != nil {
		return nil, err
	}
	seen := map[string]int{} // kanal+adres → out'taki yeri
	out := []Recipient{}
	for _, r := range all {
		key := r.Channel + "\x00" + strings.ToLower(r.Address)
		i, dup := seen[key]
		if !dup {
			seen[key] = len(out)
			out = append(out, r)
			continue
		}
		first := &out[i]
		if first.NoAccess && !r.NoAccess {
			// Aynı adresi erişimi olan bir kaynak da veriyor: ileti gider, kaynağı bu olur.
			moved := first.Source
			also := first.Also
			*first = r
			first.Also = also
			if !slices.Contains(first.Also, moved) {
				first.Also = append(first.Also, moved)
			}
			continue
		}
		if first.Source != r.Source && !slices.Contains(first.Also, r.Source) {
			first.Also = append(first.Also, r.Source)
		}
	}
	return out, nil
}

// Candidate, bir bildirim kuralına alıcı olarak seçilebilecek bir kullanıcı ya da kişidir.
type Candidate struct {
	UserID    *uuid.UUID `json:"user_id,omitempty"`
	ContactID *uuid.UUID `json:"contact_id,omitempty"`
	Name      string     `json:"name"`
	Email     string     `json:"email,omitempty"`
	Phone     string     `json:"phone,omitempty"`
	// Source alıcının nereden geldiğini söyler: "super_admin", "org_admin", "operator", "contact" ya da "system_contact".
	Source string `json:"source"`
}

// SystemCandidates, sistem kurallarının alıcı adaylarıdır: süper adminler (bütün sunucuları yalnızca onlar görür) ve
// sistem kişileri. Bildirim sunucu adı, IP ve organizasyon taşıdığı için kapsamı dar bir kullanıcı sistem kuralına
// yazılamaz.
func (s *Notifications) SystemCandidates(ctx context.Context) ([]Candidate, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT u.id, NULL::uuid, COALESCE(u.full_name, u.email), u.email, COALESCE(u.phone, ''), u.role
		   FROM users u WHERE u.role = $1
		 UNION ALL
		 SELECT NULL::uuid, k.id, k.name, COALESCE(k.email, ''), COALESCE(k.phone, ''), 'system_contact'
		   FROM contacts k WHERE k.organization_id IS NULL
		 ORDER BY 3`, model.RoleSuperAdmin)
	if err != nil {
		return nil, err
	}
	return collect(rows, func(row interface{ Scan(...any) error }) (Candidate, error) {
		var c Candidate
		err := row.Scan(&c.UserID, &c.ContactID, &c.Name, &c.Email, &c.Phone, &c.Source)
		return c, err
	})
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
		 FROM contacts oc JOIN chain c ON c.id = oc.organization_id ORDER BY oc.name`, orgID)
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
