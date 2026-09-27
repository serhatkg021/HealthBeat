package httpapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/google/uuid"

	"healthbeat-server/internal/model"
	"healthbeat-server/internal/store"
)

// organizationResponse, organizasyonun istemciye dönen hâlidir: çağıranın ondaki erişimi eklenir (bkz.
// model.OrgAccessFull). Oluşturma yanıtında erişim yazılmaz.
type organizationResponse struct {
	model.Organization
	Access string `json:"access,omitempty"`
}

// organizationView, o'yu access erişimiyle yanıta çevirir; "context" erişiminde içerik (adres) gizlenir.
func organizationView(o model.Organization, access string) organizationResponse {
	if access == model.OrgAccessContext {
		o.Address = nil
	}
	return organizationResponse{Organization: o, Access: access}
}

func (d *Deps) handleListOrganizations(w http.ResponseWriter, r *http.Request) error {
	fail := failWith("organizasyonlar listelenemedi")
	scope := d.scope(r)

	if scope.IsSuperAdmin() {
		orgs, err := d.organizations.List(r.Context())
		if err != nil {
			return fail("list organizations", err)
		}
		out := make([]organizationResponse, len(orgs))
		for i, o := range orgs {
			out[i] = organizationView(o, model.OrgAccessFull)
		}
		writeJSON(w, http.StatusOK, out)
		return nil
	}

	// org_admin: atandığı organizasyonlar ve altındaki dallar tam erişimlidir; üst zincirleri yalnızca bağlam olarak
	// (ad ve konum) görünür, içerikleri (adres, sunucular) ve kardeş dalları görünmez.
	fullIDs, err := scope.ManagedOrgIDs(r.Context())
	if err != nil {
		return fail("list user organizations", err)
	}
	contextIDs, err := scope.ContextOrgIDs(r.Context())
	if err != nil {
		return fail("list user context organizations", err)
	}
	orgs, err := d.organizations.ListByIDs(r.Context(), append(append([]uuid.UUID{}, fullIDs...), contextIDs...))
	if err != nil {
		return fail("list organizations by ids", err)
	}
	full := make(map[uuid.UUID]struct{}, len(fullIDs))
	for _, id := range fullIDs {
		full[id] = struct{}{}
	}
	out := make([]organizationResponse, len(orgs))
	for i, o := range orgs {
		access := model.OrgAccessContext
		if _, ok := full[o.ID]; ok {
			access = model.OrgAccessFull
		}
		out[i] = organizationView(o, access)
	}
	writeJSON(w, http.StatusOK, out)
	return nil
}

const (
	maxOrgNameLen    = 200
	maxOrgAddressLen = 1000
)

type createOrganizationRequest struct {
	Name                 string     `json:"name"`
	ParentOrganizationID *uuid.UUID `json:"parent_organization_id"`
	Address              *string    `json:"address"`
}

// validateOrgText, ad ve adres uzunluk sınırlarını denetler.
func validateOrgText(name string, address *string) error {
	if len(name) > maxOrgNameLen {
		return errors.New("ad çok uzun")
	}
	if address != nil && len(*address) > maxOrgAddressLen {
		return errors.New("adres çok uzun")
	}
	return nil
}

func (req *createOrganizationRequest) Validate() error {
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" {
		return errors.New("ad zorunlu")
	}
	if err := validateOrgText(req.Name, req.Address); err != nil {
		return err
	}
	if req.Address != nil {
		trimmed := strings.TrimSpace(*req.Address)
		req.Address = &trimmed
	}
	return nil
}

func (d *Deps) handleCreateOrganization(w http.ResponseWriter, r *http.Request) error {
	req, err := bind[createOrganizationRequest](r)
	if err != nil {
		return err
	}

	org, err := d.organizations.Create(r.Context(), req.Name, req.ParentOrganizationID, req.Address)
	if err != nil {
		return storeError(err, "üst organizasyon bulunamadı", failWith("organizasyon oluşturulamadı"), "create organization")
	}

	targetID := org.ID.String()
	d.logAudit(r, "organization.create", "organization", &targetID, map[string]any{"name": org.Name, "parent_organization_id": org.ParentOrganizationID})
	writeJSON(w, http.StatusCreated, org)
	return nil
}

func (d *Deps) handleGetOrganization(w http.ResponseWriter, r *http.Request) error {
	fail := failWith("organizasyon alınamadı")
	id, err := pathID(r, "geçersiz organizasyon kimliği")
	if err != nil {
		return err
	}

	access, err := d.scope(r).OrgAccess(r.Context(), id)
	if err != nil {
		return fail("check org access", err)
	}
	if access == "" {
		return forbidden()
	}

	org, err := d.organizations.GetByID(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		return notFound("organizasyon bulunamadı")
	}
	if err != nil {
		return fail("get organization", err)
	}
	writeJSON(w, http.StatusOK, organizationView(org, access))
	return nil
}

type updateOrganizationRequest struct {
	Name    *string `json:"name"`
	Address *string `json:"address"`
	// ParentOrganizationID: verilmemiş = değişmez; null = kök yap; bir kimlik = o organizasyonun altına taşı.
	ParentOrganizationID json.RawMessage `json:"parent_organization_id"`

	patch store.OrgPatch // Validate'in ürettiği değişiklik
}

func (req *updateOrganizationRequest) Validate() error {
	if req.Name != nil {
		name := strings.TrimSpace(*req.Name)
		if name == "" {
			return errors.New("ad boş olamaz")
		}
		req.patch.Name = &name
	}
	if req.Address != nil {
		trimmed := strings.TrimSpace(*req.Address)
		req.patch.Address = &trimmed
	}
	name := ""
	if req.patch.Name != nil {
		name = *req.patch.Name
	}
	if err := validateOrgText(name, req.patch.Address); err != nil {
		return err
	}
	if raw := bytes.TrimSpace(req.ParentOrganizationID); len(raw) > 0 {
		req.patch.ParentSet = true
		if string(raw) != "null" {
			var parent uuid.UUID
			if err := json.Unmarshal(raw, &parent); err != nil {
				return errors.New("parent_organization_id geçerli bir kimlik ya da null olmalı")
			}
			req.patch.Parent = &parent
		}
	}
	return nil
}

func (d *Deps) handleUpdateOrganization(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "geçersiz organizasyon kimliği")
	if err != nil {
		return err
	}
	req, err := bind[updateOrganizationRequest](r)
	if err != nil {
		return err
	}

	org, err := d.organizations.Update(r.Context(), id, req.patch)
	if err != nil {
		return storeError(err, "organizasyon bulunamadı", failWith("organizasyon güncellenemedi"), "update organization")
	}

	targetID := org.ID.String()
	d.logAudit(r, "organization.update", "organization", &targetID, map[string]any{"name": org.Name, "parent_organization_id": org.ParentOrganizationID})
	writeJSON(w, http.StatusOK, organizationView(org, model.OrgAccessFull))
	return nil
}

func (d *Deps) handleDeleteOrganization(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "geçersiz organizasyon kimliği")
	if err != nil {
		return err
	}

	if err := d.organizations.Delete(r.Context(), id); err != nil {
		return storeError(err, "organizasyon bulunamadı", failWith("organizasyon silinemedi"), "delete organization")
	}

	targetID := id.String()
	d.logAudit(r, "organization.delete", "organization", &targetID, nil)
	w.WriteHeader(http.StatusNoContent)
	return nil
}
