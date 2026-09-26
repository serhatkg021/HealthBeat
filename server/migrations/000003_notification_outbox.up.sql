-- Bildirim kuyruğu (outbox): alert bildirimleri ve hesap e-postaları (şifre sıfırlama, şifre değişti) gönderilmeden önce
-- buraya yazılır; işçi satırları alıp gönderir, başarısızlıkta geri çekilerek yeniden dener. Server yeniden başlasa da
-- bekleyen bildirim kaybolmaz ve her bildirimin teslim durumu sorgulanabilir (bkz. docs/MIMARI.md bölüm 8).
--
-- Yalnızca yeni bir tablo eklenir: güncelleme sırasında hâlâ çalışan eski bir server süreci bu şemayla çalışır (tabloyu
-- bilmez, bellek içi kuyruğuyla göndermeye devam eder). Eski bir binary ise bu veritabanıyla yeniden BAŞLATILAMAZ
-- (migrate, bilmediği bir migration görünce açılmayı reddeder); geri dönüş .down.sql ya da yedekle (docs/DISTRIBUTION.md §8.3).
CREATE TABLE notification_outbox (
    id              UUID PRIMARY KEY,
    kind            TEXT NOT NULL CHECK (kind IN ('alert', 'password_reset', 'password_changed')),
    channel         TEXT NOT NULL,                   -- notification_routes.channel ile aynı değerler (email, sms…)
    recipients      TEXT[] NOT NULL,                 -- kanalın adres biçiminde (e-posta adresi, telefon…)
    subject         TEXT NOT NULL,
    body            TEXT,                            -- düz metin gövde
    body_sealed     TEXT,                            -- şifreli gövde (secretbox, aad = id): şifre sıfırlama bağlantısı
    alert_id        UUID REFERENCES alerts (id) ON DELETE SET NULL,
    alert_event     TEXT CHECK (alert_event IN ('opened', 'level_changed', 'resolved')), -- bildirimi doğuran alert olayı
    alert_level     TEXT CHECK (alert_level IN ('info', 'warning', 'critical')),         -- o andaki alert seviyesi
    request_id      TEXT,                            -- bildirimi doğuran isteğin log kimliği (teslim satırları da taşır)
    attempts        INT NOT NULL DEFAULT 0,
    next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT now(), -- bir sonraki deneme; alınan satırda kira bitişi
    expires_at      TIMESTAMPTZ,                     -- bu andan sonra gönderilmez (ör. süresi dolan sıfırlama bağlantısı)
    last_error      TEXT,
    sent_at         TIMESTAMPTZ,
    failed_at       TIMESTAMPTZ,                     -- vazgeçildi: deneme sınırı, süre dolması ya da kalıcı hata
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    -- Alert bildiriminin olayı ve seviyesi vardır; hesap e-postalarının yoktur.
    CONSTRAINT notification_outbox_alert_event_chk CHECK ((kind = 'alert') = (alert_event IS NOT NULL AND alert_level IS NOT NULL)),
    -- Bekleyen satırın tam olarak bir gövdesi vardır; bitmiş satırın şifreli gövdesi silinir (bağlantı tabloda kalmaz).
    CONSTRAINT notification_outbox_body_chk CHECK (
        (sent_at IS NULL AND failed_at IS NULL AND (body IS NULL) <> (body_sealed IS NULL))
        OR ((sent_at IS NOT NULL OR failed_at IS NOT NULL) AND body_sealed IS NULL)
    )
);
-- İşçinin "teslim zamanı gelmiş" taraması.
CREATE INDEX notification_outbox_due_idx ON notification_outbox (next_attempt_at) WHERE sent_at IS NULL AND failed_at IS NULL;
-- Eski bitmiş satırların temizliği ve alert başına teslim durumu.
CREATE INDEX notification_outbox_created_at_idx ON notification_outbox (created_at);
CREATE INDEX notification_outbox_alert_id_idx ON notification_outbox (alert_id) WHERE alert_id IS NOT NULL;
