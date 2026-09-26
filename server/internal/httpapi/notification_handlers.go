package httpapi

import (
	"errors"
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

func (d *Deps) handleListOrganizationRoutes(w http.ResponseWriter, r *http.Request) error {
	fail := failWith("bildirim kuralları alınamadı")
	orgID, err := d.managedOrg(r, "list org routes", fail)
	if err != nil {
		return err
	}
	routes, err := d.notifs.ListByOrganization(r.Context(), orgID)
	if err != nil {
		return fail("list org routes", err)
	}
	writeJSON(w, http.StatusOK, routes)
	return nil
}

func (d *Deps) handleListHostRoutes(w http.ResponseWriter, r *http.Request) error {
	fail := failWith("bildirim kuralları alınamadı")
	host, err := d.viewableHost(r, "list host routes", fail)
	if err != nil {
		return err
	}
	routes, err := d.notifs.ListByHost(r.Context(), host.ID)
	if err != nil {
		return fail("list host routes", err)
	}
	writeJSON(w, http.StatusOK, routes)
	return nil
}

// handleOrganizationRecipientCandidates ve handleHostRecipientCandidates, o kapsam için seçilebilecek alıcıları listeler.
func (d *Deps) handleOrganizationRecipientCandidates(w http.ResponseWriter, r *http.Request) error {
	fail := failWith("alıcılar alınamadı")
	orgID, err := d.managedOrg(r, "recipient candidates", fail)
	if err != nil {
		return err
	}
	cands, err := d.notifs.Candidates(r.Context(), orgID, nil)
	if err != nil {
		return fail("recipient candidates", err)
	}
	writeJSON(w, http.StatusOK, cands)
	return nil
}

func (d *Deps) handleHostRecipientCandidates(w http.ResponseWriter, r *http.Request) error {
	fail := failWith("alıcılar alınamadı")
	host, err := d.viewableHost(r, "recipient candidates", fail)
	if err != nil {
		return err
	}
	cands, err := d.notifs.Candidates(r.Context(), host.OrganizationID, &host.ID)
	if err != nil {
		return fail("recipient candidates", err)
	}
	writeJSON(w, http.StatusOK, cands)
	return nil
}

// validateRouteFields, kanal ve seviye alanlarını denetler.
func validateRouteFields(channel, minLevel string) error {
	if !model.ValidChannel(channel) {
		return errors.New("channel email, sms, slack, discord veya telegram olmalı")
	}
	if !model.ImplementedChannel(channel) {
		return errors.New("channel " + channel + " henüz desteklenmiyor (şimdilik yalnızca: email)")
	}
	if !model.ValidAlertLevel(minLevel) {
		return errors.New("min_level info, warning veya critical olmalı")
	}
	return nil
}

// Validate, verilmeyen kanal ve seviyeye varsayılanı (email, warning) koyar.
func (req *notificationRouteRequest) Validate() error {
	if req.MinLevel == "" {
		req.MinLevel = model.AlertLevelWarning
	}
	if req.Channel == "" {
		req.Channel = model.ChannelEmail
	}
	if err := validateRouteFields(req.Channel, req.MinLevel); err != nil {
		return err
	}
	if (req.OrganizationID == nil) == (req.HostID == nil) {
		return errors.New("organization_id ve host_id'den tam olarak biri verilmeli")
	}
	if (req.UserID == nil) == (req.ContactID == nil) {
		return errors.New("user_id ve contact_id'den tam olarak biri verilmeli")
	}
	return nil
}

func (d *Deps) handleCreateRoute(w http.ResponseWriter, r *http.Request) error {
	fail := failWith("kural oluşturulamadı")
	req, err := bind[notificationRouteRequest](r)
	if err != nil {
		return err
	}

	// Kapsamın organizasyonu ve erişim.
	var scopeOrg uuid.UUID
	var scopeHost *uuid.UUID
	if req.HostID != nil {
		host, err := d.hosts.GetByID(r.Context(), *req.HostID)
		if errors.Is(err, store.ErrNotFound) {
			return notFound("sunucu bulunamadı")
		}
		if err != nil {
			return fail("create route: host", err)
		}
		scopeOrg, scopeHost = host.OrganizationID, &host.ID
	} else {
		scopeOrg = *req.OrganizationID
		_, err := d.organizations.GetByID(r.Context(), scopeOrg)
		if errors.Is(err, store.ErrNotFound) {
			return notFound("organizasyon bulunamadı")
		}
		if err != nil {
			return fail("create route: organization", err)
		}
	}
	allowed, err := d.scope(r).CanManageOrg(r.Context(), scopeOrg)
	if err != nil {
		return fail("create route: check access", err)
	}
	if !allowed {
		return forbidden()
	}

	// Alıcı bu kapsam için seçilebilir olmalı.
	cands, err := d.notifs.Candidates(r.Context(), scopeOrg, scopeHost)
	if err != nil {
		return fail("create route: candidates", err)
	}
	okRecipient := false
	for _, c := range cands {
		if (req.UserID != nil && c.UserID != nil && *c.UserID == *req.UserID) || (req.ContactID != nil && c.ContactID != nil && *c.ContactID == *req.ContactID) {
			okRecipient = true
			break
		}
	}
	if !okRecipient {
		return badRequest("bu alıcı bu kapsam için seçilemez (yalnızca kapsamdaki yöneticiler, atanmış operatörler ve organizasyonun iletişim kişileri)")
	}

	route, err := d.notifs.Create(r.Context(), model.NotificationRoute{
		OrganizationID: req.OrganizationID, HostID: req.HostID, UserID: req.UserID, ContactID: req.ContactID,
		Channel: req.Channel, MinLevel: req.MinLevel,
	})
	if err != nil {
		return routeSaveError(err, "create route", fail)
	}
	targetID := route.ID.String()
	d.logAudit(r, "notification.create", "notification_route", &targetID, map[string]any{
		"organization_id": route.OrganizationID, "host_id": route.HostID, "channel": route.Channel, "min_level": route.MinLevel,
		"recipient": route.RecipientName,
	})
	writeJSON(w, http.StatusCreated, route)
	return nil
}

// updateRouteRequest'in boş alanları mevcut kuralınkini korur; bu yüzden doğrulama handler'da, birleştirmeden sonradır.
type updateRouteRequest struct {
	Channel  string `json:"channel"`
	MinLevel string `json:"min_level"`
}

func (d *Deps) handleUpdateRoute(w http.ResponseWriter, r *http.Request) error {
	fail := failWith("kural güncellenemedi")
	existing, err := d.managedRoute(r, "update route", fail)
	if err != nil {
		return err
	}
	req, err := bind[updateRouteRequest](r)
	if err != nil {
		return err
	}
	if req.Channel == "" {
		req.Channel = existing.Channel
	}
	if req.MinLevel == "" {
		req.MinLevel = existing.MinLevel
	}
	if err := validateRouteFields(req.Channel, req.MinLevel); err != nil {
		return badRequest(err.Error())
	}
	route, err := d.notifs.Update(r.Context(), existing.ID, req.Channel, req.MinLevel)
	if err != nil {
		return routeSaveError(err, "update route", fail)
	}
	targetID := route.ID.String()
	d.logAudit(r, "notification.update", "notification_route", &targetID, map[string]any{"channel": route.Channel, "min_level": route.MinLevel})
	writeJSON(w, http.StatusOK, route)
	return nil
}

func (d *Deps) handleDeleteRoute(w http.ResponseWriter, r *http.Request) error {
	fail := failWith("kural silinemedi")
	existing, err := d.managedRoute(r, "delete route", fail)
	if err != nil {
		return err
	}
	if err := d.notifs.Delete(r.Context(), existing.ID); err != nil {
		return routeSaveError(err, "delete route", fail)
	}
	targetID := existing.ID.String()
	d.logAudit(r, "notification.delete", "notification_route", &targetID, map[string]any{"organization_id": existing.OrganizationID, "host_id": existing.HostID})
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func routeSaveError(err error, op string, fail failFunc) error {
	switch {
	case errors.Is(err, store.ErrNotFound):
		return notFound("kayıt bulunamadı")
	case errors.Is(err, store.ErrConflict):
		return conflict(err.Error())
	default:
		return fail(op, err)
	}
}

// managedRoute, yoldaki kuralı yükler ve kapsamına (organizasyon ya da sunucunun organizasyonu) erişimi denetler.
func (d *Deps) managedRoute(r *http.Request, op string, fail failFunc) (model.NotificationRoute, error) {
	id, err := pathID(r, "geçersiz kimlik")
	if err != nil {
		return model.NotificationRoute{}, err
	}
	route, err := d.notifs.GetByID(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		return model.NotificationRoute{}, notFound("kural bulunamadı")
	}
	if err != nil {
		return model.NotificationRoute{}, fail(op+": lookup notification route", err)
	}
	orgID := uuid.Nil
	if route.OrganizationID != nil {
		orgID = *route.OrganizationID
	} else if route.HostID != nil {
		host, err := d.hosts.GetByID(r.Context(), *route.HostID)
		if err != nil {
			return model.NotificationRoute{}, fail(op+": lookup notification route host", err)
		}
		orgID = host.OrganizationID
	}
	allowed, err := d.scope(r).CanManageOrg(r.Context(), orgID)
	if err != nil {
		return model.NotificationRoute{}, fail(op+": check notification route access", err)
	}
	if !allowed {
		return model.NotificationRoute{}, forbidden()
	}
	return route, nil
}
