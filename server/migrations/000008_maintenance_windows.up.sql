-- 000008: Bakım pencereleri. Bir pencere sürerken kapsamındaki sunucuların alert'leri kaydedilir ama bildirimi
-- gönderilmez; pencere bitince hâlâ açık olanlar bildirilir. Yalnızca ekleme yapılır: pencereler, kapsamları ve tek bir
-- tekrarın istisnaları için yeni tablolar, alerts'e bildirimin ertelendiğini gösteren bayrak, app_settings'e kurulumun
-- saat dilimi ve iki izin.
--
-- Güncelleme sırasında hâlâ çalışan eski bir server süreci bu şemayla çalışır (yeni tabloları ve sütunları bilmez;
-- notify_pending varsayılanı false). Eski bir binary ise bu veritabanıyla yeniden BAŞLATILAMAZ (migrate, bilmediği bir
-- migration görünce açılmayı reddeder); geri dönüş .down.sql ya da yedekle (docs/DISTRIBUTION.md §8.3).

-- ---------------------------------------------------------------- pencereler
-- recurrence 'once': starts_at–ends_at mutlak aralıktır (UTC saklanır). Diğerleri kurulumun saat dilimine göre tekrar
-- eder: valid_from gününden (sayımın çapası) başlayarak her repeat_every gün / hafta / ayda bir, gün içinde
-- start_minute'te başlar ve duration_minutes sürer (gece yarısını geçebilir). Haftalıkta günler weekdays bit maskesidir
-- (bit 0 = Pazartesi … bit 6 = Pazar). Aylıkta ya ayın günü (month_day: 1–28, -1 = son gün) ya da ayın n'inci haftanın
-- günü (month_week: 1–4, -1 = son; month_weekday: 1 = Pazartesi … 7 = Pazar) verilir. ended_at "pencereyi bitir"
-- anıdır: seriyi kapatır, süren tekrarı da o anda bitirir.
CREATE TABLE maintenance_windows (
    id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    title            TEXT NOT NULL CHECK (length(btrim(title)) BETWEEN 1 AND 200),
    recurrence       TEXT NOT NULL CHECK (recurrence IN ('once', 'daily', 'weekly', 'monthly')),
    starts_at        TIMESTAMPTZ,
    ends_at          TIMESTAMPTZ,
    start_minute     SMALLINT CHECK (start_minute BETWEEN 0 AND 1439),
    duration_minutes INTEGER,
    repeat_every     SMALLINT NOT NULL DEFAULT 1,
    weekdays         SMALLINT CHECK (weekdays BETWEEN 1 AND 127),
    month_day        SMALLINT CHECK (month_day BETWEEN 1 AND 28 OR month_day = -1),
    month_week       SMALLINT CHECK (month_week BETWEEN 1 AND 4 OR month_week = -1),
    month_weekday    SMALLINT CHECK (month_weekday BETWEEN 1 AND 7),
    valid_from       DATE,
    valid_until      DATE,
    ended_at         TIMESTAMPTZ,
    created_by       UUID REFERENCES users (id) ON DELETE SET NULL,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT now(),

    -- Tek seferlik pencerede yalnızca mutlak aralık, tekrarlıda yalnızca tekrar alanları dolu olur.
    CONSTRAINT maintenance_windows_shape_chk CHECK (CASE WHEN recurrence = 'once'
        THEN starts_at IS NOT NULL AND ends_at IS NOT NULL
             AND start_minute IS NULL AND duration_minutes IS NULL AND valid_from IS NULL AND valid_until IS NULL
        ELSE starts_at IS NULL AND ends_at IS NULL
             AND start_minute IS NOT NULL AND duration_minutes IS NOT NULL AND valid_from IS NOT NULL END),
    CONSTRAINT maintenance_windows_range_chk CHECK (ends_at > starts_at),
    CONSTRAINT maintenance_windows_valid_range_chk CHECK (valid_until >= valid_from),
    -- Süre: günlükte en çok 24 saat, haftalık ve aylıkta en çok 7 gün.
    CONSTRAINT maintenance_windows_duration_chk CHECK (
        duration_minutes BETWEEN 1 AND CASE recurrence WHEN 'daily' THEN 1440 ELSE 10080 END),
    -- Aralık: günlükte en çok 30, haftalık ve aylıkta en çok 12.
    CONSTRAINT maintenance_windows_repeat_every_chk CHECK (
        repeat_every BETWEEN 1 AND CASE recurrence WHEN 'once' THEN 1 WHEN 'daily' THEN 30 ELSE 12 END),
    CONSTRAINT maintenance_windows_weekdays_chk CHECK ((recurrence = 'weekly') = (weekdays IS NOT NULL)),
    -- Aylıkta ayın günü ile ayın n'inci haftanın günü birlikte verilmez; biri zorunludur.
    CONSTRAINT maintenance_windows_monthly_chk CHECK (
        (recurrence = 'monthly') = (month_day IS NOT NULL OR month_week IS NOT NULL)
        AND NOT (month_day IS NOT NULL AND month_week IS NOT NULL)
        AND (month_week IS NULL) = (month_weekday IS NULL))
);

