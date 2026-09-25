package httpapi

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/google/uuid"

	"healthbeat-server/internal/model"
	"healthbeat-server/internal/store"
)

// Bildirim kuralları: bir alert'in kime, hangi kanaldan ve en az hangi seviyeden gideceği. Kapsam bir organizasyon (altındaki
// dal için de geçerli) ya da tek bir sunucudur. Bir kapsamda kural varsa yalnızca o kurallar uygulanır; hiç kural
// yoksa varsayılan alıcılar kullanılır (bkz. store.Notifications.ResolveRecipients).

type notificationRouteRequest struct {
	OrganizationID *uuid.UUID `json:"organization_id"`
	HostID         *uuid.UUID `json:"host_id"`
	UserID         *uuid.UUID `json:"user_id"`
	ContactID      *uuid.UUID `json:"contact_id"`
	Channel        string     `json:"channel"`
	MinLevel       string     `json:"min_level"`
}

// scopeOrg, kuralın kapsamının organizasyonunu döndürür (sunucu kapsamında sunucunun organizasyonu).
type routeScope struct {
	orgID  uuid.UUID
	hostID *uuid.UUID
}

func (d *Deps) handleListOrganizationRoutes(w http.ResponseWriter, r *http.Request) {
	orgID, ok := d.orgFromPath(w, r, "bildirim kuralları alınamadı")
	if !ok {
		return
	}
	routes, err := d.notifs.ListByOrganization(r.Context(), orgID)
	if err != nil {
		slog.ErrorContext(r.Context(), "list org routes", "err", err)
		writeError(w, http.StatusInternalServerError, "bildirim kuralları alınamadı")
		return
	}
	writeJSON(w, http.StatusOK, routes)
}

func (d *Deps) handleListHostRoutes(w http.ResponseWriter, r *http.Request) {
	host, ok := d.loadHostForView(w, r, "bildirim kuralları alınamadı")
	if !ok {
		return
	}
	routes, err := d.notifs.ListByHost(r.Context(), host.ID)
	if err != nil {
		slog.ErrorContext(r.Context(), "list host routes", "err", err)
		writeError(w, http.StatusInternalServerError, "bildirim kuralları alınamadı")
		return
	}
	writeJSON(w, http.StatusOK, routes)
}

// handleOrganizationRecipientCandidates ve handleHostRecipientCandidates, o kapsam için seçilebilecek alıcıları listeler.
func (d *Deps) handleOrganizationRecipientCandidates(w http.ResponseWriter, r *http.Request) {
	orgID, ok := d.orgFromPath(w, r, "alıcılar alınamadı")
	if !ok {
		return
	}
	cands, err := d.notifs.Candidates(r.Context(), orgID, nil)
	if err != nil {
		slog.ErrorContext(r.Context(), "recipient candidates", "err", err)
		writeError(w, http.StatusInternalServerError, "alıcılar alınamadı")
		return
	}
	writeJSON(w, http.StatusOK, cands)
}

func (d *Deps) handleHostRecipientCandidates(w http.ResponseWriter, r *http.Request) {
	host, ok := d.loadHostForView(w, r, "alıcılar alınamadı")
	if !ok {
		return
	}
	cands, err := d.notifs.Candidates(r.Context(), host.OrganizationID, &host.ID)
	if err != nil {
		slog.ErrorContext(r.Context(), "recipient candidates", "err", err)
		writeError(w, http.StatusInternalServerError, "alıcılar alınamadı")
		return
	}
	writeJSON(w, http.StatusOK, cands)
}

// validateRouteFields, kanal ve seviye alanlarını denetler.
func validateRouteFields(channel, minLevel string) string {
	if !model.ValidChannel(channel) {
		return "channel email, sms, slack, discord veya telegram olmalı"
	}
	if !model.ImplementedChannel(channel) {
		return "channel " + channel + " henüz desteklenmiyor (şimdilik yalnızca: email)"
	}
	if !model.ValidAlertLevel(minLevel) {
		return "min_level info, warning veya critical olmalı"
	}
	return ""
}

