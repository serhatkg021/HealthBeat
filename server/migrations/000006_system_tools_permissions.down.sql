-- Geri alma: Sistem Araçları izinleri silinir (role_permissions satırları permissions'a bağlı olarak silinir).
DELETE FROM permissions WHERE key IN ('system.queue.view', 'system.cache.view', 'system.logs.view');
