package httpapi

import (
	"log"
	"net/http"
)

// handleGetMe ve handleGetMyHosts yalnızca requireAuth ile korunan (izin denetimi yok)
// self-servis endpoint'lerdir: bir kullanıcı rolünden bağımsız olarak kendi kimliğini ve
// atamalarını her zaman görebilir. Bu en çok, hiçbir user.view/organization.view iznine
// sahip olmayan ve aksi halde hangi host'lara atandıklarını keşfetmenin yolu olmayan
// operatörler için önemlidir; bu endpoint'lere izin denetimi eklemek onları kendi host'larından koparır.
func (d *Deps) handleGetMe(w http.ResponseWriter, r *http.Request) {
	userID, _ := userIDFromContext(r.Context())

	user, err := d.users.GetByID(r.Context(), userID)
	if err != nil {
		log.Printf("get me: %v", err)
		writeError(w, http.StatusInternalServerError, "kullanıcı bilgisi alınamadı")
		return
	}
	user.PasswordHash = ""
	writeJSON(w, http.StatusOK, user)
}

func (d *Deps) handleGetMyHosts(w http.ResponseWriter, r *http.Request) {
	userID, _ := userIDFromContext(r.Context())

	ids, err := d.userHosts.ListHostIDs(r.Context(), userID)
	if err != nil {
		log.Printf("get my hosts: list ids: %v", err)
		writeError(w, http.StatusInternalServerError, "atanmış sunucular alınamadı")
		return
	}

	hosts, err := d.hosts.ListByIDs(r.Context(), ids)
	if err != nil {
		log.Printf("get my hosts: %v", err)
		writeError(w, http.StatusInternalServerError, "atanmış sunucular alınamadı")
		return
	}
	writeJSON(w, http.StatusOK, hosts)
}
