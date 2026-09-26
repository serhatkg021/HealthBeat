package httpapi

import (
	"errors"
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
func (d *Deps) handleListUsers(w http.ResponseWriter, r *http.Request) error {
	p, err := parseListParams(r)
	if err != nil {
		return badRequest(err.Error())
	}

	users, total, err := d.users.List(r.Context(), p)
	if err != nil {
		return serverErr("kullanıcılar listelenemedi", "list users", err)
	}
	for i := range users {
		users[i].PasswordHash = ""
	}
	w.Header().Set("X-Total-Count", strconv.Itoa(total))
	writeJSON(w, http.StatusOK, users)
	return nil
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

// Validate, e-postayı normalleştirir (req.Email).
func (req *createUserRequest) Validate() error {
	if strings.TrimSpace(req.Email) == "" || req.Password == "" {
		return errors.New("e-posta ve şifre zorunlu")
	}
	email, err := model.NormalizeEmail(req.Email)
	if err != nil {
		return err
	}
	req.Email = email
	if err := model.ValidatePassword(req.Password); err != nil {
		return err
	}
	if !model.ValidRole(req.Role) {
		return errors.New("rol super_admin, org_admin veya operator olmalı")
	}
	return validateProfile(req.FullName, req.Phone, nil)
}

func (d *Deps) handleCreateUser(w http.ResponseWriter, r *http.Request) error {
	fail := failWith("kullanıcı oluşturulamadı")
	req, err := bind[createUserRequest](r)
	if err != nil {
		return err
	}

	passwordHash, err := authsvc.HashPassword(req.Password)
	if err != nil {
		return fail("create user: hash password", err)
	}

	user, err := d.users.Create(r.Context(), req.Email, passwordHash, req.Role, trimPtr(req.FullName), trimPtr(req.Phone))
	if errors.Is(err, store.ErrConflict) {
		return conflict(err.Error())
	}
	if err != nil {
		return fail("create user", err)
	}

	targetID := user.ID.String()
	d.logAudit(r, "user.create", "user", &targetID, map[string]any{"email": user.Email, "role": user.Role})

	user.PasswordHash = ""
	writeJSON(w, http.StatusCreated, user)
	return nil
}

func (d *Deps) handleGetUser(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "geçersiz kullanıcı kimliği")
	if err != nil {
		return err
	}
	user, err := d.users.GetByID(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		return notFound("kullanıcı bulunamadı")
	}
	if err != nil {
		return serverErr("kullanıcı alınamadı", "get user", err)
	}
	user.PasswordHash = ""
	writeJSON(w, http.StatusOK, user)
	return nil
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

// Validate, e-postayı normalleştirir (req.Email).
func (req *updateUserRequest) Validate() error {
	if req.Email != nil {
		normalized, err := model.NormalizeEmail(*req.Email)
		if err != nil {
			return err
		}
		req.Email = &normalized
	}
	if req.Role != nil && !model.ValidRole(*req.Role) {
		return errors.New("rol super_admin, org_admin veya operator olmalı")
	}
	if req.Password != nil {
		if err := model.ValidatePassword(*req.Password); err != nil {
			return err
		}
	}
	return validateProfile(req.FullName, req.Phone, req.TwoFactorChannel)
}

func (d *Deps) handleUpdateUser(w http.ResponseWriter, r *http.Request) error {
	fail := failWith("kullanıcı güncellenemedi")
	id, err := pathID(r, "geçersiz kullanıcı kimliği")
	if err != nil {
		return err
	}
	req, err := bind[updateUserRequest](r)
	if err != nil {
		return err
	}

	user, err := d.users.Update(r.Context(), id, req.Email, req.Role)
	switch {
	case errors.Is(err, store.ErrNotFound):
		return notFound("kullanıcı bulunamadı")
	case errors.Is(err, store.ErrConflict):
		return conflict(err.Error())
	case err != nil:
		return fail("update user", err)
	}

	if req.FullName != nil || req.Phone != nil || req.TwoFactorEnabled != nil || req.TwoFactorChannel != nil {
		user, err = d.users.UpdateProfile(r.Context(), id, store.UserProfile{
			FullName: trimPtr(req.FullName), Phone: trimPtr(req.Phone),
			TwoFactorEnabled: req.TwoFactorEnabled, TwoFactorChannel: req.TwoFactorChannel,
		})
		if errors.Is(err, store.ErrConflict) {
			return conflict(err.Error())
		}
		if err != nil {
			return fail("update user: profile", err)
		}
	}

	if req.Password != nil {
		hash, err := authsvc.HashPassword(*req.Password)
		if err != nil {
			return fail("update user: hash password", err)
		}
		if err := d.users.UpdatePassword(r.Context(), id, hash); err != nil {
			return fail("update user: update password", err)
		}
		// Şifre değişikliği mevcut oturumları sonlandırmalı — genellikle bu yüzden değiştirilir.
		// (Zaten verilmiş access token'lar kısa TTL'lerini doldurur; tek tek iptal edilemezler.)
		if err := d.refreshTokens.RevokeAllForUser(r.Context(), id); err != nil {
			return serverErr("şifre değişti ancak mevcut oturumlar sonlandırılamadı", "update user: revoke sessions", err)
		}
	}

	targetID := user.ID.String()
	d.logAudit(r, "user.update", "user", &targetID, map[string]any{"email": user.Email, "role": user.Role, "password_changed": req.Password != nil})

	user.PasswordHash = ""
	writeJSON(w, http.StatusOK, user)
	return nil
}

func (d *Deps) handleDeleteUser(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "geçersiz kullanıcı kimliği")
	if err != nil {
		return err
	}

	if callerID, ok := userIDFromContext(r.Context()); ok && callerID == id {
		return badRequest("kendi hesabınızı silemezsiniz")
	}

	err = d.users.Delete(r.Context(), id)
	switch {
	case errors.Is(err, store.ErrNotFound):
		return notFound("kullanıcı bulunamadı")
	case errors.Is(err, store.ErrConflict):
		return conflict(err.Error())
	case err != nil:
		return serverErr("kullanıcı silinemedi", "delete user", err)
	}

	targetID := id.String()
	d.logAudit(r, "user.delete", "user", &targetID, nil)
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (d *Deps) handleGetUserOrganizations(w http.ResponseWriter, r *http.Request) error {
	fail := failWith("atamalar alınamadı")
	id, err := pathID(r, "geçersiz kullanıcı kimliği")
	if err != nil {
		return err
	}
	ids, err := d.userOrgs.ListOrganizationIDs(r.Context(), id)
	if err != nil {
		return fail("list user organizations", err)
	}
	orgs, err := d.organizations.ListByIDs(r.Context(), ids)
	if err != nil {
		return fail("list organizations by ids", err)
	}
	writeJSON(w, http.StatusOK, orgs)
	return nil
}

type setOrganizationsRequest struct {
	OrganizationIDs []uuid.UUID `json:"organization_ids"`
}

func (d *Deps) handleSetUserOrganizations(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "geçersiz kullanıcı kimliği")
	if err != nil {
		return err
	}
	req, err := bind[setOrganizationsRequest](r)
	if err != nil {
		return err
	}

	err = d.userOrgs.Set(r.Context(), id, req.OrganizationIDs)
	if errors.Is(err, store.ErrNotFound) {
		return badRequest("kullanıcı ya da organizasyonlardan biri yok")
	}
	if err != nil {
		return serverErr("atamalar güncellenemedi", "set user organizations", err)
	}

	targetID := id.String()
	d.logAudit(r, "user.assign_organizations", "user", &targetID, map[string]any{"organization_ids": req.OrganizationIDs})
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (d *Deps) handleGetUserHosts(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "geçersiz kullanıcı kimliği")
	if err != nil {
		return err
	}
	ids, err := d.userHosts.ListHostIDs(r.Context(), id)
	if err != nil {
		return serverErr("atamalar alınamadı", "list user hosts", err)
	}
	writeJSON(w, http.StatusOK, map[string]any{"host_ids": ids})
	return nil
}

type setHostsRequest struct {
	HostIDs []uuid.UUID `json:"host_ids"`
}

// handleSetUserHosts, kullanıcının tüm host atamasını değiştirir ("sunucuları
// tek tek seç" — bkz. docs/MIMARI.md bölüm 4).
func (d *Deps) handleSetUserHosts(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "geçersiz kullanıcı kimliği")
	if err != nil {
		return err
	}
	req, err := bind[setHostsRequest](r)
	if err != nil {
		return err
	}

	err = d.userHosts.Set(r.Context(), id, req.HostIDs)
	if errors.Is(err, store.ErrNotFound) {
		return badRequest("kullanıcı ya da sunuculardan biri yok")
	}
	if err != nil {
		return serverErr("atamalar güncellenemedi", "set user hosts", err)
	}

	targetID := id.String()
	d.logAudit(r, "user.assign_hosts", "user", &targetID, map[string]any{"host_ids": req.HostIDs})
	w.WriteHeader(http.StatusNoContent)
	return nil
}

