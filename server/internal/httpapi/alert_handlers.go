package httpapi

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/google/uuid"

	"healthbeat-server/internal/model"
	"healthbeat-server/internal/store"
)

// handleListAlerts, GET /api/v1/alerts'i sunar. ?q= subject/metrik/sunucu hostname'inde alt dize
// arar; ?limit=&offset= verilmezse (geriye dönük uyumlu) kapsamdaki tüm alert'ler döner. Toplam
// sayı X-Total-Count başlığındadır.
func (d *Deps) handleListAlerts(w http.ResponseWriter, r *http.Request) error {
	fail := failWith("alert'ler listelenemedi")
	status := r.URL.Query().Get("status")
	if status != "" && status != model.AlertStatusOpen && status != model.AlertStatusAcknowledged && status != model.AlertStatusResolved {
		return badRequest("status open, acknowledged veya resolved olmalı")
	}
	p, err := parseListParams(r)
	if err != nil {
		return badRequest(err.Error())
	}

	var (
		alerts []model.Alert
		total  int
	)
	// host_id verilmişse tek bir sunucuyla sınırla (sunucu detay sayfasındaki "Alert'ler"
	// sekmesi için) — erişim host.view ile aynı kuralla kontrol edilir.
	if raw := r.URL.Query().Get("host_id"); raw != "" {
		hostID, err := uuid.Parse(raw)
		if err != nil {
			return badRequest("geçersiz host_id")
		}
		host, err := d.hosts.GetByID(r.Context(), hostID)
		if errors.Is(err, store.ErrNotFound) {
			return notFound("sunucu bulunamadı")
		}
		if err != nil {
			return fail("list alerts: lookup host", err)
		}
		allowed, err := d.requireHostViewAccess(r, host)
		if err != nil {
			return fail("list alerts: check access", err)
		}
		if !allowed {
			return forbidden()
		}
		alerts, total, err = d.alerts.ListForHosts(r.Context(), status, []uuid.UUID{hostID}, p)
		if err != nil {
			return fail("list alerts", err)
		}
		w.Header().Set("X-Total-Count", strconv.Itoa(total))
		writeJSON(w, http.StatusOK, alerts)
		return nil
	}

	role, _ := roleFromContext(r.Context())
	userID, _ := userIDFromContext(r.Context())
	hostIDs, err := d.dashboardScope(r.Context(), role, userID)
	if err != nil {
		return fail("list alerts: resolve scope", err)
	}
	if hostIDs == nil { // super_admin: kapsamsız
		alerts, total, err = d.alerts.List(r.Context(), status, p)
	} else {
		alerts, total, err = d.alerts.ListForHosts(r.Context(), status, hostIDs, p)
	}
	if err != nil {
		return fail("list alerts", err)
	}
	w.Header().Set("X-Total-Count", strconv.Itoa(total))
	writeJSON(w, http.StatusOK, alerts)
	return nil
}

func (d *Deps) handleAcknowledgeAlert(w http.ResponseWriter, r *http.Request) error {
	fail := failWith("alert onaylanamadı")
	id, err := pathID(r, "geçersiz alert kimliği")
	if err != nil {
		return err
	}

	alert, err := d.alerts.GetByID(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		return notFound("alert bulunamadı")
	}
	if err != nil {
		return fail("acknowledge alert: lookup alert", err)
	}
	host, err := d.hosts.GetByID(r.Context(), alert.HostID)
	if err != nil {
		return fail("acknowledge alert: lookup host", err)
	}
	allowed, err := d.requireHostViewAccess(r, host)
	if err != nil {
		return fail("acknowledge alert: check access", err)
	}
	if !allowed {
		return forbidden()
	}

	userID, _ := userIDFromContext(r.Context())
	acknowledged, err := d.alerts.Acknowledge(r.Context(), id, userID)
	if errors.Is(err, store.ErrNotFound) {
		return conflict("alert açık değil")
	}
	if err != nil {
		return fail("acknowledge alert", err)
	}

	targetID := id.String()
	d.logAudit(r, "alert.acknowledge", "alert", &targetID, nil)
	writeJSON(w, http.StatusOK, acknowledged)
	return nil
}
