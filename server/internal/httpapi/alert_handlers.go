package httpapi

import (
	"errors"
	"net/http"
	"strconv"
	"time"

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
		allowed, err := d.scope(r).CanViewHost(r.Context(), host)
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

	hostIDs, err := d.scope(r).VisibleHostIDs(r.Context())
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
	allowed, err := d.scope(r).CanViewHost(r.Context(), host)
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

// alertNotificationResponse, alert'in bir bildiriminin teslim kaydıdır. Alıcılar ve son hata (SMTP sunucusunun
// ayrıntısını içerebilir) yalnızca notification.view izni olanlara döner; diğerleri alıcı sayısını görür.
type alertNotificationResponse struct {
	ID             uuid.UUID  `json:"id"`
	Event          string     `json:"event"`
	Level          string     `json:"level"`
	Channel        string     `json:"channel"`
	Status         string     `json:"status"`
	Attempts       int        `json:"attempts"`
	RecipientCount int        `json:"recipient_count"`
	Recipients     []string   `json:"recipients,omitempty"`
	LastError      string     `json:"last_error,omitempty"`
	Subject        string     `json:"subject"`
	Body           string     `json:"body"`
	CreatedAt      time.Time  `json:"created_at"`
	SentAt         *time.Time `json:"sent_at,omitempty"`
	FailedAt       *time.Time `json:"failed_at,omitempty"`
	NextAttemptAt  *time.Time `json:"next_attempt_at,omitempty"`
}

// handleListAlertNotifications, GET /api/v1/alerts/:id/notifications'ı sunar: alert'in bildirimleri (açılma, seviye
// değişimi, çözülme; kanal başına bir kayıt) oluşturulma sırasıyla.
func (d *Deps) handleListAlertNotifications(w http.ResponseWriter, r *http.Request) error {
	fail := failWith("bildirimler alınamadı")
	id, err := pathID(r, "geçersiz alert kimliği")
	if err != nil {
		return err
	}
	alert, err := d.alerts.GetByID(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		return notFound("alert bulunamadı")
	}
	if err != nil {
		return fail("list alert notifications: lookup alert", err)
	}
	host, err := d.hosts.GetByID(r.Context(), alert.HostID)
	if err != nil {
		return fail("list alert notifications: lookup host", err)
	}
	scope := d.scope(r)
	allowed, err := scope.CanViewHost(r.Context(), host)
	if err != nil {
		return fail("list alert notifications: check access", err)
	}
	if !allowed {
		return forbidden()
	}
	showRecipients, err := d.perms.HasPermission(r.Context(), scope.Role(), "notification.view")
	if err != nil {
		return fail("list alert notifications: check notification.view", err)
	}

	items, err := d.outbox.ListForAlert(r.Context(), id)
	if err != nil {
		return fail("list alert notifications", err)
	}
	out := make([]alertNotificationResponse, 0, len(items))
	for _, n := range items {
		resp := alertNotificationResponse{
			ID: n.ID, Event: n.Event, Level: n.Level, Channel: n.Channel, Status: n.Status, Attempts: n.Attempts,
			RecipientCount: len(n.Recipients), Subject: n.Subject, Body: n.Body,
			CreatedAt: n.CreatedAt, SentAt: n.SentAt, FailedAt: n.FailedAt, NextAttemptAt: n.NextAttemptAt,
		}
		if showRecipients {
			resp.Recipients, resp.LastError = n.Recipients, n.LastError
		}
		out = append(out, resp)
	}
	writeJSON(w, http.StatusOK, out)
	return nil
}
