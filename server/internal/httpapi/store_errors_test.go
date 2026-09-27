package httpapi_test

import (
	"testing"

	"github.com/google/uuid"
)

// Store'un adlandırılmış nedenleri kendi Türkçe metinleriyle döner; "çakışma: " / "bulunamadı: " öneki sızmaz.
func TestStoreReasonsReachTheClientAsPlainMessages(t *testing.T) {
	a := newAPI(t)
	root, _ := a.login("root@x.test", "super_admin")
	parent := a.createOrg(root, "holding")
	child := a.createChildOrg(root, "acme", parent)
	a.createPushHost(root, child, "web")
	a.expect(201, "POST", "/api/v1/users", root, map[string]any{"email": "dup@x.test", "password": "a-long-enough-password", "role": "operator"}, nil)
	a.expect(201, "POST", "/api/v1/thresholds", root, map[string]any{"organization_id": child, "metric_type": "ram", "warning_level": 90, "critical_level": 95}, nil)
	var foreign idBody
	a.expect(201, "POST", "/api/v1/organizations/"+parent.String()+"/contacts", root, map[string]any{"name": "Yabancı", "email": "y@x.test"}, &foreign)

	cases := []struct {
		name         string
		method, path string
		body         any
		status       int
		message      string
	}{
		{"email taken", "POST", "/api/v1/users", map[string]any{"email": "dup@x.test", "password": "a-long-enough-password", "role": "operator"},
			409, "bu e-posta zaten kullanımda"},
		{"organization name taken", "POST", "/api/v1/organizations", map[string]any{"name": "acme", "parent_organization_id": parent},
			409, "aynı üst şirketin altında bu ada sahip bir organizasyon zaten var"},
		{"parent missing", "POST", "/api/v1/organizations", map[string]any{"name": "yetim", "parent_organization_id": uuid.New()},
			404, "üst organizasyon bulunamadı"},
		{"organization not empty", "DELETE", "/api/v1/organizations/" + child.String(), nil,
			409, "organizasyonda hâlâ alt organizasyon ya da sunucu var"},
		{"host title taken", "POST", "/api/v1/hosts", map[string]any{"organization_id": child, "title": "web", "ip": "10.0.0.8", "mode": "push", "interval_seconds": 10},
			409, "bu organizasyonda aynı adlı bir sunucu zaten var"},
		{"threshold exists", "POST", "/api/v1/thresholds", map[string]any{"organization_id": child, "metric_type": "ram", "warning_level": 80, "critical_level": 95},
			409, "bu kapsam ve metrik için zaten bir eşik var"},
		{"contact manager from another organization", "POST", "/api/v1/organizations/" + child.String() + "/contacts", map[string]any{"name": "X", "email": "x@x.test", "manager_contact_id": foreign.ID},
			400, "yönetici aynı organizasyondan bir iletişim kişisi olmalı"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var got struct {
				Error string `json:"error"`
			}
			a.expect(tc.status, tc.method, tc.path, root, tc.body, &got)
			if got.Error != tc.message {
				t.Fatalf("error = %q, want %q", got.Error, tc.message)
			}
		})
	}
}
