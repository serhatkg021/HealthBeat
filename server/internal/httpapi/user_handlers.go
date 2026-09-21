package httpapi

import (
	"errors"
	"log"
	"net/http"
	"strconv"
	"strings"

	"github.com/google/uuid"

	"healthbeat-server/internal/authsvc"
	"healthbeat-server/internal/model"
	"healthbeat-server/internal/store"
)

// handleListUsers, GET /api/v1/users'ı sunar. ?q= e-postada alt dize arar; ?limit=&offset=
// verilmezse (mevcut çağıranlarla geriye dönük uyumlu) tüm kullanıcılar döner. Toplam sayı,
// sayfa numaralı bir arayüz kurabilsin diye X-Total-Count başlığında gelir (gövde her zaman
// düz bir dizidir).
func (d *Deps) handleListUsers(w http.ResponseWriter, r *http.Request) {
	p, err := parseListParams(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	users, total, err := d.users.List(r.Context(), p)
	if err != nil {
		log.Printf("list users: %v", err)
		writeError(w, http.StatusInternalServerError, "kullanıcılar listelenemedi")
		return
	}
	for i := range users {
		users[i].PasswordHash = ""
	}
	w.Header().Set("X-Total-Count", strconv.Itoa(total))
	writeJSON(w, http.StatusOK, users)
}

type createUserRequest struct {
	Email    string  `json:"email"`
	Password string  `json:"password"`
	Role     string  `json:"role"`
	FullName *string `json:"full_name"`
	Phone    *string `json:"phone"`
}

const (
	maxFullNameLen = 200
	maxPhoneLen    = 32
)

// validatePhone, telefonun makul bir biçimde olduğunu denetler (rakam, boşluk, + - ( ) ve . ; boş = yok).
func validatePhone(phone string) error {
	if len(phone) > maxPhoneLen {
		return errors.New("telefon çok uzun")
	}
	for _, r := range phone {
		if !(r >= '0' && r <= '9') && !strings.ContainsRune(" +-().", r) {
			return errors.New("telefon yalnızca rakam, boşluk ve + - ( ) . içerebilir")
		}
	}
	return nil
}

// validateProfile, ad/telefon/2FA alanlarını denetler; nil alan atlanır.
func validateProfile(fullName, phone, channel *string) error {
	if fullName != nil && len(strings.TrimSpace(*fullName)) > maxFullNameLen {
		return errors.New("ad çok uzun")
	}
	if phone != nil {
		if err := validatePhone(strings.TrimSpace(*phone)); err != nil {
			return err
		}
	}
	if channel != nil && *channel != "" && *channel != "email" && *channel != "sms" && *channel != "app" {
		return errors.New("two_factor_channel email, sms veya app olmalı")
	}
	return nil
}

func trimPtr(s *string) *string {
	if s == nil {
		return nil
	}
	t := strings.TrimSpace(*s)
	return &t
}

func (d *Deps) handleCreateUser(w http.ResponseWriter, r *http.Request) {
	var req createUserRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "geçersiz istek gövdesi")
		return
	}
	if strings.TrimSpace(req.Email) == "" || req.Password == "" {
		writeError(w, http.StatusBadRequest, "e-posta ve şifre zorunlu")
		return
	}
	email, err := model.NormalizeEmail(req.Email)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := model.ValidatePassword(req.Password); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if !model.ValidRole(req.Role) {
		writeError(w, http.StatusBadRequest, "rol super_admin, org_admin veya operator olmalı")
		return
	}

	if err := validateProfile(req.FullName, req.Phone, nil); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	passwordHash, err := authsvc.HashPassword(req.Password)
	if err != nil {
		log.Printf("create user: hash password: %v", err)
		writeError(w, http.StatusInternalServerError, "kullanıcı oluşturulamadı")
		return
	}

	user, err := d.users.Create(r.Context(), email, passwordHash, req.Role, trimPtr(req.FullName), trimPtr(req.Phone))
	if err != nil {
		if errors.Is(err, store.ErrConflict) {
			writeError(w, http.StatusConflict, err.Error())
			return
		}
		log.Printf("create user: %v", err)
		writeError(w, http.StatusInternalServerError, "kullanıcı oluşturulamadı")
		return
	}

	targetID := user.ID.String()
	d.logAudit(r, "user.create", "user", &targetID, map[string]any{"email": user.Email, "role": user.Role})

	user.PasswordHash = ""
	writeJSON(w, http.StatusCreated, user)
}

