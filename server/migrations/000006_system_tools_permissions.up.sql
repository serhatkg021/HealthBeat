-- 000006: Sistem Araçları (panel) izinleri. Kuyruk durumu, cache durumu ve log analizi ayrı izinlerle korunur; üçü de
-- varsayılan olarak yalnızca super_admin'dedir.
--
-- Yalnızca satır eklenir: güncelleme sırasında hâlâ çalışan eski bir server süreci bu şemayla çalışır (izinleri bilmez,
-- kullanmaz). Eski bir binary ise bu veritabanıyla yeniden BAŞLATILAMAZ (migrate, bilmediği bir migration görünce
-- açılmayı reddeder); geri dönüş .down.sql ya da yedekle (docs/DISTRIBUTION.md §8.3).
INSERT INTO permissions (key, description) VALUES
    ('system.queue.view', 'Sistem Araçları: bildirim kuyruğunun durumunu görme'),
    ('system.cache.view', 'Sistem Araçları: server''ın bellekteki durumunu (cache) görme'),
    ('system.logs.view',  'Sistem Araçları: server log dosyalarını görme');

INSERT INTO role_permissions (role, permission_key) VALUES
    ('super_admin', 'system.queue.view'),
    ('super_admin', 'system.cache.view'),
    ('super_admin', 'system.logs.view');
