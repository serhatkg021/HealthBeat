package httpapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/google/uuid"

	"healthbeat-server/internal/model"
	"healthbeat-server/internal/store"
)

func (d *Deps) handleListOrganizations(w http.ResponseWriter, r *http.Request) {
	role, _ := roleFromContext(r.Context())

	if role == model.RoleSuperAdmin {
		orgs, err := d.organizations.List(r.Context())
		if err != nil {
			slog.ErrorContext(r.Context(), "list organizations", "err", err)
			writeError(w, http.StatusInternalServerError, "organizasyonlar listelenemedi")
			return
		}
		for i := range orgs {
			orgs[i].Access = model.OrgAccessFull
		}
		writeJSON(w, http.StatusOK, orgs)
		return
	}

	// org_admin: atandığı organizasyonlar ve altındaki dallar tam erişimlidir; üst zincirleri yalnızca bağlam olarak
	// (ad ve konum) görünür, içerikleri (adres, sunucular) ve kardeş dalları görünmez.
	userID, _ := userIDFromContext(r.Context())
	fullIDs, err := d.userOrgs.ListOrganizationIDs(r.Context(), userID)
	if err != nil {
		slog.ErrorContext(r.Context(), "list user organizations", "err", err)
		writeError(w, http.StatusInternalServerError, "organizasyonlar listelenemedi")
		return
	}
	contextIDs, err := d.userOrgs.ListContextIDs(r.Context(), userID)
	if err != nil {
		slog.ErrorContext(r.Context(), "list user context organizations", "err", err)
		writeError(w, http.StatusInternalServerError, "organizasyonlar listelenemedi")
		return
	}
	orgs, err := d.organizations.ListByIDs(r.Context(), append(append([]uuid.UUID{}, fullIDs...), contextIDs...))
	if err != nil {
		slog.ErrorContext(r.Context(), "list organizations by ids", "err", err)
		writeError(w, http.StatusInternalServerError, "organizasyonlar listelenemedi")
		return
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
func validateOrgText(name string, address *string) string {
	if len(name) > maxOrgNameLen {
		return "ad çok uzun"
	}
	if address != nil && len(*address) > maxOrgAddressLen {
		return "adres çok uzun"
	}
	return ""
}

func (d *Deps) handleCreateOrganization(w http.ResponseWriter, r *http.Request) {
	var req createOrganizationRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "geçersiz istek gövdesi")
		return
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		writeError(w, http.StatusBadRequest, "ad zorunlu")
		return
	}

	if msg := validateOrgText(name, req.Address); msg != "" {
		writeError(w, http.StatusBadRequest, msg)
		return
	}
	var address *string
	if req.Address != nil {
		trimmed := strings.TrimSpace(*req.Address)
		address = &trimmed
	}

	org, err := d.organizations.Create(r.Context(), name, req.ParentOrganizationID, address)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, err.Error())
			return
		}
		if errors.Is(err, store.ErrConflict) {
			writeError(w, http.StatusConflict, err.Error())
			return
		}
		slog.ErrorContext(r.Context(), "create organization", "err", err)
		writeError(w, http.StatusInternalServerError, "organizasyon oluşturulamadı")
		return
	}

	targetID := org.ID.String()
	d.logAudit(r, "organization.create", "organization", &targetID, map[string]any{"name": org.Name, "parent_organization_id": org.ParentOrganizationID})
	writeJSON(w, http.StatusCreated, org)
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

func (d *Deps) handleGetOrganization(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "geçersiz organizasyon kimliği")
		return
	}

	access, err := d.orgAccessLevel(r, id)
	if err != nil {
		slog.ErrorContext(r.Context(), "check org access", "err", err)
		writeError(w, http.StatusInternalServerError, "organizasyon alınamadı")
		return
	}
	if access == "" {
		writeError(w, http.StatusForbidden, "yetkiniz yok")
		return
	}

	org, err := d.organizations.GetByID(r.Context(), id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "organizasyon bulunamadı")
			return
		}
		slog.ErrorContext(r.Context(), "get organization", "err", err)
		writeError(w, http.StatusInternalServerError, "organizasyon alınamadı")
		return
	}
	org.Access = access
	if access == model.OrgAccessContext {
		org.Address = nil // yalnızca üst zincir bilgisi: içerik görünmez
	}
	writeJSON(w, http.StatusOK, org)
}

type updateOrganizationRequest struct {
	Name    *string `json:"name"`
	Address *string `json:"address"`
	// ParentOrganizationID: verilmemiş = değişmez; null = kök yap; bir kimlik = o organizasyonun altına taşı.
	ParentOrganizationID json.RawMessage `json:"parent_organization_id"`
}

func (d *Deps) handleUpdateOrganization(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "geçersiz organizasyon kimliği")
		return
	}

	var req updateOrganizationRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "geçersiz istek gövdesi")
		return
	}
	patch := store.OrgPatch{}
	if req.Name != nil {
		name := strings.TrimSpace(*req.Name)
		if name == "" {
			writeError(w, http.StatusBadRequest, "ad boş olamaz")
			return
		}
		patch.Name = &name
	}
	if req.Address != nil {
		trimmed := strings.TrimSpace(*req.Address)
		patch.Address = &trimmed
	}
	name := ""
	if patch.Name != nil {
		name = *patch.Name
	}
	if msg := validateOrgText(name, patch.Address); msg != "" {
		writeError(w, http.StatusBadRequest, msg)
		return
	}
	if raw := bytes.TrimSpace(req.ParentOrganizationID); len(raw) > 0 {
		patch.ParentSet = true
		if string(raw) != "null" {
			var parent uuid.UUID
			if err := json.Unmarshal(raw, &parent); err != nil {
				writeError(w, http.StatusBadRequest, "parent_organization_id geçerli bir kimlik ya da null olmalı")
				return
			}
			patch.Parent = &parent
		}
	}

	org, err := d.organizations.Update(r.Context(), id, patch)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "organizasyon bulunamadı")
			return
		}
		if errors.Is(err, store.ErrConflict) {
			writeError(w, http.StatusConflict, err.Error())
			return
		}
		slog.ErrorContext(r.Context(), "update organization", "err", err)
		writeError(w, http.StatusInternalServerError, "organizasyon güncellenemedi")
		return
	}

	targetID := org.ID.String()
	d.logAudit(r, "organization.update", "organization", &targetID, map[string]any{"name": org.Name, "parent_organization_id": org.ParentOrganizationID})
	org.Access = model.OrgAccessFull
	writeJSON(w, http.StatusOK, org)
}

func (d *Deps) handleDeleteOrganization(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "geçersiz organizasyon kimliği")
		return
	}

	if err := d.organizations.Delete(r.Context(), id); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "organizasyon bulunamadı")
			return
		}
		if errors.Is(err, store.ErrConflict) {
			writeError(w, http.StatusConflict, err.Error())
			return
		}
		slog.ErrorContext(r.Context(), "delete organization", "err", err)
		writeError(w, http.StatusInternalServerError, "organizasyon silinemedi")
		return
	}

	targetID := id.String()
	d.logAudit(r, "organization.delete", "organization", &targetID, nil)
	w.WriteHeader(http.StatusNoContent)
}
