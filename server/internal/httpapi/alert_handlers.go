package httpapi

import (
	"errors"
	"log"
	"net/http"
	"strconv"

	"github.com/google/uuid"

	"healthbeat-server/internal/model"
	"healthbeat-server/internal/store"
)

// handleListAlerts, GET /api/v1/alerts'i sunar. ?q= subject/metrik/sunucu hostname'inde alt dize
// arar; ?limit=&offset= verilmezse (geriye dönük uyumlu) kapsamdaki tüm alert'ler döner. Toplam
// sayı X-Total-Count başlığındadır.
func (d *Deps) handleListAlerts(w http.ResponseWriter, r *http.Request) {
	status := r.URL.Query().Get("status")
	if status != "" && status != model.AlertStatusOpen && status != model.AlertStatusAcknowledged && status != model.AlertStatusResolved {
		writeError(w, http.StatusBadRequest, "status open, acknowledged veya resolved olmalı")
		return
	}
	p, err := parseListParams(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	// host_id verilmişse tek bir sunucuyla sınırla (sunucu detay sayfasındaki "Alert'ler"
	// sekmesi için) — erişim host.view ile aynı kuralla kontrol edilir.
	if raw := r.URL.Query().Get("host_id"); raw != "" {
		hostID, err := uuid.Parse(raw)
		if err != nil {
			writeError(w, http.StatusBadRequest, "geçersiz host_id")
			return
		}
		host, err := d.hosts.GetByID(r.Context(), hostID)
		if err != nil {
			if errors.Is(err, store.ErrNotFound) {
				writeError(w, http.StatusNotFound, "sunucu bulunamadı")
				return
			}
			log.Printf("list alerts: lookup host: %v", err)
			writeError(w, http.StatusInternalServerError, "alert'ler listelenemedi")
			return
		}
		allowed, err := d.requireHostViewAccess(r, host)
		if err != nil {
			log.Printf("list alerts: check access: %v", err)
			writeError(w, http.StatusInternalServerError, "alert'ler listelenemedi")
			return
		}
		if !allowed {
			writeError(w, http.StatusForbidden, "yetkiniz yok")
			return
		}
		alerts, total, err := d.alerts.ListForHosts(r.Context(), status, []uuid.UUID{hostID}, p)
		if err != nil {
			log.Printf("list alerts: %v", err)
			writeError(w, http.StatusInternalServerError, "alert'ler listelenemedi")
			return
		}
		w.Header().Set("X-Total-Count", strconv.Itoa(total))
		writeJSON(w, http.StatusOK, alerts)
		return
	}

	role, _ := roleFromContext(r.Context())
	userID, _ := userIDFromContext(r.Context())

	if role == model.RoleSuperAdmin {
		alerts, total, err := d.alerts.List(r.Context(), status, p)
		if err != nil {
			log.Printf("list alerts: %v", err)
			writeError(w, http.StatusInternalServerError, "alert'ler listelenemedi")
			return
		}
		w.Header().Set("X-Total-Count", strconv.Itoa(total))
		writeJSON(w, http.StatusOK, alerts)
		return
	}

	var hostIDs []uuid.UUID
	var err2 error
	switch role {
	case model.RoleOrgAdmin:
		var orgIDs []uuid.UUID
		orgIDs, err2 = d.userOrgs.ListOrganizationIDs(r.Context(), userID)
		if err2 == nil {
			hostIDs, err2 = d.hosts.ListIDsByOrganizations(r.Context(), orgIDs)
		}
	case model.RoleOperator:
		hostIDs, err2 = d.userHosts.ListHostIDs(r.Context(), userID)
	}
	if err2 != nil {
		log.Printf("list alerts: resolve scope: %v", err2)
		writeError(w, http.StatusInternalServerError, "alert'ler listelenemedi")
		return
	}

	alerts, total, err := d.alerts.ListForHosts(r.Context(), status, hostIDs, p)
	if err != nil {
		log.Printf("list alerts: %v", err)
		writeError(w, http.StatusInternalServerError, "alert'ler listelenemedi")
		return
	}
	w.Header().Set("X-Total-Count", strconv.Itoa(total))
	writeJSON(w, http.StatusOK, alerts)
}

func (d *Deps) handleAcknowledgeAlert(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "geçersiz alert kimliği")
		return
	}

	alert, err := d.alerts.GetByID(r.Context(), id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "alert bulunamadı")
			return
		}
		log.Printf("acknowledge alert: lookup alert: %v", err)
		writeError(w, http.StatusInternalServerError, "alert onaylanamadı")
		return
	}

	host, err := d.hosts.GetByID(r.Context(), alert.HostID)
	if err != nil {
		log.Printf("acknowledge alert: lookup host: %v", err)
		writeError(w, http.StatusInternalServerError, "alert onaylanamadı")
		return
	}
	allowed, err := d.requireHostViewAccess(r, host)
	if err != nil {
		log.Printf("acknowledge alert: check access: %v", err)
		writeError(w, http.StatusInternalServerError, "alert onaylanamadı")
		return
	}
	if !allowed {
		writeError(w, http.StatusForbidden, "yetkiniz yok")
		return
	}

	userID, _ := userIDFromContext(r.Context())
	acknowledged, err := d.alerts.Acknowledge(r.Context(), id, userID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusConflict, "alert açık değil")
			return
		}
		log.Printf("acknowledge alert: %v", err)
		writeError(w, http.StatusInternalServerError, "alert onaylanamadı")
		return
	}

	targetID := id.String()
	d.logAudit(r, "alert.acknowledge", "alert", &targetID, nil)
	writeJSON(w, http.StatusOK, acknowledged)
}