func (d *Deps) handleGetUser(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "geçersiz kullanıcı kimliği")
		return
	}
	user, err := d.users.GetByID(r.Context(), id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "kullanıcı bulunamadı")
			return
		}
		log.Printf("get user: %v", err)
		writeError(w, http.StatusInternalServerError, "kullanıcı alınamadı")
		return
	}
	user.PasswordHash = ""
	writeJSON(w, http.StatusOK, user)
}

type updateUserRequest struct {
	Email    *string `json:"email"`
	Role     *string `json:"role"`
	Password *string `json:"password"`
	// Profil: nil alan değişmez; boş metin alanı temizler. İki faktörlü doğrulama tercihi şimdilik yalnızca saklanır.
	FullName         *string `json:"full_name"`
	Phone            *string `json:"phone"`
	TwoFactorEnabled *bool   `json:"two_factor_enabled"`
	TwoFactorChannel *string `json:"two_factor_channel"`
}

func (d *Deps) handleUpdateUser(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "geçersiz kullanıcı kimliği")
		return
	}

	var req updateUserRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "geçersiz istek gövdesi")
		return
	}
	if req.Email != nil {
		normalized, err := model.NormalizeEmail(*req.Email)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		req.Email = &normalized
	}
	if req.Role != nil && !model.ValidRole(*req.Role) {
		writeError(w, http.StatusBadRequest, "rol super_admin, org_admin veya operator olmalı")
		return
	}
	if req.Password != nil {
		if err := model.ValidatePassword(*req.Password); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
	}
	if err := validateProfile(req.FullName, req.Phone, req.TwoFactorChannel); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	user, err := d.users.Update(r.Context(), id, req.Email, req.Role)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "kullanıcı bulunamadı")
			return
		}
		if errors.Is(err, store.ErrConflict) {
			writeError(w, http.StatusConflict, err.Error())
			return
		}
		log.Printf("update user: %v", err)
		writeError(w, http.StatusInternalServerError, "kullanıcı güncellenemedi")
		return
	}

	if req.FullName != nil || req.Phone != nil || req.TwoFactorEnabled != nil || req.TwoFactorChannel != nil {
		user, err = d.users.UpdateProfile(r.Context(), id, store.UserProfile{
			FullName: trimPtr(req.FullName), Phone: trimPtr(req.Phone),
			TwoFactorEnabled: req.TwoFactorEnabled, TwoFactorChannel: req.TwoFactorChannel,
		})
		if err != nil {
			if errors.Is(err, store.ErrConflict) {
				writeError(w, http.StatusConflict, err.Error())
				return
			}
			log.Printf("update user: profile: %v", err)
			writeError(w, http.StatusInternalServerError, "kullanıcı güncellenemedi")
			return
		}
	}

	if req.Password != nil {
		hash, err := authsvc.HashPassword(*req.Password)
		if err != nil {
			log.Printf("update user: hash password: %v", err)
			writeError(w, http.StatusInternalServerError, "kullanıcı güncellenemedi")
			return
		}
		if err := d.users.UpdatePassword(r.Context(), id, hash); err != nil {
			log.Printf("update user: update password: %v", err)
			writeError(w, http.StatusInternalServerError, "kullanıcı güncellenemedi")
			return
		}
		// Şifre değişikliği mevcut oturumları sonlandırmalı — genellikle bu yüzden değiştirilir.
		// (Zaten verilmiş access token'lar kısa TTL'lerini doldurur; tek tek iptal edilemezler.)
		if err := d.refreshTokens.RevokeAllForUser(r.Context(), id); err != nil {
			log.Printf("update user: revoke sessions: %v", err)
			writeError(w, http.StatusInternalServerError, "şifre değişti ancak mevcut oturumlar sonlandırılamadı")
			return
		}
	}

	targetID := user.ID.String()
	d.logAudit(r, "user.update", "user", &targetID, map[string]any{"email": user.Email, "role": user.Role, "password_changed": req.Password != nil})

	user.PasswordHash = ""
	writeJSON(w, http.StatusOK, user)
}

func (d *Deps) handleDeleteUser(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "geçersiz kullanıcı kimliği")
		return
	}

	if callerID, ok := userIDFromContext(r.Context()); ok && callerID == id {
		writeError(w, http.StatusBadRequest, "kendi hesabınızı silemezsiniz")
		return
	}

	if err := d.users.Delete(r.Context(), id); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "kullanıcı bulunamadı")
			return
		}
		if errors.Is(err, store.ErrConflict) {
			writeError(w, http.StatusConflict, err.Error())
			return
		}
		log.Printf("delete user: %v", err)
		writeError(w, http.StatusInternalServerError, "kullanıcı silinemedi")
		return
	}

	targetID := id.String()
	d.logAudit(r, "user.delete", "user", &targetID, nil)
	w.WriteHeader(http.StatusNoContent)
}

