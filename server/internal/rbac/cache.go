package rbac

import (
	"context"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// DefaultCacheTTL, Cache'in bir rolün izinlerini yeniden okumadan önce tuttuğu süredir. role_permissions tohum veridir ve
// çalışırken onu değiştiren bir API yoktur; elle (SQL ile) yapılan bir değişiklik en geç bu süre sonra etkili olur.
const DefaultCacheTTL = 60 * time.Second

// Cache, her istekteki izin denetimini veritabanına gitmeden yanıtlar: bir rolün izin kümesi ilk sorulduğunda okunur ve
// ttl boyunca tutulur. Hatalar önbelleğe alınmaz. Eşzamanlı kullanıma uygundur.
type Cache struct {
	pool *pgxpool.Pool
	ttl  time.Duration
	now  func() time.Time

	mu    sync.Mutex
	roles map[string]cachedRole
}

type cachedRole struct {
	keys    []string // bayt sırasıyla sıralı (bkz. Permissions)
	expires time.Time
}

// NewCache, ttl süreli bir izin önbelleği kurar; ttl <= 0 önbelleği kapatır (her soru tabloya gider).
func NewCache(pool *pgxpool.Pool, ttl time.Duration) *Cache {
	return &Cache{pool: pool, ttl: ttl, now: time.Now, roles: map[string]cachedRole{}}
}

// HasPermission, HasPermission'ın önbellekli karşılığıdır.
func (c *Cache) HasPermission(ctx context.Context, role, permissionKey string) (bool, error) {
	keys, err := c.Permissions(ctx, role)
	if err != nil {
		return false, err
	}
	_, found := slices.BinarySearch(keys, permissionKey)
	return found, nil
}

// Permissions, rolün izin anahtarlarını (sıralı) döndürür. Dönen dilim paylaşılır; çağıran değiştirmemelidir.
func (c *Cache) Permissions(ctx context.Context, role string) ([]string, error) {
	if c.ttl > 0 {
		c.mu.Lock()
		cached, ok := c.roles[role]
		c.mu.Unlock()
		if ok && c.now().Before(cached.expires) {
			return cached.keys, nil
		}
	}
	keys, err := Permissions(ctx, c.pool, role)
	if err != nil {
		return nil, err
	}
	if c.ttl > 0 {
		c.mu.Lock()
		c.roles[role] = cachedRole{keys: keys, expires: c.now().Add(c.ttl)}
		c.mu.Unlock()
	}
	return keys, nil
}

// Invalidate, önbelleği boşaltır: bir sonraki soru tabloyu yeniden okur.
func (c *Cache) Invalidate() {
	c.mu.Lock()
	clear(c.roles)
	c.mu.Unlock()
}

// CachedRole, önbellekteki bir rolün izinleridir (bkz. Snapshot).
type CachedRole struct {
	Role        string    `json:"role"`
	Permissions []string  `json:"permissions"`
	ExpiresAt   time.Time `json:"expires_at"`
}

// CacheSnapshot, izin önbelleğinin salt okunur anlık görüntüsüdür (Sistem Araçları → Cache Durumu).
type CacheSnapshot struct {
	TTLSeconds int          `json:"ttl_seconds"` // 0 = önbellek kapalı
	Roles      []CachedRole `json:"roles"`
}

// Snapshot, süresi dolmamış girdileri rol adına göre sıralı döndürür. Süresi dolmuş girdi listelenmez: bir sonraki
// soruda tablodan yeniden okunacaktır.
func (c *Cache) Snapshot() CacheSnapshot {
	now := c.now()
	snap := CacheSnapshot{TTLSeconds: int(max(c.ttl, 0).Seconds()), Roles: []CachedRole{}}
	c.mu.Lock()
	for role, cached := range c.roles {
		if now.Before(cached.expires) {
			snap.Roles = append(snap.Roles, CachedRole{Role: role, Permissions: slices.Clone(cached.keys), ExpiresAt: cached.expires})
		}
	}
	c.mu.Unlock()
	slices.SortFunc(snap.Roles, func(a, b CachedRole) int { return strings.Compare(a.Role, b.Role) })
	return snap
}
