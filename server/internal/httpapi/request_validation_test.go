package httpapi_test

import (
	"testing"

	"github.com/google/uuid"
)

// Çözülemeyen gövdede mesaj aynı kalır ("geçersiz istek gövdesi"); sorun bir alana bağlanabiliyorsa fields onu adlandırır.
func TestDecodeErrorsNameTheField(t *testing.T) {
	a := newAPI(t)
	root, _ := a.login("root@x.test", "super_admin")
	org := a.createOrg(root, "o")

	type apiError struct {
		Error  string            `json:"error"`
		Code   string            `json:"code"`
		Fields map[string]string `json:"fields"`
	}
	cases := []struct {
		name       string
		method     string
		path       string
		body       any
		wantFields map[string]string
	}{
		{"wrong type", "POST", "/api/v1/hosts", map[string]any{
			"organization_id": org, "title": "web", "ip": "10.0.0.1", "mode": "push", "interval_seconds": "60",
		}, map[string]string{"interval_seconds": "tam sayı olmalı"}},
		{"nested wrong type", "POST", "/api/v1/thresholds", map[string]any{
			"metric_type": "cpu", "warning_level": "yüksek", "critical_level": 90,
		}, map[string]string{"warning_level": "sayı olmalı"}},
		{"unknown field", "POST", "/api/v1/organizations", map[string]any{"name": "x", "colour": "red"},
			map[string]string{"colour": "bilinmeyen alan"}},
		{"malformed JSON", "POST", "/api/v1/organizations", rawBody(`{"name": `), nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var got apiError
			a.expect(400, tc.method, tc.path, root, tc.body, &got)
			if got.Error != "geçersiz istek gövdesi" || got.Code != "validation_failed" {
				t.Fatalf("error = %+v", got)
			}
			if len(got.Fields) != len(tc.wantFields) {
				t.Fatalf("fields = %v, want %v", got.Fields, tc.wantFields)
			}
			for k, v := range tc.wantFields {
				if got.Fields[k] != v {
					t.Fatalf("fields = %v, want %v", got.Fields, tc.wantFields)
				}
			}
		})
	}

	// Doğrulama (Validate) hataları mesajlarını korur ve fields taşımaz.
	var got apiError
	a.expect(400, "POST", "/api/v1/hosts", root, map[string]any{
		"organization_id": org, "title": "web", "ip": "yok", "mode": "push", "interval_seconds": 60,
	}, &got)
	if got.Error != "ip geçerli bir IP adresi olmalı" || got.Fields != nil {
		t.Fatalf("validation error = %+v", got)
	}

	// Yoldaki geçersiz kimlik ve bulunamayan kayıt da ortak yardımcılardan geçer.
	a.expect(400, "GET", "/api/v1/hosts/abc", root, nil, &got)
	if got.Error != "geçersiz sunucu kimliği" {
		t.Fatalf("bad path id = %+v", got)
	}
	a.expect(404, "GET", "/api/v1/hosts/"+uuid.NewString(), root, nil, &got)
	if got.Error != "sunucu bulunamadı" || got.Code != "not_found" {
		t.Fatalf("missing host = %+v", got)
	}
}