func (d *Deps) handleCreateRoute(w http.ResponseWriter, r *http.Request) {
	var req notificationRouteRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "geçersiz istek gövdesi")
		return
	}
	if req.MinLevel == "" {
		req.MinLevel = model.AlertLevelWarning
	}
	if req.Channel == "" {
		req.Channel = model.ChannelEmail
	}
	if msg := validateRouteFields(req.Channel, req.MinLevel); msg != "" {
		writeError(w, http.StatusBadRequest, msg)
		return
	}
	if (req.OrganizationID == nil) == (req.HostID == nil) {
		writeError(w, http.StatusBadRequest, "organization_id ve host_id'den tam olarak biri verilmeli")
		return
	}
	if (req.UserID == nil) == (req.ContactID == nil) {
		writeError(w, http.StatusBadRequest, "user_id ve contact_id'den tam olarak biri verilmeli")
		return
	}

	// Kapsamın organizasyonu ve erişim.
	var scopeOrg uuid.UUID
	var scopeHost *uuid.UUID
	if req.HostID != nil {
		host, err := d.hosts.GetByID(r.Context(), *req.HostID)
		if err != nil {
			if errors.Is(err, store.ErrNotFound) {
				writeError(w, http.StatusNotFound, "sunucu bulunamadı")
				return
			}
			slog.ErrorContext(r.Context(), "create route: host", "err", err)
			writeError(w, http.StatusInternalServerError, "kural oluşturulamadı")
			return
		}
		scopeOrg, scopeHost = host.OrganizationID, &host.ID
	} else {
		scopeOrg = *req.OrganizationID
		if _, err := d.organizations.GetByID(r.Context(), scopeOrg); err != nil {
			if errors.Is(err, store.ErrNotFound) {
				writeError(w, http.StatusNotFound, "organizasyon bulunamadı")
				return
			}
			slog.ErrorContext(r.Context(), "create route: organization", "err", err)
			writeError(w, http.StatusInternalServerError, "kural oluşturulamadı")
			return
		}
	}
	allowed, err := d.requireOrgAccess(r, scopeOrg)
	if err != nil {
		slog.ErrorContext(r.Context(), "create route: check access", "err", err)
		writeError(w, http.StatusInternalServerError, "kural oluşturulamadı")
		return
	}
	if !allowed {
		writeError(w, http.StatusForbidden, "yetkiniz yok")
		return
	}

	// Alıcı bu kapsam için seçilebilir olmalı.
	cands, err := d.notifs.Candidates(r.Context(), scopeOrg, scopeHost)
	if err != nil {
		slog.ErrorContext(r.Context(), "create route: candidates", "err", err)
		writeError(w, http.StatusInternalServerError, "kural oluşturulamadı")
		return
	}
	okRecipient := false
	for _, c := range cands {
		if (req.UserID != nil && c.UserID != nil && *c.UserID == *req.UserID) || (req.ContactID != nil && c.ContactID != nil && *c.ContactID == *req.ContactID) {
			okRecipient = true
			break
		}
	}
	if !okRecipient {
		writeError(w, http.StatusBadRequest, "bu alıcı bu kapsam için seçilemez (yalnızca kapsamdaki yöneticiler, atanmış operatörler ve organizasyonun iletişim kişileri)")
		return
	}

	route, err := d.notifs.Create(r.Context(), model.NotificationRoute{
		OrganizationID: req.OrganizationID, HostID: req.HostID, UserID: req.UserID, ContactID: req.ContactID,
		Channel: req.Channel, MinLevel: req.MinLevel,
	})
	if err != nil {
		d.writeRouteError(w, r, err, "kural oluşturulamadı")
		return
	}
	targetID := route.ID.String()
	d.logAudit(r, "notification.create", "notification_route", &targetID, map[string]any{
		"organization_id": route.OrganizationID, "host_id": route.HostID, "channel": route.Channel, "min_level": route.MinLevel,
		"recipient": route.RecipientName,
	})
	writeJSON(w, http.StatusCreated, route)
}

