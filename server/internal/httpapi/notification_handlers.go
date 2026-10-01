package httpapi

import (
	"errors"
	"net/http"

	"github.com/google/uuid"

	"healthbeat-server/internal/model"
	"healthbeat-server/internal/store"
)

// Bildirim kuralları: bir alert'in kime, hangi kanaldan ve en az hangi seviyeden gideceği. Kapsam bir organizasyon (altındaki
// dal için de geçerli) ya da tek bir sunucudur. Bildirim her zaman sistem sahiplerine gider; kurallar ek alıcıdır ve
// toplanır (bkz. store.Notifications.ResolveRecipients).

type notificationRouteRequest struct {
	OrganizationID *uuid.UUID `json:"organization_id"`
	HostID         *uuid.UUID `json:"host_id"`
	UserID         *uuid.UUID `json:"user_id"`
	ContactID      *uuid.UUID `json:"contact_id"`
	Channel        string     `json:"channel"`
	MinLevel       string     `json:"min_level"`
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

// validateRouteLevel, kuralın en düşük seviyesini denetler.
func validateRouteLevel(minLevel string) error {
	if !model.ValidAlertLevel(minLevel) {
		return errors.New("min_level info, warning veya critical olmalı")
	}
	return nil
}

// routeChannelError, kanalın bir kurala seçilebilir olup olmadığını söyler: tanımlı ve server'ca gönderilebilir, kişiye
// giden (ortak kanallar yalnızca sistem sahiplerine gider) ve açık olmalı. Seçilemiyorsa alanı adlandıran bir 400 döner.
func (d *Deps) routeChannelError(channel string) error {
	reject := func(message string) error {
		e := newError(http.StatusBadRequest, message)
		e.Fields = map[string]string{"channel": message}
		return e
	}
	ch, ok := d.channels.Get(channel)
	personal, implemented := d.alertEngine.Notifier(channel)
	switch {
	case !ok || !implemented:
		return reject("bu bildirim kanalı desteklenmiyor")
	case !personal:
		return reject("bu kanal yalnızca sistem sahiplerine gönderir; kurallarda seçilemez")
	case !ch.Enabled:
		return reject("bu kanal kapalı; önce Ayarlar → Sistem Ayarları → Bildirim kanalları'ndan açılmalı")
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
	if err := validateRouteLevel(req.MinLevel); err != nil {
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
	if err := d.routeChannelError(req.Channel); err != nil {
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
		return storeError(err, "kayıt bulunamadı", fail, "create route")
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
	if err := validateRouteLevel(req.MinLevel); err != nil {
		return badRequest(err.Error())
	}
	// Kanalı kapalı bir kuralın seviyesi değiştirilebilir; başka bir kanala geçiş yeni kanalın seçilebilir olmasını ister.
	if req.Channel != existing.Channel {
		if err := d.routeChannelError(req.Channel); err != nil {
			return err
		}
	}
	route, err := d.notifs.Update(r.Context(), existing.ID, req.Channel, req.MinLevel)
	if err != nil {
		return storeError(err, "kayıt bulunamadı", fail, "update route")
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
		return storeError(err, "kayıt bulunamadı", fail, "delete route")
	}
	targetID := existing.ID.String()
	d.logAudit(r, "notification.delete", "notification_route", &targetID, map[string]any{"organization_id": existing.OrganizationID, "host_id": existing.HostID})
	w.WriteHeader(http.StatusNoContent)
	return nil
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
