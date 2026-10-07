-- 000007: Protokol 4 (agent'ın sistem sağlığı ve performans verileri). Yalnızca ekleme yapılır: zaman serisi için
-- metrics'e üç JSONB sütun, anlık durumlar için host_status.system_state, servisler ve izlenen servisler için iki tablo,
-- Docker sağlık sütunları, eşiksiz alert'lerin ayarı için status_alert_rules, süre koşullu alert'ler için alert_pending
-- ve eşiklere duration_seconds. Yeni sütunların hepsi
-- boş bırakılabilir; eski agent'ların raporları onları boş bırakır.
--
-- Kısıt değişikliği: alerts.alert_type ve eşik tablolarının metric_type / subject CHECK'leri yeni türleri kabul edecek
-- şekilde GENİŞLETİLİR (yalnızca izin verilen değerler artar). Güncelleme sırasında hâlâ çalışan eski bir server süreci
-- bu şemayla çalışır: yeni sütunları ve tabloları bilmez, eski türleri yazmaya devam eder (docs/COMPATIBILITY.md §4).
-- Eski bir binary ise bu veritabanıyla yeniden BAŞLATILAMAZ (migrate, bilmediği bir migration görünce açılmayı
-- reddeder); geri dönüş .down.sql ya da yedekle (docs/DISTRIBUTION.md §8.3).

-- ---------------------------------------------------------------- zaman serisi
-- system_json: CPU ayrıntısı (iowait, steal, G/Ç'de bekleyen süreç), bellek ayrıntısı, PSI ve TCP; disk_io_json: fiziksel
-- disk başına G/Ç; net_io_json: arayüz başına trafik. Değerler ölçüldüğü gibi saklanır (yuvarlama panelde).
ALTER TABLE metrics
    ADD COLUMN system_json  JSONB,
    ADD COLUMN disk_io_json JSONB,
    ADD COLUMN net_io_json  JSONB;

-- ---------------------------------------------------------------- anlık durumlar
-- Sıcaklık, RAID, kapasite, süreçler, bekleyen güncellemeler, saat senkronu ve OOM sayacı: her rapor kaydı son
-- bildirilenle değiştirir (geçmişi yok). Raporda olmayan bölüm "bilinmiyor"dur.
ALTER TABLE host_status ADD COLUMN system_state JSONB;

-- ---------------------------------------------------------------- Docker sağlık durumu
ALTER TABLE docker_containers
    ADD COLUMN health                TEXT CHECK (health IN ('healthy', 'unhealthy', 'starting')), -- healthcheck yoksa NULL
    ADD COLUMN health_failing_streak INTEGER CHECK (health_failing_streak >= 0),
    ADD COLUMN exit_code             INTEGER,
    ADD COLUMN oom_killed            BOOLEAN;

-- ---------------------------------------------------------------- systemd servisleri
-- Agent'ın son bildirdiği servis listesi: tam rapor listeyi değiştirir, kısmi rapor yalnızca gelen servisleri günceller.
CREATE TABLE host_services (
    host_id     UUID NOT NULL REFERENCES hosts (id) ON DELETE CASCADE,
    name        TEXT NOT NULL,
    description TEXT,
    active      TEXT NOT NULL,                       -- active | inactive | failed | activating | deactivating | reloading …
    sub         TEXT,                                -- running | exited | dead | failed | auto-restart …
    since       TIMESTAMPTZ,                         -- bu duruma geçtiği an
    restarts    INTEGER CHECK (restarts >= 0),       -- systemd'nin otomatik yeniden başlatma sayacı
    enabled     TEXT,                                -- enabled | disabled | static …
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),  -- satırın içeriğinin en son DEĞİŞTİĞİ an (değişmeyen rapor yazılmaz)
    restart_history JSONB,                           -- son yeniden başlatmalar ([[unix_sn, sayaç], …]; yeniden başlatma döngüsü alert'i için)
    PRIMARY KEY (host_id, name)
);

-- Alert üretecek servisler, sunucu bazında seçilir (disk seçimi gibi; izin host.update).
CREATE TABLE host_watched_services (
    host_id    UUID NOT NULL REFERENCES hosts (id) ON DELETE CASCADE,
    name       TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (host_id, name)
);

-- ---------------------------------------------------------------- süre koşullu alert'ler
-- Eşiği aşan ama süre koşulu henüz dolmamış durumlar: koşul since'tan beri sürüyorsa alert açılır, koşul kalkınca satır
-- silinir. Anahtar alerts'teki tek aktif alert kuralıyla aynıdır (sunucu + tür + konu).
CREATE TABLE alert_pending (
    host_id    UUID NOT NULL REFERENCES hosts (id) ON DELETE CASCADE,
    alert_type TEXT NOT NULL,
    subject    TEXT,
    level      TEXT NOT NULL CHECK (level IN ('info', 'warning', 'critical')),
    since      TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT alert_pending_key UNIQUE NULLS NOT DISTINCT (host_id, alert_type, subject)
);

-- Durum kuralları: eşiği olmayan alert'lerin (servis çalışmıyor, RAID bozuk …) seviyesi ve süresi. Kapsam genel (iki
-- kimlik de NULL), organizasyon (alt dallara miras kalır) ya da tek sunucudur; en özel olan geçerlidir. Hiç satır yoksa
-- kural kapalıdır: yeni alert türleri ancak açılınca çalışır. level 'off' üst kapsamdaki kuralı bu kapsamda kapatır.
-- duration_seconds: koşul bu kadar sürerse alert açılır (oom_kill'de: bu kadar süre yeni artış olmazsa kapanır); NULL =
-- hemen. Anlık olaylara (container_oom, fs_readonly, reboot_required) süre verilmez.
CREATE TABLE status_alert_rules (
    id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id  UUID REFERENCES organizations (id) ON DELETE CASCADE,
    host_id          UUID REFERENCES hosts (id) ON DELETE CASCADE,
    rule             TEXT NOT NULL CHECK (rule IN (
        'service_failed', 'container_unhealthy', 'container_oom', 'oom_kill', 'fs_readonly', 'raid_degraded',
        'raid_rebuilding', 'time_unsynced', 'time_source', 'reboot_required', 'security_updates')),
    level            TEXT NOT NULL CHECK (level IN ('off', 'info', 'warning', 'critical')),
    duration_seconds INTEGER CHECK (duration_seconds > 0),
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT status_alert_rules_one_scope_chk CHECK (organization_id IS NULL OR host_id IS NULL),
    CONSTRAINT status_alert_rules_duration_chk CHECK (
        duration_seconds IS NULL OR rule NOT IN ('container_oom', 'fs_readonly', 'reboot_required')),
    CONSTRAINT status_alert_rules_key UNIQUE NULLS NOT DISTINCT (organization_id, host_id, rule)
);
CREATE INDEX status_alert_rules_host_idx ON status_alert_rules (host_id) WHERE host_id IS NOT NULL;

-- Eşik ancak bu kadar saniye kesintisiz aşılırsa alert açılır; NULL = hemen.
ALTER TABLE threshold_defaults ADD COLUMN duration_seconds INTEGER CHECK (duration_seconds > 0);
ALTER TABLE host_custom_thresholds ADD COLUMN duration_seconds INTEGER CHECK (duration_seconds > 0);

-- ---------------------------------------------------------------- CHECK genişletme
ALTER TABLE alerts DROP CONSTRAINT alerts_alert_type_check;
ALTER TABLE alerts ADD CONSTRAINT alerts_alert_type_check CHECK (alert_type IN (
    'cpu', 'ram', 'disk', 'docker_restart', 'host_offline', 'disk_missing',
    'service_failed', 'service_restart_loop', 'container_unhealthy', 'container_oom', 'disk_latency', 'oom_kill',
    'fs_readonly', 'raid_degraded', 'temperature', 'time_sync', 'reboot_required', 'security_updates'));

ALTER TABLE threshold_defaults DROP CONSTRAINT threshold_defaults_metric_type_check;
ALTER TABLE threshold_defaults ADD CONSTRAINT threshold_defaults_metric_type_check CHECK (metric_type IN (
    'cpu', 'ram', 'disk', 'docker_restart', 'disk_latency', 'temperature', 'service_restart', 'time_offset'));

ALTER TABLE host_custom_thresholds DROP CONSTRAINT host_custom_thresholds_metric_type_check;
ALTER TABLE host_custom_thresholds ADD CONSTRAINT host_custom_thresholds_metric_type_check CHECK (metric_type IN (
    'cpu', 'ram', 'disk', 'docker_restart', 'disk_latency', 'temperature', 'service_restart', 'time_offset'));

-- Konu (subject): mount, container, disk, sensör ya da servis adı.
ALTER TABLE host_custom_thresholds DROP CONSTRAINT host_custom_thresholds_subject_chk;
ALTER TABLE host_custom_thresholds ADD CONSTRAINT host_custom_thresholds_subject_chk CHECK (
    subject IS NULL OR metric_type IN ('disk', 'docker_restart', 'disk_latency', 'temperature', 'service_restart'));
