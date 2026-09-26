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

func (d *Deps) handleListOrganizations(w http.ResponseWriter, r *http.Request) error {
	fail := failWith("organizasyonlar listelenemedi")
	role, _ := roleFromContext(r.Context())

	if role == model.RoleSuperAdmin {
		orgs, err := d.organizations.List(r.Context())
		if err != nil {
			return fail("list organizations", err)
		}
		for i := range orgs {
			orgs[i].Access = model.OrgAccessFull
		}
		writeJSON(w, http.StatusOK, orgs)
		return nil
	}

	// org_admin: atandığı organizasyonlar ve altındaki dallar tam erişimlidir; üst zincirleri yalnızca bağlam olarak
	// (ad ve konum) görünür, içerikleri (adres, sunucular) ve kardeş dalları görünmez.
	userID, _ := userIDFromContext(r.Context())
	fullIDs, err := d.userOrgs.ListOrganizationIDs(r.Context(), userID)
	if err != nil {
		return fail("list user organizations", err)
	}
	contextIDs, err := d.userOrgs.ListContextIDs(r.Context(), userID)
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
	for i := range orgs {
		if _, ok := full[orgs[i].ID]; ok {
			orgs[i].Access = model.OrgAccessFull
			continue
		}
		orgs[i].Access = model.OrgAccessContext
		orgs[i].Address = nil
	}
	writeJSON(w, http.StatusOK, orgs)
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
	switch {
	case errors.Is(err, store.ErrNotFound):
		return notFound(err.Error())
	case errors.Is(err, store.ErrConflict):
		return conflict(err.Error())
	case err != nil:
		return serverErr("organizasyon oluşturulamadı", "create organization", err)
	}

	targetID := org.ID.String()
	d.logAudit(r, "organization.create", "organization", &targetID, map[string]any{"name": org.Name, "parent_organization_id": org.ParentOrganizationID})
	writeJSON(w, http.StatusCreated, org)
	return nil
}

// requireOrgAccess, çağıranın orgID üzerinde işlem yapabileceğini denetler: super_admin her
// zaman yapabilir, org_admin yalnızca orgID atamalarındaysa.
func (d *Deps) requireOrgAccess(r *http.Request, orgID uuid.UUID) (bool, error) {
	role, _ := roleFromContext(r.Context())
	if role == model.RoleSuperAdmin {
		return true, nil
	}
	userID, _ := userIDFromContext(r.Context())
	return d.userOrgs.IsAssigned(r.Context(), userID, orgID)
}

// orgAccessLevel, çağıranın orgID'deki erişimini söyler: "full", yalnızca üst zincir bilgisi olarak "context" ya da "".
func (d *Deps) orgAccessLevel(r *http.Request, orgID uuid.UUID) (string, error) {
	full, err := d.requireOrgAccess(r, orgID)
	if err != nil || full {
		if full {
			return model.OrgAccessFull, nil
		}
		return "", err
	}
	userID, _ := userIDFromContext(r.Context())
	ids, err := d.userOrgs.ListContextIDs(r.Context(), userID)
	if err != nil {
		return "", err
	}
	for _, id := range ids {
		if id == orgID {
			return model.OrgAccessContext, nil
		}
	}
	return "", nil
}

func (d *Deps) handleGetOrganization(w http.ResponseWriter, r *http.Request) error {
	fail := failWith("organizasyon alınamadı")
	id, err := pathID(r, "geçersiz organizasyon kimliği")
	if err != nil {
		return err
	}

	access, err := d.orgAccessLevel(r, id)
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
	org.Access = access
	if access == model.OrgAccessContext {
		org.Address = nil // yalnızca üst zincir bilgisi: içerik görünmez
	}
	writeJSON(w, http.StatusOK, org)
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
	switch {
	case errors.Is(err, store.ErrNotFound):
		return notFound("organizasyon bulunamadı")
	case errors.Is(err, store.ErrConflict):
		return conflict(err.Error())
	case err != nil:
		return serverErr("organizasyon güncellenemedi", "update organization", err)
	}

	targetID := org.ID.String()
	d.logAudit(r, "organization.update", "organization", &targetID, map[string]any{"name": org.Name, "parent_organization_id": org.ParentOrganizationID})
	org.Access = model.OrgAccessFull
	writeJSON(w, http.StatusOK, org)
	return nil
}

func (d *Deps) handleDeleteOrganization(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "geçersiz organizasyon kimliği")
	if err != nil {
		return err
	}

	err = d.organizations.Delete(r.Context(), id)
	switch {
	case errors.Is(err, store.ErrNotFound):
		return notFound("organizasyon bulunamadı")
	case errors.Is(err, store.ErrConflict):
		return conflict(err.Error())
	case err != nil:
		return serverErr("organizasyon silinemedi", "delete organization", err)
	}

	targetID := id.String()
	d.logAudit(r, "organization.delete", "organization", &targetID, nil)
	w.WriteHeader(http.StatusNoContent)
	return nil
}
