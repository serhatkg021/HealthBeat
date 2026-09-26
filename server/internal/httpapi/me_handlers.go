package httpapi

import "net/http"

// handleGetMe ve handleGetMyHosts yalnızca requireAuth ile korunan (izin denetimi yok)
// self-servis endpoint'lerdir: bir kullanıcı rolünden bağımsız olarak kendi kimliğini ve
// atamalarını her zaman görebilir. Bu en çok, hiçbir user.view/organization.view iznine
// sahip olmayan ve aksi halde hangi host'lara atandıklarını keşfetmenin yolu olmayan
// operatörler için önemlidir; bu endpoint'lere izin denetimi eklemek onları kendi host'larından koparır.
func (d *Deps) handleGetMe(w http.ResponseWriter, r *http.Request) error {
	userID, _ := userIDFromContext(r.Context())

	user, err := d.users.GetByID(r.Context(), userID)
	if err != nil {
		return serverErr("kullanıcı bilgisi alınamadı", "get me", err)
	}
	user.PasswordHash = ""
	writeJSON(w, http.StatusOK, user)
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
