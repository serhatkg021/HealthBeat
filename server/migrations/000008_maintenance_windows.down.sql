-- Geri alma: bakım pencereleri, kapsamları ve istisnaları, ertelenen bildirim bayrağı, saat dilimi ayarı ve bakım izinleri
-- silinir (role_permissions satırları permissions'a bağlı olarak silinir). Ertelenmiş bildirimler gönderilmeden kalır.
DELETE FROM permissions WHERE key IN ('maintenance.view', 'maintenance.manage');

ALTER TABLE app_settings DROP COLUMN timezone;

DROP INDEX alerts_notify_pending_idx;
ALTER TABLE alerts DROP COLUMN notify_pending;

DROP TABLE maintenance_occurrence_overrides;
DROP TABLE maintenance_window_orgs;
DROP TABLE maintenance_window_hosts;
DROP TABLE maintenance_windows;
