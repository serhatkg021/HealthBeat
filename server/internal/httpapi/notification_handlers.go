package httpapi

import (
	"errors"
	"net/http"

	"github.com/google/uuid"

	"healthbeat-server/internal/model"
	"healthbeat-server/internal/store"
)

// Bildirim kuralları: bir alert'in kime, hangi kanaldan ve en az hangi seviyeden gideceği. Kapsam sistem (bütün sunucular),
// bir organizasyon (altındaki dal için de geçerli) ya da tek bir sunucudur; alıcılar yalnızca kurallardan gelir ve
// toplanır (bkz. store.Notifications.ResolveRecipients). Sistem kurallarını görmek settings.view, yazmak settings.manage
// ister (varsayılan olarak yalnızca süper admin); organizasyon ve sunucu kuralları notification.view/edit ve kapsama erişim.

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

func (d *Deps) handleListSystemRoutes(w http.ResponseWriter, r *http.Request) error {
	routes, err := d.notifs.ListSystem(r.Context())
	if err != nil {
		return failWith("bildirim kuralları alınamadı")("list system routes", err)
	}
	writeJSON(w, http.StatusOK, routes)
	return nil
}

// handleSystemRecipientCandidates, sistem kurallarına seçilebilecek alıcılardır: süper adminler ve sistem kişileri.
func (d *Deps) handleSystemRecipientCandidates(w http.ResponseWriter, r *http.Request) error {
	cands, err := d.notifs.SystemCandidates(r.Context())
	if err != nil {
		return failWith("alıcılar alınamadı")("system recipient candidates", err)
	}
	writeJSON(w, http.StatusOK, cands)
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

// "Bu alert kime gider?" önizlemesi: bir sunucudaki bir seviyedeki alert'in bildirimi şu an kime gider, kime neden
// gitmez. Alıcılar gerçek gönderimin kurallarıyla hesaplanır (alertengine.Engine.Preview); hiçbir şey yazılmaz.

type previewSource struct {
	Kind string `json:"kind"`           // system, organization ya da host
	Name string `json:"name,omitempty"` // organizasyon kuralında organizasyonun adı
}

type previewRecipient struct {
	Name    string          `json:"name"`
	Channel string          `json:"channel"`
	Address string          `json:"address"`
	Source  previewSource   `json:"source"`
	Also    []previewSource `json:"also,omitempty"`
	Reason  string          `json:"reason,omitempty"` // yalnızca skipped'de: channel_off, channel_not_supported ya da no_access
}

// previewSystemCount, sistem kurallarını göremeyen (settings.view'sız) kullanıcıya sistem kurallarından gelen alıcıların
// kanal başına sayısıdır.
type previewSystemCount struct {
	Channel string `json:"channel"`
	Count   int    `json:"count"`
	Reason  string `json:"reason,omitempty"` // boş = gider
}

type notificationPreviewResponse struct {
	HostID        uuid.UUID          `json:"host_id"`
	Level         string             `json:"level"`
	InMaintenance bool               `json:"in_maintenance"`
	Recipients    []previewRecipient `json:"recipients"`
	Skipped       []previewRecipient `json:"skipped"`
	// SystemSummary yalnızca sistem kurallarını göremeyen kullanıcıda doludur; o zaman sistem kaynaklı satırlar
	// listelerde yoktur.
	SystemSummary []previewSystemCount `json:"system_summary"`
}

func (d *Deps) handleNotificationPreview(w http.ResponseWriter, r *http.Request) error {
	fail := failWith("bildirim önizlemesi alınamadı")
	level := r.URL.Query().Get("level")
	if level == "" {
		level = model.AlertLevelCritical
	}
	if !model.ValidAlertLevel(level) {
		return fieldError400("level", "seviye info, warning veya critical olmalı")
	}
	host, err := d.viewableHost(r, "notification preview", fail)
	if err != nil {
		return err
	}
	showSystem, err := d.perms.HasPermission(r.Context(), d.scope(r).Role(), "settings.view")
	if err != nil {
		return fail("notification preview: check settings.view", err)
	}
	p, err := d.alertEngine.Preview(r.Context(), host.ID, host.OrganizationID, level)
	if err != nil {
		return fail("notification preview", err)
	}
	resp := notificationPreviewResponse{HostID: host.ID, Level: level, InMaintenance: p.InMaintenance,
		Recipients: []previewRecipient{}, Skipped: []previewRecipient{}}
	if !showSystem {
		resp.SystemSummary = []previewSystemCount{}
	}
	// add, bir alıcıyı listeye ekler. Sistem kuralları gizliyse sayılır; alıcı aynı zamanda bir organizasyon ya da sunucu
	// kuralındaysa satır kalır ama kaynağı o kural olur (yoksa kuralı yazan "kuralım çalışmıyor" sanır).
	add := func(list *[]previewRecipient, rc store.Recipient, reason string) {
		source, also := rc.Source, rc.Also
		if !showSystem && source.Kind == store.RecipientSourceSystem {
			resp.countSystem(rc.Channel, reason)
			if len(also) == 0 {
				return
			}
			source, also = also[0], also[1:]
		}
		item := previewRecipient{Name: rc.Name, Channel: rc.Channel, Address: rc.Address, Source: previewSource(source), Reason: reason}
		for _, a := range also {
			item.Also = append(item.Also, previewSource(a))
		}
		*list = append(*list, item)
	}
	for _, rc := range p.Recipients {
		add(&resp.Recipients, rc, "")
	}
	for _, rc := range p.Skipped {
		add(&resp.Skipped, rc.Recipient, rc.Reason)
	}
	writeJSON(w, http.StatusOK, resp)
	return nil
}

func (resp *notificationPreviewResponse) countSystem(channel, reason string) {
	for i := range resp.SystemSummary {
		if c := &resp.SystemSummary[i]; c.Channel == channel && c.Reason == reason {
			c.Count++
			return
		}
	}
	resp.SystemSummary = append(resp.SystemSummary, previewSystemCount{Channel: channel, Count: 1, Reason: reason})
}

// validateRouteLevel, kuralın en düşük seviyesini denetler.
func validateRouteLevel(minLevel string) error {
	if !model.ValidAlertLevel(minLevel) {
		return errors.New("min_level info, warning veya critical olmalı")
	}
	return nil
}

// routeChannelError, kanalın bir kurala seçilebilir olup olmadığını söyler: tanımlı ve server'ca gönderilebilir, kişiye
// giden (kuralın alıcısı bir kişidir) ve açık olmalı. Seçilemiyorsa alanı adlandıran bir 400 döner.
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
		return reject("bu kanal bir kişiye gönderilmiyor; kurallarda seçilemez")
	case !ch.Enabled:
		return reject("bu kanal kapalı; önce Bildirim → Kanallar'dan açılmalı")
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
	if req.OrganizationID != nil && req.HostID != nil {
		return errors.New("organization_id ve host_id birlikte verilemez (ikisi de boşsa kural sistem kapsamlıdır)")
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

	ok, err := d.routeRecipientAllowed(r, req, fail)
	if err != nil {
		return err
	}
	if !ok {
		return badRequest("bu alıcı bu kapsam için seçilemez (sistem: süper adminler ve sistem kişileri; organizasyon/sunucu: " +
			"kapsamdaki yöneticiler, atanmış operatörler ve organizasyonun iletişim kişileri)")
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

// routeRecipientAllowed, yeni kuralın kapsamına erişimi denetler (yoksa 403/404 döner) ve alıcının o kapsamın
// adaylarından olup olmadığını söyler. Sistem kapsamı settings.manage ister.
func (d *Deps) routeRecipientAllowed(r *http.Request, req notificationRouteRequest, fail failFunc) (bool, error) {
	var cands []store.Candidate
	switch {
	case req.OrganizationID == nil && req.HostID == nil:
		if err := d.requireSystemRules(r, "create route", fail); err != nil {
			return false, err
		}
		list, err := d.notifs.SystemCandidates(r.Context())
		if err != nil {
			return false, fail("create route: system candidates", err)
		}
		cands = list
	default:
		var scopeOrg uuid.UUID
		var scopeHost *uuid.UUID
		if req.HostID != nil {
			host, err := d.hosts.GetByID(r.Context(), *req.HostID)
			if errors.Is(err, store.ErrNotFound) {
				return false, notFound("sunucu bulunamadı")
			}
			if err != nil {
				return false, fail("create route: host", err)
			}
			scopeOrg, scopeHost = host.OrganizationID, &host.ID
		} else {
			scopeOrg = *req.OrganizationID
			_, err := d.organizations.GetByID(r.Context(), scopeOrg)
			if errors.Is(err, store.ErrNotFound) {
				return false, notFound("organizasyon bulunamadı")
			}
			if err != nil {
				return false, fail("create route: organization", err)
			}
		}
		allowed, err := d.scope(r).CanManageOrg(r.Context(), scopeOrg)
		if err != nil {
			return false, fail("create route: check access", err)
		}
		if !allowed {
			return false, forbidden()
		}
		list, err := d.notifs.Candidates(r.Context(), scopeOrg, scopeHost)
		if err != nil {
			return false, fail("create route: candidates", err)
		}
		cands = list
	}
	for _, c := range cands {
		if (req.UserID != nil && c.UserID != nil && *c.UserID == *req.UserID) || (req.ContactID != nil && c.ContactID != nil && *c.ContactID == *req.ContactID) {
			return true, nil
		}
	}
	return false, nil
}

// requireSystemRules, sistem kapsamlı kuralları yazma iznini (settings.manage) denetler.
func (d *Deps) requireSystemRules(r *http.Request, op string, fail failFunc) error {
	ok, err := d.perms.HasPermission(r.Context(), d.scope(r).Role(), "settings.manage")
	if err != nil {
		return fail(op+": check settings.manage", err)
	}
	if !ok {
		return forbidden()
	}
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

// managedRoute, yoldaki kuralı yükler ve kapsamına erişimi denetler: sistem kuralı settings.manage, organizasyon ya da
// sunucu kuralı o organizasyona (sunucunun organizasyonuna) TAM erişim ister.
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
	if route.OrganizationID == nil && route.HostID == nil {
		if err := d.requireSystemRules(r, op, fail); err != nil {
			return model.NotificationRoute{}, err
		}
		return route, nil
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
