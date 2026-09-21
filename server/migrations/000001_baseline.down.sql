-- Baseline'ı geri alır (tüm tabloları siler). Geliştirme/deneme içindir; asla otomatik çalışmaz.
DROP TABLE IF EXISTS password_reset_tokens, refresh_tokens, audit_logs, notification_routes,
    host_custom_thresholds, threshold_defaults, alerts, docker_containers, metrics,
    host_custom_mounts_alerts, host_inventory, host_status, user_hosts, hosts,
    user_organizations, organization_contacts, organizations, users,
    role_permissions, permissions, roles CASCADE;
DROP FUNCTION IF EXISTS organizations_prevent_cycle();
