package rbac

import (
	"context"
	"slices"
	"testing"
	"time"

	"healthbeat-server/internal/testdb"
)

func TestCacheServesUntilTTLThenRereads(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	now := time.Now()
	c := NewCache(pool, time.Minute)
	c.now = func() time.Time { return now }

	if ok, err := c.HasPermission(ctx, "operator", "threshold.edit"); err != nil || ok {
		t.Fatalf("precondition: operator threshold.edit = %v, %v", ok, err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO role_permissions (role, permission_key) VALUES ('operator', 'threshold.edit')`); err != nil {
		t.Fatal(err)
	}

	now = now.Add(59 * time.Second)
	if ok, _ := c.HasPermission(ctx, "operator", "threshold.edit"); ok {
		t.Fatal("grant visible before the TTL expired; the cache is not being used")
	}
	now = now.Add(2 * time.Second)
	if ok, err := c.HasPermission(ctx, "operator", "threshold.edit"); err != nil || !ok {
		t.Fatalf("after TTL = %v, %v; want the grant to be visible", ok, err)
	}

	if _, err := pool.Exec(ctx, `DELETE FROM role_permissions WHERE role = 'operator' AND permission_key = 'threshold.edit'`); err != nil {
		t.Fatal(err)
	}
	c.Invalidate()
	if ok, _ := c.HasPermission(ctx, "operator", "threshold.edit"); ok {
		t.Fatal("revoke not visible after Invalidate")
	}
}

func TestCacheWithoutTTLAlwaysReadsTable(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	c := NewCache(pool, 0)

	if ok, _ := c.HasPermission(ctx, "operator", "threshold.edit"); ok {
		t.Fatal("precondition: operator should not have threshold.edit")
	}
	if _, err := pool.Exec(ctx, `INSERT INTO role_permissions (role, permission_key) VALUES ('operator', 'threshold.edit')`); err != nil {
		t.Fatal(err)
	}
	if ok, err := c.HasPermission(ctx, "operator", "threshold.edit"); err != nil || !ok {
		t.Fatalf("ttl 0 = %v, %v; want the grant to be visible immediately", ok, err)
	}
}

// Önbellekli yanıt tablonun kendisiyle aynıdır: her rol × izin için HasPermission ile karşılaştırılır.
func TestCacheMatchesTable(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	c := NewCache(pool, time.Minute)

	var keys []string
	rows, err := pool.Query(ctx, `SELECT DISTINCT permission_key FROM role_permissions`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var k string
		if err := rows.Scan(&k); err != nil {
			t.Fatal(err)
		}
		keys = append(keys, k)
	}
	rows.Close()
	keys = append(keys, "nonexistent.permission", "")

	for _, role := range []string{"super_admin", "org_admin", "operator", "nonexistent_role", ""} {
		perms, err := c.Permissions(ctx, role)
		if err != nil || !slices.IsSorted(perms) {
			t.Fatalf("Permissions(%q) = %v, %v; want sorted", role, perms, err)
		}
		for _, k := range keys {
			want, err := HasPermission(ctx, pool, role, k)
			if err != nil {
				t.Fatal(err)
			}
			if got, err := c.HasPermission(ctx, role, k); err != nil || got != want {
				t.Errorf("cache HasPermission(%q, %q) = %v, %v; table says %v", role, k, got, err, want)
			}
		}
	}
}
