package httpapi_test

import (
	"slices"
	"testing"
)

// PATCH /me: herkes (izin gerekmeden) kendi adını ve telefonunu değiştirir; e-posta, rol ve şifre buradan değişmez.
func TestUpdateOwnProfile(t *testing.T) {
	a := newAPI(t)
	token, _ := a.login("op@x.test", "operator")

	type me struct {
		Email       string   `json:"email"`
		Role        string   `json:"role"`
		FullName    *string  `json:"full_name"`
		Phone       *string  `json:"phone"`
		Permissions []string `json:"permissions"`
	}
	var got me
	a.expect(200, "PATCH", "/api/v1/me", token, map[string]any{"full_name": "  Ayşe Operatör  ", "phone": "+90 555 000 00 00"}, &got)
	if got.FullName == nil || *got.FullName != "Ayşe Operatör" || got.Phone == nil || *got.Phone != "+90 555 000 00 00" {
		t.Fatalf("updated profile = %+v, want the trimmed name and the phone", got)
	}
	if got.Email != "op@x.test" || got.Role != "operator" || !slices.Contains(got.Permissions, "alert.acknowledge") {
		t.Fatalf("response lost the identity or permissions: %+v", got)
	}

	// Verilmeyen alan değişmez; boş metin alanı temizler.
	a.expect(200, "PATCH", "/api/v1/me", token, map[string]any{"phone": ""}, &got)
	var after me
	a.expect(200, "GET", "/api/v1/me", token, nil, &after)
	if after.FullName == nil || *after.FullName != "Ayşe Operatör" || after.Phone != nil {
		t.Fatalf("after clearing the phone = %+v, want the name kept and no phone", after)
	}

	// Kimlik ve yetki alanları reddedilir, hiçbir şey değişmez.
	for _, body := range []map[string]any{
		{"email": "baska@x.test"},
		{"role": "super_admin"},
		{"password": "yeni-uzun-bir-sifre-123"},
		{"two_factor_enabled": true},
		{"phone": "telefon değil"},
	} {
		a.expect(400, "PATCH", "/api/v1/me", token, body, nil)
	}
	a.expect(200, "GET", "/api/v1/me", token, nil, &after)
	if after.Email != "op@x.test" || after.Role != "operator" {
		t.Fatalf("identity changed through PATCH /me: %+v", after)
	}

	a.expect(401, "PATCH", "/api/v1/me", "", map[string]any{"full_name": "x"}, nil)
	if n := a.auditCount("user.update_self"); n != 2 {
		t.Errorf("audit rows for user.update_self = %d, want 2 (one per successful update)", n)
	}
}