type assignHostsByOrgRequest struct {
	OrganizationID uuid.UUID `json:"organization_id"`
}

func (req *assignHostsByOrgRequest) Validate() error {
	if req.OrganizationID == uuid.Nil {
		return errors.New("organization_id zorunlu")
	}
	return nil
}

// handleAddUserHostsByOrganization, verilen organizasyondaki her host'ı kullanıcının
// atamasına, mevcut atamalara dokunmadan ekler ("organizasyondaki tüm sunucuları ekle" —
// bkz. bölüm 4).
func (d *Deps) handleAddUserHostsByOrganization(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "geçersiz kullanıcı kimliği")
	if err != nil {
		return err
	}
	req, err := bind[assignHostsByOrgRequest](r)
	if err != nil {
		return badRequest("organization_id zorunlu") // çözülemeyen gövde de aynı yanıtı alır
	}

	added, err := d.userHosts.AddByOrganization(r.Context(), id, req.OrganizationID)
	if err != nil {
		return serverErr("atamalar güncellenemedi", "add user hosts by organization", err)
	}

	targetID := id.String()
	d.logAudit(r, "user.assign_hosts_by_organization", "user", &targetID, map[string]any{
		"organization_id": req.OrganizationID,
		"hosts_added":     added,
	})
	writeJSON(w, http.StatusOK, map[string]any{"hosts_added": added})
	return nil
}
