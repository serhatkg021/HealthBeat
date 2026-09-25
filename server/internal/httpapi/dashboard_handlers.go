package httpapi

import (
	"context"
	"log/slog"
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
}

// handleDashboardSummary, panelin genel bakış ekranını besler (bkz.
// docs/MIMARI.md bölüm 9: "genel sağlık, aktif alert sayısı, eşik
// aşan/kritik durumdaki host'lar"). Bölüm 7'nin örnek endpoint listesinde yok —
// eklendi, çünkü "kaç host/alert görebiliyorum" sorusunu, host her satırı kendisi
// çekip saymadan başka hiçbir şey yanıtlayamaz.
func (d *Deps) handleDashboardSummary(w http.ResponseWriter, r *http.Request) {
	role, _ := roleFromContext(r.Context())
	userID, _ := userIDFromContext(r.Context())

	hostIDs, err := d.dashboardScope(r.Context(), role, userID)
	if err != nil {
		slog.ErrorContext(r.Context(), "dashboard summary: resolve scope", "err", err)
		writeError(w, http.StatusInternalServerError, "özet oluşturulamadı")
		return
	}

	online, offline, err := d.hosts.CountByStatus(r.Context(), hostIDs)
	if err != nil {
		slog.ErrorContext(r.Context(), "dashboard summary: count hosts", "err", err)
		writeError(w, http.StatusInternalServerError, "özet oluşturulamadı")
		return
	}
	critical, warning, err := d.alerts.CountOpenByLevel(r.Context(), hostIDs)
	if err != nil {
		slog.ErrorContext(r.Context(), "dashboard summary: count alerts", "err", err)
		writeError(w, http.StatusInternalServerError, "özet oluşturulamadı")
		return
	}

	writeJSON(w, http.StatusOK, dashboardSummaryResponse{
		TotalHosts:         online + offline,
		OnlineHosts:        online,
		OfflineHosts:       offline,
		OpenAlerts:         critical + warning,
		OpenCriticalAlerts: critical,
		OpenWarningAlerts:  warning,
	})
}

// dashboardScope, çağıranın görebildiği host kimliklerini döndürür. nil "kapsamsız" demektir
// (super_admin her şeyi görür); diğer her rol somut (belki boş) bir dilim alır.
func (d *Deps) dashboardScope(ctx context.Context, role string, userID uuid.UUID) ([]uuid.UUID, error) {
	switch role {
	case model.RoleSuperAdmin:
		return nil, nil
	case model.RoleOrgAdmin:
		orgIDs, err := d.userOrgs.ListOrganizationIDs(ctx, userID)
		if err != nil {
			return nil, err
		}
		return d.hosts.ListIDsByOrganizations(ctx, orgIDs)
	case model.RoleOperator:
		return d.userHosts.ListHostIDs(ctx, userID)
	}
	return []uuid.UUID{}, nil
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
func (d *Deps) handleDashboardOverview(w http.ResponseWriter, r *http.Request) {
	role, _ := roleFromContext(r.Context())
	userID, _ := userIDFromContext(r.Context())

	fail := func(step string, err error) {
		slog.ErrorContext(r.Context(), "dashboard overview: "+step, "err", err)
		writeError(w, http.StatusInternalServerError, "özet oluşturulamadı")
	}

	hostIDs, err := d.dashboardScope(r.Context(), role, userID)
	if err != nil {
		fail("resolve scope", err)
		return
	}

	var hosts []model.Host
	var alerts []model.Alert
	if hostIDs == nil {
		if hosts, err = d.hosts.ListAll(r.Context()); err != nil {
			fail("list hosts", err)
			return
		}
		if alerts, _, err = d.alerts.List(r.Context(), model.AlertStatusOpen, store.ListParams{}); err != nil {
			fail("list alerts", err)
			return
		}
	} else {
		if hosts, err = d.hosts.ListByIDs(r.Context(), hostIDs); err != nil {
			fail("list hosts", err)
			return
		}
		if alerts, _, err = d.alerts.ListForHosts(r.Context(), model.AlertStatusOpen, hostIDs, store.ListParams{}); err != nil {
			fail("list alerts", err)
			return
		}
	}

	orgs := []overviewOrganization{}
	if role == model.RoleSuperAdmin || role == model.RoleOrgAdmin {
		var list []model.Organization
		if role == model.RoleSuperAdmin {
			list, err = d.organizations.List(r.Context())
		} else {
			var orgIDs []uuid.UUID
			if orgIDs, err = d.userOrgs.ListOrganizationIDs(r.Context(), userID); err == nil {
				list, err = d.organizations.ListByIDs(r.Context(), orgIDs)
			}
		}
		if err != nil {
			fail("list organizations", err)
			return
		}
		for _, o := range list {
			orgs = append(orgs, overviewOrganization{ID: o.ID, Name: o.Name})
		}
	}

	out := make([]overviewHost, 0, len(hosts))
	for _, c := range hosts {
		out = append(out, overviewHost{
			ID: c.ID, OrganizationID: c.OrganizationID, Title: c.Title, IP: c.IP,
			Mode: c.Mode, Status: c.Status, LastSeen: c.LastSeen,
			AgentVersion: c.AgentVersion, AgentProtocol: c.AgentProtocol,
		})
	}
	writeJSON(w, http.StatusOK, dashboardOverviewResponse{Organizations: orgs, Hosts: out, Alerts: alerts})
}
