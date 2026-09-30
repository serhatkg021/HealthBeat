-- Çalışma zamanı ayarları ve bildirim kanalları (server 2.0.0).
--   * app_settings: env'den taşınan işletim ayarları (agent sürüm politikası, saklama süreleri, oturum süreleri, hız
--     sınırları, loglama, panel adresi). Tek satırdır; varsayılanlar sütunların DEFAULT'ları, sınırlar CHECK'lerdir
--     ("varsayılana dön" = SET sütun = DEFAULT).
--   * notification_channels: sistem düzeyindeki bildirim kanalları (kod bir kanalı desteklediğinde satırı buraya eklenir;
--     şimdilik yalnızca e-posta). Ayarı yapılmamış ya da kapalı kanal kurallarda seçilemez.
--   * notification_owners: her alert'in bildirimini alan sistem sahipleri; panel kullanıcısı olmaları gerekmez.
--     Organizasyon ve sunucu kuralları bunlara ek alıcılardır (bkz. docs/MIMARI.md bölüm 8).
--
-- Güncelleme sırasında hâlâ çalışan eski bir server süreci bu şemayla çalışır: yeni tabloları bilmez, kurallara yalnızca
-- 'email' yazar (yeni yabancı anahtarı sağlar) ve kuyruğa çok alıcılı satır yazmaya devam edebilir (şema bunu hâlâ
-- kabul eder). Eski bir binary ise bu veritabanıyla yeniden BAŞLATILAMAZ (migrate, bilmediği bir migration görünce
-- açılmayı reddeder); geri dönüş .down.sql ya da yedekle (docs/DISTRIBUTION.md §8.3).

