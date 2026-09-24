package httpapi

import "net/http"

func (d *Deps) Router() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})

	mux.HandleFunc("POST /api/v1/auth/login", d.handleLogin)
	mux.HandleFunc("POST /api/v1/auth/refresh", d.handleRefresh)
	mux.HandleFunc("POST /api/v1/auth/logout", d.handleLogout)
	// Şifre sıfırlama (kimlik doğrulamasız; e-posta ile): bkz. password_reset_handlers.go.
	mux.HandleFunc("GET /api/v1/auth/options", d.handleAuthOptions)
	mux.HandleFunc("POST /api/v1/auth/forgot-password", d.handleForgotPassword)
	mux.HandleFunc("POST /api/v1/auth/reset-password", d.handleResetPassword)

	mux.HandleFunc("GET /api/v1/me", d.requireAuthEvenIfPasswordChangeDue(d.handleGetMe))
	mux.HandleFunc("POST /api/v1/me/password", d.requireAuthEvenIfPasswordChangeDue(d.handleChangeOwnPassword))
	mux.HandleFunc("GET /api/v1/me/hosts", d.requireAuth(d.handleGetMyHosts))
	mux.HandleFunc("GET /api/v1/meta", d.requireAuth(d.handleMeta))

	mux.HandleFunc("GET /api/v1/organizations", d.requirePermission("organization.view", d.handleListOrganizations))
	mux.HandleFunc("POST /api/v1/organizations", d.requirePermission("organization.create", d.handleCreateOrganization))
	mux.HandleFunc("GET /api/v1/organizations/{id}", d.requirePermission("organization.view", d.handleGetOrganization))
	mux.HandleFunc("PUT /api/v1/organizations/{id}", d.requirePermission("organization.update", d.handleUpdateOrganization))
	mux.HandleFunc("DELETE /api/v1/organizations/{id}", d.requirePermission("organization.delete", d.handleDeleteOrganization))

	mux.HandleFunc("GET /api/v1/organizations/{id}/contacts", d.requirePermission("contact.view", d.handleListContacts))
	mux.HandleFunc("POST /api/v1/organizations/{id}/contacts", d.requirePermission("contact.edit", d.handleCreateContact))
	mux.HandleFunc("PUT /api/v1/contacts/{id}", d.requirePermission("contact.edit", d.handleUpdateContact))
	mux.HandleFunc("DELETE /api/v1/contacts/{id}", d.requirePermission("contact.edit", d.handleDeleteContact))

	mux.HandleFunc("GET /api/v1/organizations/{id}/notification-routes", d.requirePermission("notification.view", d.handleListOrganizationRoutes))
	mux.HandleFunc("GET /api/v1/organizations/{id}/notification-recipients", d.requirePermission("notification.view", d.handleOrganizationRecipientCandidates))
	mux.HandleFunc("GET /api/v1/hosts/{id}/notification-routes", d.requirePermission("notification.view", d.handleListHostRoutes))
	mux.HandleFunc("GET /api/v1/hosts/{id}/notification-recipients", d.requirePermission("notification.view", d.handleHostRecipientCandidates))
	mux.HandleFunc("POST /api/v1/notification-routes", d.requirePermission("notification.edit", d.handleCreateRoute))
	mux.HandleFunc("PUT /api/v1/notification-routes/{id}", d.requirePermission("notification.edit", d.handleUpdateRoute))
	mux.HandleFunc("DELETE /api/v1/notification-routes/{id}", d.requirePermission("notification.edit", d.handleDeleteRoute))

	mux.HandleFunc("GET /api/v1/users", d.requirePermission("user.view", d.handleListUsers))
	mux.HandleFunc("POST /api/v1/users", d.requirePermission("user.create", d.handleCreateUser))
	mux.HandleFunc("GET /api/v1/users/{id}", d.requirePermission("user.view", d.handleGetUser))
	mux.HandleFunc("PUT /api/v1/users/{id}", d.requirePermission("user.update", d.handleUpdateUser))
	mux.HandleFunc("DELETE /api/v1/users/{id}", d.requirePermission("user.delete", d.handleDeleteUser))

	mux.HandleFunc("GET /api/v1/users/{id}/organizations", d.requirePermission("user.view", d.handleGetUserOrganizations))
	mux.HandleFunc("PUT /api/v1/users/{id}/organizations", d.requirePermission("user.update", d.handleSetUserOrganizations))

	mux.HandleFunc("GET /api/v1/users/{id}/hosts", d.requirePermission("user.view", d.handleGetUserHosts))
	mux.HandleFunc("PUT /api/v1/users/{id}/hosts", d.requirePermission("user.update", d.handleSetUserHosts))
	mux.HandleFunc("POST /api/v1/users/{id}/hosts/by-organization", d.requirePermission("user.update", d.handleAddUserHostsByOrganization))

	mux.HandleFunc("POST /api/v1/hosts", d.requirePermission("host.create", d.handleCreateHost))
	mux.HandleFunc("GET /api/v1/hosts/{id}", d.requirePermission("host.view", d.handleGetHost))
	mux.HandleFunc("PUT /api/v1/hosts/{id}", d.requirePermission("host.update", d.handleUpdateHost))
	mux.HandleFunc("DELETE /api/v1/hosts/{id}", d.requirePermission("host.delete", d.handleDeleteHost))
	mux.HandleFunc("POST /api/v1/hosts/{id}/rotate-credentials", d.requirePermission("host.update", d.handleRotateHostCredentials))
	mux.HandleFunc("GET /api/v1/organizations/{id}/hosts", d.requirePermission("host.view", d.handleListOrganizationHosts))

	mux.HandleFunc("POST /api/v1/metrics", d.requireHostAuth(d.handleIngestMetrics))

	mux.HandleFunc("GET /api/v1/thresholds", d.requirePermission("threshold.view", d.handleListThresholds))
	mux.HandleFunc("POST /api/v1/thresholds", d.requirePermission("threshold.edit", d.handleCreateThreshold))
	mux.HandleFunc("GET /api/v1/thresholds/{id}", d.requirePermission("threshold.view", d.handleGetThreshold))
	mux.HandleFunc("PUT /api/v1/thresholds/{id}", d.requirePermission("threshold.edit", d.handleUpdateThreshold))
	mux.HandleFunc("DELETE /api/v1/thresholds/{id}", d.requirePermission("threshold.edit", d.handleDeleteThreshold))

	mux.HandleFunc("GET /api/v1/alerts", d.requirePermission("alert.view", d.handleListAlerts))
	mux.HandleFunc("POST /api/v1/alerts/{id}/acknowledge", d.requirePermission("alert.acknowledge", d.handleAcknowledgeAlert))

	mux.HandleFunc("GET /api/v1/hosts/{id}/metrics", d.requirePermission("host.view", d.handleGetHostMetrics))
	mux.HandleFunc("GET /api/v1/hosts/{id}/thresholds", d.requirePermission("threshold.view", d.handleGetHostThresholds))
	mux.HandleFunc("PUT /api/v1/hosts/{id}/thresholds", d.requirePermission("threshold.edit", d.handleSetHostThresholds))
	mux.HandleFunc("GET /api/v1/hosts/{id}/disk-alerts", d.requirePermission("host.view", d.handleGetDiskAlerts))
	mux.HandleFunc("PUT /api/v1/hosts/{id}/disk-alerts", d.requirePermission("host.update", d.handleSetDiskAlerts))
	mux.HandleFunc("GET /api/v1/hosts/{id}/docker", d.requirePermission("host.view", d.handleGetHostDocker))

	mux.HandleFunc("GET /api/v1/audit-logs", d.requirePermission("audit.view", d.handleListAuditLogs))

	mux.HandleFunc("GET /api/v1/dashboard/summary", d.requirePermission("dashboard.view", d.handleDashboardSummary))
	mux.HandleFunc("GET /api/v1/dashboard/overview", d.requirePermission("dashboard.view", d.handleDashboardOverview))

	return d.resolveClientIP(loggingMiddleware(mux))
}