func (d *Deps) handleUpdateRoute(w http.ResponseWriter, r *http.Request) {
	existing, ok := d.routeFromPath(w, r, "kural güncellenemedi")
	if !ok {
		return
	}
	var req struct {
		Channel  string `json:"channel"`
		MinLevel string `json:"min_level"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "geçersiz istek gövdesi")
		return
	}
	if req.Channel == "" {
		req.Channel = existing.Channel
	}
	if req.MinLevel == "" {
		req.MinLevel = existing.MinLevel
	}
	if msg := validateRouteFields(req.Channel, req.MinLevel); msg != "" {
		writeError(w, http.StatusBadRequest, msg)
		return
	}
	route, err := d.notifs.Update(r.Context(), existing.ID, req.Channel, req.MinLevel)
	if err != nil {
		d.writeRouteError(w, r, err, "kural güncellenemedi")
		return
	}
	targetID := route.ID.String()
	d.logAudit(r, "notification.update", "notification_route", &targetID, map[string]any{"channel": route.Channel, "min_level": route.MinLevel})
	writeJSON(w, http.StatusOK, route)
}

func (d *Deps) handleDeleteRoute(w http.ResponseWriter, r *http.Request) {
	existing, ok := d.routeFromPath(w, r, "kural silinemedi")
	if !ok {
		return
	}
	if err := d.notifs.Delete(r.Context(), existing.ID); err != nil {
		d.writeRouteError(w, r, err, "kural silinemedi")
		return
	}
	targetID := existing.ID.String()
	d.logAudit(r, "notification.delete", "notification_route", &targetID, map[string]any{"organization_id": existing.OrganizationID, "host_id": existing.HostID})
	w.WriteHeader(http.StatusNoContent)
}

func (d *Deps) writeRouteError(w http.ResponseWriter, r *http.Request, err error, failure string) {
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, "kayıt bulunamadı")
	case errors.Is(err, store.ErrConflict):
		writeError(w, http.StatusConflict, err.Error())
	default:
		slog.ErrorContext(r.Context(), "save notification route", "failure", failure, "err", err)
		writeError(w, http.StatusInternalServerError, failure)
	}
}

// routeFromPath, yoldaki kuralı yükler ve kapsamına (organizasyon ya da sunucunun organizasyonu) erişimi denetler.
func (d *Deps) routeFromPath(w http.ResponseWriter, r *http.Request, failure string) (model.NotificationRoute, bool) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "geçersiz kimlik")
		return model.NotificationRoute{}, false
	}
	route, err := d.notifs.GetByID(r.Context(), id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "kural bulunamadı")
			return model.NotificationRoute{}, false
		}
		slog.ErrorContext(r.Context(), "lookup notification route", "failure", failure, "err", err)
		writeError(w, http.StatusInternalServerError, failure)
		return model.NotificationRoute{}, false
	}
	orgID := uuid.Nil
	if route.OrganizationID != nil {
		orgID = *route.OrganizationID
	} else if route.HostID != nil {
		host, err := d.hosts.GetByID(r.Context(), *route.HostID)
		if err != nil {
			slog.ErrorContext(r.Context(), "lookup notification route host", "failure", failure, "err", err)
			writeError(w, http.StatusInternalServerError, failure)
			return model.NotificationRoute{}, false
		}
		orgID = host.OrganizationID
	}
	allowed, err := d.requireOrgAccess(r, orgID)
	if err != nil {
		slog.ErrorContext(r.Context(), "check notification route access", "failure", failure, "err", err)
		writeError(w, http.StatusInternalServerError, failure)
		return model.NotificationRoute{}, false
	}
	if !allowed {
		writeError(w, http.StatusForbidden, "yetkiniz yok")
		return model.NotificationRoute{}, false
	}
	return route, true
}
