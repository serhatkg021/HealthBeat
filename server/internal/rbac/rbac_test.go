package rbac_test

import (
	"context"
	"testing"

	"healthbeat-server/internal/rbac"
	"healthbeat-server/internal/testdb"
)

func TestHasPermissionMatchesSeededMatrix(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()

	cases := []struct {
		role, perm string
		want       bool
	}{
		{"super_admin", "user.create", true},
		{"super_admin", "organization.delete", true},
		{"super_admin", "audit.view", true},

		{"org_admin", "host.create", true},
		{"org_admin", "threshold.edit", true},
		{"org_admin", "user.create", false},         // organizasyonlar arası kullanıcı yönetimi yok
		{"org_admin", "organization.create", false}, // organizasyonlar yalnızca super_admin içindir
		{"org_admin", "audit.view", false},

		{"operator", "host.view", true},
		{"operator", "alert.acknowledge", true},
		{"operator", "host.create", false},
		{"operator", "threshold.edit", false},
		{"operator", "organization.view", false}, // host'larına bunun yerine /me/hosts ile ulaşır

		{"nonexistent_role", "host.view", false},
		{"operator", "nonexistent.permission", false},
		{"", "", false},
	}
	for _, c := range cases {
		got, err := rbac.HasPermission(ctx, pool, c.role, c.perm)
		if err != nil {
			t.Fatalf("HasPermission(%q, %q): %v", c.role, c.perm, err)
		}
		if got != c.want {
			t.Errorf("HasPermission(%q, %q) = %v, want %v", c.role, c.perm, got, c.want)
		}
	}
}

// Karar kodtan değil role_permissions tablosundan gelmeli: çalışma zamanında eklenen ya da
// kaldırılan bir satır yanıtı hemen değiştirmeli.
func TestHasPermissionReadsTableNotCode(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()

	if ok, _ := rbac.HasPermission(ctx, pool, "operator", "threshold.edit"); ok {
		t.Fatal("precondition: operator should not have threshold.edit")
	}

	if _, err := pool.Exec(ctx, `INSERT INTO role_permissions (role, permission_key) VALUES ('operator', 'threshold.edit')`); err != nil {
		t.Fatal(err)
	}
	if ok, err := rbac.HasPermission(ctx, pool, "operator", "threshold.edit"); err != nil || !ok {
		t.Fatalf("after grant: ok=%v err=%v, want true", ok, err)
	}

	if _, err := pool.Exec(ctx, `DELETE FROM role_permissions WHERE role = 'super_admin' AND permission_key = 'user.create'`); err != nil {
		t.Fatal(err)
	}
	if ok, err := rbac.HasPermission(ctx, pool, "super_admin", "user.create"); err != nil || ok {
		t.Fatalf("after revoke: ok=%v err=%v, want false", ok, err)
	}
}