func (d *Deps) handleGetUserOrganizations(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "geçersiz kullanıcı kimliği")
		return
	}
	ids, err := d.userOrgs.ListOrganizationIDs(r.Context(), id)
	if err != nil {
		log.Printf("list user organizations: %v", err)
		writeError(w, http.StatusInternalServerError, "atamalar alınamadı")
		return
	}
	orgs, err := d.organizations.ListByIDs(r.Context(), ids)
	if err != nil {
		log.Printf("list organizations by ids: %v", err)
		writeError(w, http.StatusInternalServerError, "atamalar alınamadı")
		return
	}
	writeJSON(w, http.StatusOK, orgs)
}

type setOrganizationsRequest struct {
	OrganizationIDs []uuid.UUID `json:"organization_ids"`
}

func (d *Deps) handleSetUserOrganizations(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "geçersiz kullanıcı kimliği")
		return
	}

	var req setOrganizationsRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "geçersiz istek gövdesi")
		return
	}

	if err := d.userOrgs.Set(r.Context(), id, req.OrganizationIDs); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusBadRequest, "kullanıcı ya da organizasyonlardan biri yok")
			return
		}
		log.Printf("set user organizations: %v", err)
		writeError(w, http.StatusInternalServerError, "atamalar güncellenemedi")
		return
	}

	targetID := id.String()
	d.logAudit(r, "user.assign_organizations", "user", &targetID, map[string]any{"organization_ids": req.OrganizationIDs})
	w.WriteHeader(http.StatusNoContent)
}

func (d *Deps) handleGetUserHosts(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "geçersiz kullanıcı kimliği")
		return
	}
	ids, err := d.userHosts.ListHostIDs(r.Context(), id)
	if err != nil {
		log.Printf("list user hosts: %v", err)
		writeError(w, http.StatusInternalServerError, "atamalar alınamadı")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"host_ids": ids})
}

type setHostsRequest struct {
	HostIDs []uuid.UUID `json:"host_ids"`
}

// handleSetUserHosts, kullanıcının tüm host atamasını değiştirir ("sunucuları
// tek tek seç" — bkz. docs/MIMARI.md bölüm 4).
func (d *Deps) handleSetUserHosts(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "geçersiz kullanıcı kimliği")
		return
	}

	var req setHostsRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "geçersiz istek gövdesi")
		return
	}

	if err := d.userHosts.Set(r.Context(), id, req.HostIDs); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusBadRequest, "kullanıcı ya da sunuculardan biri yok")
			return
		}
		log.Printf("set user hosts: %v", err)
		writeError(w, http.StatusInternalServerError, "atamalar güncellenemedi")
		return
	}

	targetID := id.String()
	d.logAudit(r, "user.assign_hosts", "user", &targetID, map[string]any{"host_ids": req.HostIDs})
	w.WriteHeader(http.StatusNoContent)
}

type assignHostsByOrgRequest struct {
	OrganizationID uuid.UUID `json:"organization_id"`
}

// handleAddUserHostsByOrganization, verilen organizasyondaki her host'ı kullanıcının
// atamasına, mevcut atamalara dokunmadan ekler ("organizasyondaki tüm sunucuları ekle" —
// bkz. bölüm 4).
func (d *Deps) handleAddUserHostsByOrganization(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "geçersiz kullanıcı kimliği")
		return
	}

	var req assignHostsByOrgRequest
	if err := decodeJSON(r, &req); err != nil || req.OrganizationID == uuid.Nil {
		writeError(w, http.StatusBadRequest, "organization_id zorunlu")
		return
	}

	added, err := d.userHosts.AddByOrganization(r.Context(), id, req.OrganizationID)
	if err != nil {
		log.Printf("add user hosts by organization: %v", err)
		writeError(w, http.StatusInternalServerError, "atamalar güncellenemedi")
		return
	}

	targetID := id.String()
	d.logAudit(r, "user.assign_hosts_by_organization", "user", &targetID, map[string]any{
		"organization_id": req.OrganizationID,
		"hosts_added":     added,
	})
	writeJSON(w, http.StatusOK, map[string]any{"hosts_added": added})
}
