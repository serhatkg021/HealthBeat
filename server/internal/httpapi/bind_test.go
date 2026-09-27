package httpapi

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
)

type bindTestNested struct {
	Level float64 `json:"level"`
}

type bindTestRequest struct {
	Name    string                    `json:"name"`
	Count   int                       `json:"count"`
	Enabled *bool                     `json:"enabled"`
	OrgID   uuid.UUID                 `json:"org_id"`
	Tags    []string                  `json:"tags"`
	Levels  map[string]bindTestNested `json:"levels"`
}

func (req *bindTestRequest) Validate() error {
	req.Name = strings.TrimSpace(req.Name)
	switch req.Name {
	case "":
		return errors.New("name zorunlu")
	case "özel":
		return conflict("özel hata olduğu gibi döner")
	}
	return nil
}

func bindBody(body string) (bindTestRequest, error) {
	return bind[bindTestRequest](httptest.NewRequest(http.MethodPost, "/x", strings.NewReader(body)))
}

func TestBindDecodesAndValidates(t *testing.T) {
	req, err := bindBody(`{"name": "  web  ", "count": 3}`)
	if err != nil || req.Name != "web" || req.Count != 3 {
		t.Fatalf("bind = %+v, %v (Validate should trim the name)", req, err)
	}

	_, err = bindBody(`{"name": " "}`)
	var apiErr *apiError
	if !errors.As(err, &apiErr) || apiErr.Status != http.StatusBadRequest || apiErr.Message != "name zorunlu" || apiErr.Fields != nil {
		t.Fatalf("validation error = %#v", err)
	}

	_, err = bindBody(`{"name": "özel"}`)
	if !errors.As(err, &apiErr) || apiErr.Status != http.StatusConflict {
		t.Fatalf("an *apiError from Validate must pass through: %#v", err)
	}
}

func TestBindDecodeErrorsNameTheField(t *testing.T) {
	cases := []struct {
		body       string
		wantFields map[string]string
	}{
		{`{"name": "a", "count": "üç"}`, map[string]string{"count": "tam sayı olmalı"}},
		{`{"name": "a", "count": 1.5}`, map[string]string{"count": "tam sayı olmalı"}},
		{`{"name": 5}`, map[string]string{"name": "metin olmalı"}},
		{`{"name": "a", "enabled": "evet"}`, map[string]string{"enabled": "true ya da false olmalı"}},
		{`{"name": "a", "org_id": 7}`, map[string]string{"org_id": "metin olmalı"}},
		{`{"name": "a", "tags": "x"}`, map[string]string{"tags": "dizi olmalı"}},
		{`{"name": "a", "levels": {"cpu": {"level": "yüksek"}}}`, map[string]string{"levels.cpu.level": "sayı olmalı"}},
		{`{"name": "a", "colour": "red"}`, map[string]string{"colour": "bilinmeyen alan"}},
		// Alana bağlanamayan hatalar yalnızca genel mesajı taşır.
		{`{"name": "a"`, nil},
		{``, nil},
		{`{"name": "a", "org_id": "abc"}`, nil},
	}
	for _, tc := range cases {
		_, err := bindBody(tc.body)
		var apiErr *apiError
		if !errors.As(err, &apiErr) || apiErr.Status != http.StatusBadRequest || apiErr.Message != "geçersiz istek gövdesi" {
			t.Errorf("%s: error = %#v", tc.body, err)
			continue
		}
		if len(apiErr.Fields) != len(tc.wantFields) {
			t.Errorf("%s: fields = %v, want %v", tc.body, apiErr.Fields, tc.wantFields)
			continue
		}
		for k, v := range tc.wantFields {
			if apiErr.Fields[k] != v {
				t.Errorf("%s: fields = %v, want %v", tc.body, apiErr.Fields, tc.wantFields)
			}
		}
	}
}

func TestBindRejectsOversizedBody(t *testing.T) {
	body := `{"name": "` + strings.Repeat("a", maxBodyBytes) + `"}`
	_, err := bindBody(body)
	var apiErr *apiError
	if !errors.As(err, &apiErr) || apiErr.Status != http.StatusBadRequest || apiErr.Message != "geçersiz istek gövdesi" {
		t.Fatalf("oversized body error = %#v", err)
	}
}