-- Kapsam: seçilen sunucular ve organizasyonlar. Organizasyon yalnızca doğrudan bağlı sunucularını kapsar (alt
-- organizasyonlar ayrıca seçilir); kapsam alert anında değerlendirilir, sonradan eklenen sunucu da girer.
CREATE TABLE maintenance_window_hosts (
    window_id UUID NOT NULL REFERENCES maintenance_windows (id) ON DELETE CASCADE,
    host_id   UUID NOT NULL REFERENCES hosts (id) ON DELETE CASCADE,
    PRIMARY KEY (window_id, host_id)
);
CREATE INDEX maintenance_window_hosts_host_idx ON maintenance_window_hosts (host_id);

CREATE TABLE maintenance_window_orgs (
    window_id       UUID NOT NULL REFERENCES maintenance_windows (id) ON DELETE CASCADE,
    organization_id UUID NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    PRIMARY KEY (window_id, organization_id)
);
CREATE INDEX maintenance_window_orgs_org_idx ON maintenance_window_orgs (organization_id);

-- Tek bir tekrarın istisnası ("sıradaki tekrarı atla", "bu tekrarı bitir"): occurrence_start o tekrarın başladığı
-- (başlayacağı) andır. ended_at NULL ise tekrar atlanmıştır, doluysa o anda erken bitirilmiştir.
CREATE TABLE maintenance_occurrence_overrides (
    window_id        UUID NOT NULL REFERENCES maintenance_windows (id) ON DELETE CASCADE,
    occurrence_start TIMESTAMPTZ NOT NULL,
    ended_at         TIMESTAMPTZ,
    created_by       UUID REFERENCES users (id) ON DELETE SET NULL,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (window_id, occurrence_start)
);

-- ---------------------------------------------------------------- ertelenen bildirimler
-- Bir olayın (açılma, seviye değişimi) bildirimi bakım yüzünden gönderilmediyse true olur. Sunucu bakımdan çıkınca
-- hâlâ aktif olan alert'in güncel durumu bildirilir ve bayrak temizlenir; alert çözülünce de temizlenir.
ALTER TABLE alerts ADD COLUMN notify_pending BOOLEAN NOT NULL DEFAULT false;
CREATE INDEX alerts_notify_pending_idx ON alerts (host_id) WHERE notify_pending;

-- ---------------------------------------------------------------- saat dilimi
-- Kurulumun saat dilimi (IANA adı, ör. Europe/Istanbul): tekrarlı bakım pencereleri buna göre hesaplanır. Boş:
-- server sürecinin saat dilimi (TZ), o da yoksa UTC. Geçerliliğini server denetler.
ALTER TABLE app_settings ADD COLUMN timezone TEXT NOT NULL DEFAULT '' CHECK (length(timezone) <= 64);

-- ---------------------------------------------------------------- izinler
INSERT INTO permissions (key, description) VALUES
    ('maintenance.view',   'Bakım pencerelerini görme'),
    ('maintenance.manage', 'Bakım penceresi ekleme, düzenleme, bitirme ve silme');

INSERT INTO role_permissions (role, permission_key) VALUES
    ('super_admin', 'maintenance.view'),
    ('super_admin', 'maintenance.manage'),
    ('org_admin',   'maintenance.view'),
    ('org_admin',   'maintenance.manage'),
    ('operator',    'maintenance.view');