-- ---------------------------------------------------------------- app_settings
CREATE TABLE app_settings (
    id                                  SMALLINT PRIMARY KEY DEFAULT 1,
    -- Agent sürüm politikası (yalnızca bilgilendirir; bkz. docs/COMPATIBILITY.md). NULL latest: politika yok, her agent
    -- güncel sayılır; NULL min: "desteklenmiyor" durumu üretilmez. min ≤ latest kontrolü uygulamadadır.
    latest_agent_version                TEXT DEFAULT '1.0.0',
    min_supported_agent_version         TEXT,
    -- Saklama süreleri (gün); 0 = sonsuza dek sakla.
    metrics_retention_days              INTEGER NOT NULL DEFAULT 30,
    audit_retention_days                INTEGER NOT NULL DEFAULT 0,
    resolved_alert_retention_days       INTEGER NOT NULL DEFAULT 0,
    -- Oturum süreleri (saniye).
    access_token_ttl_seconds            INTEGER NOT NULL DEFAULT 900,
    refresh_token_ttl_seconds           INTEGER NOT NULL DEFAULT 604800,
    -- Hız sınırları (dakikada); 0 = kapalı.
    rate_limit_auth_failures_per_minute INTEGER NOT NULL DEFAULT 10,
    rate_limit_ingest_per_minute        INTEGER NOT NULL DEFAULT 120,
    -- Panelin kullanıcıya görünen adresi (scheme://host[:port]); boş = e-posta ile şifre sıfırlama kapalı. Biçimi
    -- uygulamada doğrulanır.
    panel_base_url                      TEXT NOT NULL DEFAULT '',
    -- Loglama. Açılış ve migration logları veritabanı okunmadan yazıldığı için her zaman info seviyesindedir.
    log_level                           TEXT NOT NULL DEFAULT 'info',
    log_error_body_bytes                INTEGER NOT NULL DEFAULT 4096,
    log_file_max_age_days               INTEGER NOT NULL DEFAULT 14,
    log_file_max_total_mb               INTEGER NOT NULL DEFAULT 1024,
    updated_at                          TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_by                          UUID REFERENCES users (id) ON DELETE SET NULL,
    CONSTRAINT app_settings_single_row_chk CHECK (id = 1),
    CONSTRAINT app_settings_latest_agent_version_chk
        CHECK (latest_agent_version ~ '^[0-9]+\.[0-9]+\.[0-9]+([-+][0-9A-Za-z.-]+)?$'),
    CONSTRAINT app_settings_min_supported_agent_version_chk
        CHECK (min_supported_agent_version ~ '^[0-9]+\.[0-9]+\.[0-9]+([-+][0-9A-Za-z.-]+)?$'),
    CONSTRAINT app_settings_retention_chk
        CHECK (metrics_retention_days >= 0 AND audit_retention_days >= 0 AND resolved_alert_retention_days >= 0),
    CONSTRAINT app_settings_access_token_ttl_chk CHECK (access_token_ttl_seconds BETWEEN 60 AND 86400),
    CONSTRAINT app_settings_refresh_token_ttl_chk
        CHECK (refresh_token_ttl_seconds BETWEEN 3600 AND 7776000 AND refresh_token_ttl_seconds > access_token_ttl_seconds),
    CONSTRAINT app_settings_rate_limit_chk
        CHECK (rate_limit_auth_failures_per_minute >= 0 AND rate_limit_ingest_per_minute >= 0),
    CONSTRAINT app_settings_log_level_chk CHECK (log_level IN ('debug', 'info', 'warn', 'error')),
    CONSTRAINT app_settings_log_error_body_bytes_chk CHECK (log_error_body_bytes BETWEEN 0 AND 1048576),
    CONSTRAINT app_settings_log_file_chk CHECK (log_file_max_age_days >= 1 AND log_file_max_total_mb >= 1)
);
INSERT INTO app_settings DEFAULT VALUES;

-- ---------------------------------------------------------------- notification_channels
CREATE TABLE notification_channels (
    channel         TEXT PRIMARY KEY,                -- notification_routes.channel ve notification_outbox.channel değerleri
    provider        TEXT NOT NULL,                   -- kanalın gerçekleştirimi (email → smtp)
    enabled         BOOLEAN NOT NULL DEFAULT false,  -- false: "ayar gerekli" ya da elle kapatıldı; gönderim yapılmaz
    config          JSONB NOT NULL DEFAULT '{}',     -- sır olmayan ayarlar; biçimini kanalın göndericisi doğrular
    secret_enc      TEXT,                            -- şifre/token (secretbox, aad = "notification_channels:<channel>"); API asla geri döndürmez
    owner_min_level TEXT NOT NULL DEFAULT 'warning', -- sistem sahiplerine bu seviye ve üzeri gönderilir
    verified_at     TIMESTAMPTZ,                     -- son başarılı deneme gönderimi
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_by      UUID REFERENCES users (id) ON DELETE SET NULL,
    CONSTRAINT notification_channels_owner_min_level_chk CHECK (owner_min_level IN ('info', 'warning', 'critical')),
    CONSTRAINT notification_channels_config_object_chk CHECK (jsonb_typeof(config) = 'object')
);
INSERT INTO notification_channels (channel, provider, config) VALUES ('email', 'smtp', '{"port": 587}');

-- ---------------------------------------------------------------- notification_owners
CREATE TABLE notification_owners (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name          TEXT NOT NULL,
    email         TEXT,
    phone         TEXT,
    email_enabled BOOLEAN NOT NULL DEFAULT true,     -- e-posta kanalından bildirim alsın mı
    sms_enabled   BOOLEAN NOT NULL DEFAULT true,     -- SMS kanalından bildirim alsın mı
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT notification_owners_email_lowercase_chk CHECK (email = lower(email)),
    CONSTRAINT notification_owners_reachable_chk CHECK (email IS NOT NULL OR phone IS NOT NULL)
);
CREATE UNIQUE INDEX notification_owners_email_uidx ON notification_owners (email) WHERE email IS NOT NULL;

-- ---------------------------------------------------------------- notification_routes: kanal artık notification_channels'tan
-- Ön kontrol: API yalnızca 'email' kuralı kabul ettiği için başka kanala bağlı kural olmamalı. Varsa sessizce silinmez;
-- migration durur ve ne yapılacağını söyler (transaction geri alınır, veritabanı değişmez).
DO $$
DECLARE
    summary TEXT;
    total   BIGINT;
BEGIN
    SELECT string_agg(channel || ': ' || n, ', ' ORDER BY channel), sum(n)
      INTO summary, total
      FROM (SELECT channel, count(*) AS n
              FROM notification_routes
             WHERE channel NOT IN (SELECT channel FROM notification_channels)
             GROUP BY channel) c;
    IF total > 0 THEN
        RAISE EXCEPTION '% notification rules use a channel that is not available (%); delete them (DELETE FROM notification_routes WHERE channel <> ''email'') and restart', total, summary;
    END IF;
END $$;

ALTER TABLE notification_routes DROP CONSTRAINT notification_routes_channel_check;
ALTER TABLE notification_routes
    ADD CONSTRAINT notification_routes_channel_fkey FOREIGN KEY (channel) REFERENCES notification_channels (channel);

-- ---------------------------------------------------------------- notification_outbox: alıcı başına bir satır
-- Kişiye giden kanallarda her alıcı ayrı ileti alır (birbirini görmez). Bekleyen çok alıcılı alert satırları alıcı başına
-- satırlara bölünür; gönderilmiş/vazgeçilmiş satırlar geçmiş olarak olduğu gibi kalır.
INSERT INTO notification_outbox (id, kind, channel, recipients, subject, body, body_sealed, alert_id, alert_event,
                                 alert_level, request_id, attempts, next_attempt_at, expires_at, last_error, created_at)
SELECT gen_random_uuid(), o.kind, o.channel, ARRAY[r.recipient], o.subject, o.body, o.body_sealed, o.alert_id,
       o.alert_event, o.alert_level, o.request_id, o.attempts, o.next_attempt_at, o.expires_at, o.last_error, o.created_at
  FROM notification_outbox o
 CROSS JOIN LATERAL unnest(o.recipients) AS r(recipient)
 WHERE o.kind = 'alert' AND o.sent_at IS NULL AND o.failed_at IS NULL AND cardinality(o.recipients) > 1;

DELETE FROM notification_outbox
 WHERE kind = 'alert' AND sent_at IS NULL AND failed_at IS NULL AND cardinality(recipients) > 1;

-- ---------------------------------------------------------------- izinler
INSERT INTO permissions (key, description) VALUES
    ('settings.view',   'Sistem ayarlarını görme'),
    ('settings.manage', 'Sistem ayarlarını, bildirim kanallarını ve sistem sahiplerini düzenleme');

INSERT INTO role_permissions (role, permission_key) VALUES
    ('super_admin', 'settings.view'),
    ('super_admin', 'settings.manage');
