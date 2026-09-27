package httpapi_test

import (
	"encoding/json"
	"strings"
	"testing"
)

// Kullanıcı dönen hiçbir yanıtta şifre hash'i yoktur (ne alan adı ne de bcrypt değeri).
func TestUserResponsesNeverCarryThePasswordHash(t *testing.T) {
	a := newAPI(t)
	root, _ := a.login("root@x.test", "super_admin")
	opTok, _ := a.login("op@x.test", "operator")

	var created struct {
		ID string `json:"id"`
	}
	a.expect(201, "POST", "/api/v1/users", root, map[string]any{"email": "new@x.test", "password": "a-long-enough-password", "role": "operator"}, &created)

	// Sıra önemli: şifre değişikliği oturumları kapatır, en sona kalır.
	for _, req := range []struct {
		name, method, path, token string
		body                      any
	}{
		{"list", "GET", "/api/v1/users", root, nil},
		{"get", "GET", "/api/v1/users/" + created.ID, root, nil},
		{"update", "PUT", "/api/v1/users/" + created.ID, root, map[string]any{"full_name": "Yeni"}},
		{"me", "GET", "/api/v1/me", opTok, nil},
		{"login", "POST", "/api/v1/auth/login", "", map[string]string{"email": "op@x.test", "password": password}},
		{"change password", "POST", "/api/v1/me/password", opTok,
			map[string]string{"current_password": password, "new_password": "another-long-password"}},
	} {
		var raw json.RawMessage
		a.expect(200, req.method, req.path, req.token, req.body, &raw)
		if body := string(raw); strings.Contains(body, "password_hash") || strings.Contains(body, "$2a$") {
			t.Errorf("%s response exposes the password hash: %s", req.name, body)
		}
	}
}
