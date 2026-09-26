package httpapi_test

import (
	"slices"
	"testing"
)

// GET /me, rolün izin anahtarlarını (sıralı) da döndürür; panel rol adına değil izne bakabilsin diye.
func TestMeListsRolePermissions(t *testing.T) {
	a := newAPI(t)
	for role, want := range map[string]struct{ has, lacks string }{
		"super_admin": {"user.create", ""},
		"org_admin":   {"host.create", "user.create"},
		"operator":    {"alert.acknowledge", "host.create"},
	} {
		token, _ := a.login(role+"@x.test", role)
		var me struct {
			Email       string   `json:"email"`
			Role        string   `json:"role"`
			Permissions []string `json:"permissions"`
		}
		a.expect(200, "GET", "/api/v1/me", token, nil, &me)
		if me.Email != role+"@x.test" || me.Role != role {
			t.Fatalf("%s: user fields lost: %+v", role, me)
		}
		if !slices.IsSorted(me.Permissions) || !slices.Contains(me.Permissions, want.has) ||
			(want.lacks != "" && slices.Contains(me.Permissions, want.lacks)) {
			t.Errorf("%s: permissions = %v (want %q, not %q)", role, me.Permissions, want.has, want.lacks)
		}
	}
}
