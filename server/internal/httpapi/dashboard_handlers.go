package httpapi

import (
	"net/http"
	"time"

	"github.com/google/uuid"

	"healthbeat-server/internal/model"
	"healthbeat-server/internal/store"
)

type dashboardSummaryResponse struct {
	TotalHosts         int `json:"total_hosts"`
	OnlineHosts        int `json:"online_hosts"`
	OfflineHosts       int `json:"offline_hosts"`
	OpenAlerts         int `json:"open_alerts"`
	OpenCriticalAlerts int `json:"open_critical_alerts"`
	OpenWarningAlerts  int `json:"open_warning_alerts"`
	OpenInfoAlerts     int `json:"open_info_alerts"`
}

// handleDashboardSummary, panelin genel bakış ekranını besler (bkz.
// docs/MIMARI.md bölüm 9: "genel sağlık, aktif alert sayısı, eşik
// aşan/kritik durumdaki host'lar"). Bölüm 7'nin örnek endpoint listesinde yok —
// eklendi, çünkü "kaç host/alert görebiliyorum" sorusunu, host her satırı kendisi
// çekip saymadan başka hiçbir şey yanıtlayamaz.
func (d *Deps) handleDashboardSummary(w http.ResponseWriter, r *http.Request) error {
	fail := failWith("özet oluşturulamadı")
	hostIDs, err := d.scope(r).VisibleHostIDs(r.Context())
	if err != nil {
		return fail("dashboard summary: resolve scope", err)
	}
	online, offline, err := d.hosts.CountByStatus(r.Context(), hostIDs)
	if err != nil {
		return fail("dashboard summary: count hosts", err)
	}
	open, err := d.alerts.CountOpenByLevel(r.Context(), hostIDs)
	if err != nil {
		return fail("dashboard summary: count alerts", err)
	}

	writeJSON(w, http.StatusOK, dashboardSummaryResponse{
		TotalHosts:         online + offline,
		OnlineHosts:        online,
		OfflineHosts:       offline,
		OpenAlerts:         open.Critical + open.Warning + open.Info,
		OpenCriticalAlerts: open.Critical,
		OpenWarningAlerts:  open.Warning,
		OpenInfoAlerts:     open.Info,
	})
	return nil
}

type overviewOrganization struct {
	ID   uuid.UUID `json:"id"`
	Name string    `json:"name"`
}

// overviewHost bilerek dardır: panelin süzgeçleyip listelemek için ihtiyaç duyduğu alanlar;
// bağlantı ayrıntıları, disk seçimi ve kimlik bilgisiyle ilgili hiçbir şey yok.
type overviewHost struct {
	ID             uuid.UUID  `json:"id"`
	OrganizationID uuid.UUID  `json:"organization_id"`
	Title          string     `json:"title"`
	IP             string     `json:"ip"`
	Mode           string     `json:"mode"`
	Status         string     `json:"status"`
	LastSeen       *time.Time `json:"last_seen,omitempty"`
	// AgentVersion/AgentProtocol: panel "güncellenmesi gereken agent"ları süzsün/saysın diye
	// (bkz. model.Host). Sürümden başka bir şey değildir, gizli bilgi taşımaz.
	AgentVersion  *string `json:"agent_version,omitempty"`
	AgentProtocol *int    `json:"agent_protocol,omitempty"`
	// MaintenanceUntil, sunucu şu an bakımdaysa kesintisiz bakımın bittiği andır (MaintenanceUntilLocal kurulumun
	// saatinde); bakımda değilse yoktur.
	MaintenanceUntil      *time.Time `json:"maintenance_until,omitempty"`
	MaintenanceUntilLocal *string    `json:"maintenance_until_local,omitempty"`
}

type dashboardOverviewResponse struct {
	Organizations []overviewOrganization `json:"organizations"`
	Hosts         []overviewHost         `json:"hosts"`
	Alerts        []model.Alert          `json:"alerts"`
}

// handleDashboardOverview, panelin Özet ekranındaki süzgeçlerin çalıştığı veriyi tek istekte verir:
// çağıranın görebildiği host'lar, bunların açık alert'leri ve organizasyon adları. Süzme panelde
// yapılır; böylece her süzgeç değişiminde istek atılmaz ve sayılar birbiriyle tutarlı kalır.
// Operatör organizasyon adlarını almaz (organization.view yetkisi yok) — organization_id alanı yeter.
func (d *Deps) handleDashboardOverview(w http.ResponseWriter, r *http.Request) error {
	scope := d.scope(r)
	failed := failWith("özet oluşturulamadı")
	fail := func(step string, err error) error { return failed("dashboard overview: "+step, err) }

	hostIDs, err := scope.VisibleHostIDs(r.Context())
	if err != nil {
		return fail("resolve scope", err)
	}

	var hosts []model.Host
	var alerts []model.Alert
	if hostIDs == nil {
		if hosts, err = d.hosts.ListAll(r.Context()); err != nil {
			return fail("list hosts", err)
		}
		if alerts, _, err = d.alerts.List(r.Context(), store.AlertFilter{Status: model.AlertStatusOpen}, store.ListParams{}); err != nil {
			return fail("list alerts", err)
		}
	} else {
		if hosts, err = d.hosts.ListByIDs(r.Context(), hostIDs); err != nil {
			return fail("list hosts", err)
		}
		if alerts, _, err = d.alerts.ListForHosts(r.Context(), store.AlertFilter{Status: model.AlertStatusOpen}, hostIDs, store.ListParams{}); err != nil {
			return fail("list alerts", err)
		}
	}

	orgs := []overviewOrganization{}
	if scope.IsSuperAdmin() || scope.Role() == model.RoleOrgAdmin {
		var list []model.Organization
		if scope.IsSuperAdmin() {
			list, err = d.organizations.List(r.Context())
		} else {
			var orgIDs []uuid.UUID
			if orgIDs, err = scope.ManagedOrgIDs(r.Context()); err == nil {
				list, err = d.organizations.ListByIDs(r.Context(), orgIDs)
			}
		}
		if err != nil {
			return fail("list organizations", err)
		}
		for _, o := range list {
			orgs = append(orgs, overviewOrganization{ID: o.ID, Name: o.Name})
		}
	}

	until, err := d.maintenanceUntil(r.Context(), hosts)
	if err != nil {
		return fail("maintenance", err)
	}
	loc := d.location()
	out := make([]overviewHost, 0, len(hosts))
	for _, c := range hosts {
		h := overviewHost{
			ID: c.ID, OrganizationID: c.OrganizationID, Title: c.Title, IP: c.IP,
			Mode: c.Mode, Status: c.Status, LastSeen: c.LastSeen,
			AgentVersion: c.AgentVersion, AgentProtocol: c.AgentProtocol,
		}
		if t, ok := until[c.ID]; ok {
			h.MaintenanceUntil, h.MaintenanceUntilLocal = &t, localPtr(&t, loc, localDateTime)
		}
		out = append(out, h)
	}
	writeJSON(w, http.StatusOK, dashboardOverviewResponse{Organizations: orgs, Hosts: out, Alerts: alerts})
	return nil
}
