package rbac

import (
	"context"
	"slices"
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
