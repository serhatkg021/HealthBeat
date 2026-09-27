package httpapi

import (
	"net/http"

	"healthbeat-server/internal/model"
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
