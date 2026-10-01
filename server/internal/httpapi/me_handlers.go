package httpapi

import (
	"net/http"

	"healthbeat-server/internal/model"
	"healthbeat-server/internal/store"
)

// handleGetMe ve handleGetMyHosts yalnızca requireAuth ile korunan (izin denetimi yok)
// self-servis endpoint'lerdir: bir kullanıcı rolünden bağımsız olarak kendi kimliğini ve
// atamalarını her zaman görebilir. Bu en çok, hiçbir user.view/organization.view iznine
// sahip olmayan ve aksi halde hangi host'lara atandıklarını keşfetmenin yolu olmayan
// operatörler için önemlidir; bu endpoint'lere izin denetimi eklemek onları kendi host'larından koparır.
// meResponse, kullanıcıya rolünün izin anahtarlarını da verir (ör. "host.create"); panel bir düğmeyi göstermeden önce
// rol adına değil izne bakabilsin diye (bkz. docs/MIMARI.md bölüm 4). Anahtarlar sıralıdır.
type meResponse struct {
	model.User
	Permissions []string `json:"permissions"`
}

func (d *Deps) handleGetMe(w http.ResponseWriter, r *http.Request) error {
	fail := failWith("kullanıcı bilgisi alınamadı")
	userID, _ := userIDFromContext(r.Context())

	user, err := d.users.GetByID(r.Context(), userID)
	if err != nil {
		return fail("get me", err)
	}
	perms, err := d.perms.Permissions(r.Context(), user.Role)
	if err != nil {
		return fail("get me: permissions", err)
	}
	writeJSON(w, http.StatusOK, meResponse{User: user, Permissions: perms})
	return nil
}

// updateMeRequest: kişinin kendi profilinde değiştirebildiği alanlar. nil alan değişmez; boş metin alanı temizler.
// E-posta, rol ve şifre burada yoktur (bilinmeyen alan olarak reddedilir): e-posta ve rolü yalnızca bir yönetici,
// şifreyi POST /me/password değiştirir.
type updateMeRequest struct {
	FullName *string `json:"full_name"`
	Phone    *string `json:"phone"`
}

func (req *updateMeRequest) Validate() error {
	return validateProfile(req.FullName, req.Phone, nil)
}

// handleUpdateMe, PATCH /api/v1/me'yi sunar: oturumdaki kullanıcı kendi adını ve telefonunu günceller. Yanıt GET /me ile
// aynıdır (izinlerle birlikte), panel saklı kullanıcıyı doğrudan yeniler.
func (d *Deps) handleUpdateMe(w http.ResponseWriter, r *http.Request) error {
	fail := failWith("profil güncellenemedi")
	userID, _ := userIDFromContext(r.Context())
	req, err := bind[updateMeRequest](r)
	if err != nil {
		return err
	}

	user, err := d.users.UpdateProfile(r.Context(), userID, store.UserProfile{FullName: trimPtr(req.FullName), Phone: trimPtr(req.Phone)})
	if err != nil {
		return storeError(err, "kullanıcı bulunamadı", fail, "update me")
	}
	perms, err := d.perms.Permissions(r.Context(), user.Role)
	if err != nil {
		return fail("update me: permissions", err)
	}

	targetID := user.ID.String()
	d.logAudit(r, "user.update_self", "user", &targetID, map[string]any{"full_name_changed": req.FullName != nil, "phone_changed": req.Phone != nil})

	writeJSON(w, http.StatusOK, meResponse{User: user, Permissions: perms})
	return nil
}

func (d *Deps) handleGetMyHosts(w http.ResponseWriter, r *http.Request) error {
	fail := failWith("atanmış sunucular alınamadı")
	userID, _ := userIDFromContext(r.Context())

	ids, err := d.userHosts.ListHostIDs(r.Context(), userID)
	if err != nil {
		return fail("get my hosts: list ids", err)
	}

	hosts, err := d.hosts.ListByIDs(r.Context(), ids)
	if err != nil {
		return fail("get my hosts", err)
	}
	writeJSON(w, http.StatusOK, hosts)
	return nil
}
