package httpapi

import "net/http"

func (d *Deps) Router() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})

	mux.HandleFunc("POST /api/v1/auth/login", handle(d.handleLogin))
	mux.HandleFunc("POST /api/v1/auth/refresh", handle(d.handleRefresh))
	mux.HandleFunc("POST /api/v1/auth/logout", handle(d.handleLogout))
	// Şifre sıfırlama (kimlik doğrulamasız; e-posta ile): bkz. password_reset_handlers.go.
	mux.HandleFunc("GET /api/v1/auth/options", d.handleAuthOptions)
	mux.HandleFunc("POST /api/v1/auth/forgot-password", handle(d.handleForgotPassword))
	mux.HandleFunc("POST /api/v1/auth/reset-password", handle(d.handleResetPassword))

	mux.HandleFunc("GET /api/v1/me", d.requireAuthEvenIfPasswordChangeDue(handle(d.handleGetMe)))
	mux.HandleFunc("POST /api/v1/me/password", d.requireAuthEvenIfPasswordChangeDue(handle(d.handleChangeOwnPassword)))
	mux.HandleFunc("GET /api/v1/me/hosts", d.requireAuth(handle(d.handleGetMyHosts)))
	mux.HandleFunc("GET /api/v1/meta", d.requireAuth(d.handleMeta))

	mux.HandleFunc("GET /api/v1/organizations", d.requirePermission("organization.view", handle(d.handleListOrganizations)))
	mux.HandleFunc("POST /api/v1/organizations", d.requirePermission("organization.create", handle(d.handleCreateOrganization)))
	mux.HandleFunc("GET /api/v1/organizations/{id}", d.requirePermission("organization.view", handle(d.handleGetOrganization)))
	mux.HandleFunc("PUT /api/v1/organizations/{id}", d.requirePermission("organization.update", handle(d.handleUpdateOrganization)))
	mux.HandleFunc("DELETE /api/v1/organizations/{id}", d.requirePermission("organization.delete", handle(d.handleDeleteOrganization)))

	mux.HandleFunc("GET /api/v1/organizations/{id}/contacts", d.requirePermission("contact.view", handle(d.handleListContacts)))
	mux.HandleFunc("POST /api/v1/organizations/{id}/contacts", d.requirePermission("contact.edit", handle(d.handleCreateContact)))
	mux.HandleFunc("PUT /api/v1/contacts/{id}", d.requirePermission("contact.edit", handle(d.handleUpdateContact)))
	mux.HandleFunc("DELETE /api/v1/contacts/{id}", d.requirePermission("contact.edit", handle(d.handleDeleteContact)))

	mux.HandleFunc("GET /api/v1/organizations/{id}/notification-routes", d.requirePermission("notification.view", handle(d.handleListOrganizationRoutes)))
	mux.HandleFunc("GET /api/v1/organizations/{id}/notification-recipients", d.requirePermission("notification.view", handle(d.handleOrganizationRecipientCandidates)))
	mux.HandleFunc("GET /api/v1/hosts/{id}/notification-routes", d.requirePermission("notification.view", handle(d.handleListHostRoutes)))
	mux.HandleFunc("GET /api/v1/hosts/{id}/notification-recipients", d.requirePermission("notification.view", handle(d.handleHostRecipientCandidates)))
	mux.HandleFunc("POST /api/v1/notification-routes", d.requirePermission("notification.edit", handle(d.handleCreateRoute)))
	mux.HandleFunc("PUT /api/v1/notification-routes/{id}", d.requirePermission("notification.edit", handle(d.handleUpdateRoute)))
	mux.HandleFunc("DELETE /api/v1/notification-routes/{id}", d.requirePermission("notification.edit", handle(d.handleDeleteRoute)))

	mux.HandleFunc("GET /api/v1/users", d.requirePermission("user.view", handle(d.handleListUsers)))
	mux.HandleFunc("POST /api/v1/users", d.requirePermission("user.create", handle(d.handleCreateUser)))
	mux.HandleFunc("GET /api/v1/users/{id}", d.requirePermission("user.view", handle(d.handleGetUser)))
	mux.HandleFunc("PUT /api/v1/users/{id}", d.requirePermission("user.update", handle(d.handleUpdateUser)))
	mux.HandleFunc("DELETE /api/v1/users/{id}", d.requirePermission("user.delete", handle(d.handleDeleteUser)))

	mux.HandleFunc("GET /api/v1/users/{id}/organizations", d.requirePermission("user.view", handle(d.handleGetUserOrganizations)))
	mux.HandleFunc("PUT /api/v1/users/{id}/organizations", d.requirePermission("user.update", handle(d.handleSetUserOrganizations)))

	mux.HandleFunc("GET /api/v1/users/{id}/hosts", d.requirePermission("user.view", handle(d.handleGetUserHosts)))
	mux.HandleFunc("PUT /api/v1/users/{id}/hosts", d.requirePermission("user.update", handle(d.handleSetUserHosts)))
	mux.HandleFunc("POST /api/v1/users/{id}/hosts/by-organization", d.requirePermission("user.update", handle(d.handleAddUserHostsByOrganization)))

	mux.HandleFunc("POST /api/v1/hosts", d.requirePermission("host.create", handle(d.handleCreateHost)))
	mux.HandleFunc("GET /api/v1/hosts/{id}", d.requirePermission("host.view", handle(d.handleGetHost)))
	mux.HandleFunc("PUT /api/v1/hosts/{id}", d.requirePermission("host.update", handle(d.handleUpdateHost)))
	mux.HandleFunc("DELETE /api/v1/hosts/{id}", d.requirePermission("host.delete", handle(d.handleDeleteHost)))
	mux.HandleFunc("POST /api/v1/hosts/{id}/rotate-credentials", d.requirePermission("host.update", handle(d.handleRotateHostCredentials)))
	mux.HandleFunc("GET /api/v1/organizations/{id}/hosts", d.requirePermission("host.view", handle(d.handleListOrganizationHosts)))

	mux.HandleFunc("POST /api/v1/metrics", d.requireHostAuth(handle(d.handleIngestMetrics)))

	mux.HandleFunc("GET /api/v1/thresholds", d.requirePermission("threshold.view", handle(d.handleListThresholds)))
	mux.HandleFunc("POST /api/v1/thresholds", d.requirePermission("threshold.edit", handle(d.handleCreateThreshold)))
	mux.HandleFunc("GET /api/v1/thresholds/{id}", d.requirePermission("threshold.view", handle(d.handleGetThreshold)))
	mux.HandleFunc("PUT /api/v1/thresholds/{id}", d.requirePermission("threshold.edit", handle(d.handleUpdateThreshold)))
	mux.HandleFunc("DELETE /api/v1/thresholds/{id}", d.requirePermission("threshold.edit", handle(d.handleDeleteThreshold)))

	mux.HandleFunc("GET /api/v1/alerts", d.requirePermission("alert.view", handle(d.handleListAlerts)))
	mux.HandleFunc("POST /api/v1/alerts/{id}/acknowledge", d.requirePermission("alert.acknowledge", handle(d.handleAcknowledgeAlert)))

	mux.HandleFunc("GET /api/v1/hosts/{id}/metrics", d.requirePermission("host.view", handle(d.handleGetHostMetrics)))
	mux.HandleFunc("GET /api/v1/hosts/{id}/metrics/latest", d.requirePermission("host.view", handle(d.handleGetHostLatestMetric)))
	mux.HandleFunc("GET /api/v1/hosts/{id}/thresholds", d.requirePermission("threshold.view", handle(d.handleGetHostThresholds)))
	mux.HandleFunc("PUT /api/v1/hosts/{id}/thresholds", d.requirePermission("threshold.edit", handle(d.handleSetHostThresholds)))
	mux.HandleFunc("GET /api/v1/hosts/{id}/disk-alerts", d.requirePermission("host.view", handle(d.handleGetDiskAlerts)))
	mux.HandleFunc("PUT /api/v1/hosts/{id}/disk-alerts", d.requirePermission("host.update", handle(d.handleSetDiskAlerts)))
	mux.HandleFunc("GET /api/v1/hosts/{id}/docker", d.requirePermission("host.view", handle(d.handleGetHostDocker)))

	mux.HandleFunc("GET /api/v1/audit-logs", d.requirePermission("audit.view", handle(d.handleListAuditLogs)))

	mux.HandleFunc("GET /api/v1/dashboard/summary", d.requirePermission("dashboard.view", handle(d.handleDashboardSummary)))
	mux.HandleFunc("GET /api/v1/dashboard/overview", d.requirePermission("dashboard.view", handle(d.handleDashboardOverview)))

	// Dıştan içe: istek kimliği → istemci IP'si → istek logu (panic kurtarma ve hata ayrıntısı dahil) → rotalar.
	return withRequestID(d.resolveClientIP(d.requestLog(mux)))
}
