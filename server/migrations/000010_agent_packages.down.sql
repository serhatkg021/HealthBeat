-- Geri alma: agent paket kayıtları, indirme günlüğü, izin ve ayar silinir. Diskteki paket dosyalarına dokunulmaz.
ALTER TABLE app_settings DROP COLUMN agent_download_enabled;
DELETE FROM permissions WHERE key = 'agent.package.manage';
DROP TABLE agent_package_downloads;
DROP TABLE agent_packages;
