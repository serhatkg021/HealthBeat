-- HealthBeat v1.0.0 başlangıç şeması. (Geliştirme sürecindeki eski migration'lar bu dosyada tek adımda toplandı;
-- sonraki her şema değişikliği 000002'den başlayan yeni bir migration'dır.) Şemanın anlatımı: docs/VERITABANI.md.
--
-- Kavramlar: organizasyonlar bir ağaç oluşturur (üst şirket → alt şirketler); bir organizasyona atanan yönetici o
-- organizasyonun ve altındaki tüm dalın sunucularını (hosts) yönetir. Bir host, agent'ın çalıştığı izlenen makinedir.

-- ---------------------------------------------------------------- roller ve izinler
CREATE TABLE roles (
    key         TEXT PRIMARY KEY,
    description TEXT NOT NULL
);

CREATE TABLE permissions (
    key         TEXT PRIMARY KEY,
    description TEXT NOT NULL
);

CREATE TABLE role_permissions (
    role           TEXT NOT NULL REFERENCES roles (key) ON DELETE CASCADE,
    permission_key TEXT NOT NULL REFERENCES permissions (key) ON DELETE CASCADE,
    PRIMARY KEY (role, permission_key)
);

INSERT INTO roles (key, description) VALUES
    ('super_admin', 'Her şeyi yönetir: tüm organizasyonlar, kullanıcılar, denetim kaydı.'),
    ('org_admin',   'Atandığı organizasyonların ve altındaki dalın sunucularını, eşiklerini ve alert''lerini yönetir.'),
    ('operator',    'Atandığı sunucuları izler; alert''leri onaylar. Değişiklik yapamaz.');

INSERT INTO permissions (key, description) VALUES
    ('organization.create', 'Organizasyon oluşturma'),
    ('organization.view',   'Organizasyonları görme'),
    ('organization.update', 'Organizasyon düzenleme (ad, adres, üst şirket)'),
    ('organization.delete', 'Organizasyon silme'),
    ('host.create',         'Sunucu ekleme'),
    ('host.view',           'Sunucuları görme'),
    ('host.update',         'Sunucu düzenleme, kimlik bilgisi yenileme'),
    ('host.delete',         'Sunucu silme'),
    ('threshold.view',      'Eşikleri görme'),
    ('threshold.edit',      'Eşik tanımlama ve düzenleme'),
    ('alert.view',          'Alert''leri görme'),
    ('alert.acknowledge',   'Alert onaylama'),
    ('dashboard.view',      'Özet sayfasını görme'),
    ('user.create',         'Kullanıcı oluşturma'),
    ('user.view',           'Kullanıcıları görme'),
    ('user.update',         'Kullanıcı düzenleme ve atama'),
    ('user.delete',         'Kullanıcı silme'),
    ('audit.view',          'Denetim kaydını görme'),
    ('contact.view',        'Organizasyon iletişim kişilerini görme'),
    ('contact.edit',        'İletişim kişisi ekleme, düzenleme, silme'),
    ('notification.view',   'Bildirim kurallarını görme'),
    ('notification.edit',   'Bildirim kuralı ekleme, düzenleme, silme');

INSERT INTO role_permissions (role, permission_key)
SELECT 'super_admin', key FROM permissions;

INSERT INTO role_permissions (role, permission_key) VALUES
    ('org_admin', 'organization.view'),
    ('org_admin', 'host.create'), ('org_admin', 'host.view'), ('org_admin', 'host.update'), ('org_admin', 'host.delete'),
    ('org_admin', 'threshold.view'), ('org_admin', 'threshold.edit'),
    ('org_admin', 'alert.view'), ('org_admin', 'alert.acknowledge'),
    ('org_admin', 'dashboard.view'),
    ('org_admin', 'contact.view'), ('org_admin', 'contact.edit'),
    ('org_admin', 'notification.view'), ('org_admin', 'notification.edit'),
    ('operator', 'host.view'), ('operator', 'threshold.view'),
    ('operator', 'alert.view'), ('operator', 'alert.acknowledge'),
    ('operator', 'dashboard.view'),
    ('operator', 'notification.view');

-- ---------------------------------------------------------------- kullanıcılar
CREATE TABLE users (
    id                   UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    email                TEXT NOT NULL UNIQUE,
    full_name            TEXT,
    password_hash        TEXT NOT NULL,
    role                 TEXT NOT NULL REFERENCES roles (key),
    phone                TEXT,
    -- İki faktörlü doğrulama için hesap bazlı anahtar; TOTP sırrı ve kurtarma kodları 2FA yazılırken eklenir.
    two_factor_enabled   BOOLEAN NOT NULL DEFAULT false,
    two_factor_channel   TEXT CHECK (two_factor_channel IN ('email', 'sms', 'app')),
    must_change_password BOOLEAN NOT NULL DEFAULT false,
    created_at           TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_login_at        TIMESTAMPTZ,
    CONSTRAINT users_email_lowercase_chk CHECK (email = lower(email)),
    CONSTRAINT users_two_factor_channel_chk CHECK (NOT two_factor_enabled OR two_factor_channel IS NOT NULL)
);

-- ---------------------------------------------------------------- organizasyonlar (ağaç)
CREATE TABLE organizations (
    id                     UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    parent_organization_id UUID REFERENCES organizations (id) ON DELETE RESTRICT,
    name                   TEXT NOT NULL,
    address                TEXT,
    created_at             TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT organizations_not_own_parent_chk CHECK (parent_organization_id IS DISTINCT FROM id),
    -- Aynı üst şirketin altında iki alt şirket aynı adı taşıyamaz (üst şirketi olmayanlar da kendi aralarında).
    CONSTRAINT organizations_parent_name_key UNIQUE NULLS NOT DISTINCT (parent_organization_id, name)
);
CREATE INDEX organizations_parent_idx ON organizations (parent_organization_id);

-- Döngü (A → B → A) uygulamada denetlenir, burada da engellenir (savunma derinliği).
CREATE FUNCTION organizations_prevent_cycle() RETURNS trigger AS $$
BEGIN
    IF NEW.parent_organization_id IS NOT NULL AND EXISTS (
        WITH RECURSIVE ancestors AS (
            SELECT id, parent_organization_id FROM organizations WHERE id = NEW.parent_organization_id
            UNION ALL
            SELECT o.id, o.parent_organization_id FROM organizations o JOIN ancestors a ON o.id = a.parent_organization_id
        )
        SELECT 1 FROM ancestors WHERE id = NEW.id
    ) THEN
        RAISE EXCEPTION 'organization tree must not contain a cycle' USING ERRCODE = '23514';
    END IF;
    RETURN NEW;
END
$$ LANGUAGE plpgsql;

CREATE TRIGGER organizations_no_cycle
    BEFORE INSERT OR UPDATE OF parent_organization_id ON organizations
    FOR EACH ROW EXECUTE FUNCTION organizations_prevent_cycle();

-- Organizasyonun sunucu işleri için başvurulacak kişileri (paneli olmayan müşteri yetkilileri dahil).
CREATE TABLE organization_contacts (
    id                 UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id    UUID NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    department         TEXT,
    title              TEXT,
    name               TEXT NOT NULL,
    manager_contact_id UUID,
    phone              TEXT,
    email              TEXT,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT organization_contacts_org_id_key UNIQUE (organization_id, id),
    -- Yönetici aynı organizasyondan olmalı (bileşik yabancı anahtar); yönetici silinirse yalnızca bağ boşalır.
    CONSTRAINT organization_contacts_manager_fkey FOREIGN KEY (organization_id, manager_contact_id)
        REFERENCES organization_contacts (organization_id, id) ON DELETE SET NULL (manager_contact_id),
    CONSTRAINT organization_contacts_not_own_manager_chk CHECK (manager_contact_id IS DISTINCT FROM id),
    CONSTRAINT organization_contacts_reachable_chk CHECK (phone IS NOT NULL OR email IS NOT NULL)
);

-- Erişim: org_admin bir organizasyona atanır (o organizasyonun ve altındaki dalın tamamını yönetir); operator tek tek sunuculara atanır.
CREATE TABLE user_organizations (
    user_id         UUID NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    organization_id UUID NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    PRIMARY KEY (user_id, organization_id)
);
CREATE INDEX user_organizations_organization_id_idx ON user_organizations (organization_id);

-- ---------------------------------------------------------------- sunucular (izlenen makineler)
-- hosts: kimlik ve bağlantı ayarı (nadiren değişir). Anlık durum host_status'ta, envanter host_inventory'de.
CREATE TABLE hosts (
    id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id   UUID NOT NULL REFERENCES organizations (id) ON DELETE RESTRICT,
    title             TEXT NOT NULL,                 -- panelde görünen ad (makinenin bildirdiği hostname host_inventory'dedir)
    ip                INET NOT NULL,
    mode              TEXT NOT NULL CHECK (mode IN ('push', 'pull')),
    interval_seconds  INTEGER NOT NULL CHECK (interval_seconds > 0),
    api_token_hash    TEXT,
    pull_port         INTEGER CHECK (pull_port IS NULL OR (pull_port > 0 AND pull_port <= 65535)),
    pull_endpoint     TEXT,
    pull_secret_enc   TEXT,                          -- şifreli saklanır (secretbox)
    -- Disk alert'i: true = agent'ın raporladığı tüm mount'lar; false = yalnızca host_custom_mounts_alerts'teki mount'lar.
    all_mounts_alert  BOOLEAN NOT NULL DEFAULT true,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT hosts_org_title_key UNIQUE (organization_id, title),
    CONSTRAINT hosts_pull_fields_chk CHECK (mode <> 'pull' OR (pull_port IS NOT NULL AND pull_endpoint IS NOT NULL AND pull_secret_enc IS NOT NULL)),
    CONSTRAINT hosts_push_fields_chk CHECK (mode <> 'push' OR api_token_hash IS NOT NULL)
);
CREATE INDEX hosts_organization_id_idx ON hosts (organization_id);

CREATE TABLE user_hosts (
    user_id UUID NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    host_id UUID NOT NULL REFERENCES hosts (id) ON DELETE CASCADE,
    PRIMARY KEY (user_id, host_id)
);
CREATE INDEX user_hosts_host_id_idx ON user_hosts (host_id);

-- Anlık durum ve sık değişen bilgiler: her rapor bu küçük satırı günceller, geniş hosts satırını değil.
CREATE TABLE host_status (
    host_id            UUID PRIMARY KEY REFERENCES hosts (id) ON DELETE CASCADE,
    status             TEXT NOT NULL DEFAULT 'offline' CHECK (status IN ('online', 'offline')),
    last_seen          TIMESTAMPTZ,
    agent_version      TEXT,
    agent_protocol     INTEGER,
    unsupported_fields JSONB,                        -- agent'ın gönderdiği ama bu server'ın tanımadığı alan adları
    runtime            JSONB,                        -- uptime, yük, swap, başarısız servis sayısı gibi her raporda değişenler
    updated_at         TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Yavaş değişen envanter: agent'ın son bildirdiği değerler; yalnızca içerik değişince yazılır.
CREATE TABLE host_inventory (
    host_id         UUID PRIMARY KEY REFERENCES hosts (id) ON DELETE CASCADE,
    hostname        TEXT,                            -- makinenin kendi bildirdiği ad
    machine_id_hash TEXT,                            -- /etc/machine-id'nin uygulamaya özgü özeti; çift kaydı yakalamak için (benzersiz DEĞİL: klon VM'ler aynı kimliği taşır)
    cpu_cores       INTEGER,
    ram_total_mb    BIGINT,
    physical_disks  JSONB,
    info            JSONB,                           -- işletim sistemi, kernel, donanım, adresler vb.
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()  -- içeriğin en son DEĞİŞTİĞİ zaman (değişmeyen rapor yazılmaz)
);
CREATE INDEX host_inventory_machine_id_idx ON host_inventory (machine_id_hash) WHERE machine_id_hash IS NOT NULL;

CREATE TABLE host_custom_mounts_alerts (
    host_id UUID NOT NULL REFERENCES hosts (id) ON DELETE CASCADE,
    mount   TEXT NOT NULL,
    PRIMARY KEY (host_id, mount)
);

-- ---------------------------------------------------------------- metrikler
CREATE TABLE metrics (
    host_id       UUID NOT NULL REFERENCES hosts (id) ON DELETE CASCADE,
    recorded_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    cpu_usage_pct NUMERIC NOT NULL CHECK (cpu_usage_pct >= 0 AND cpu_usage_pct <= 100),
    ram_usage_pct NUMERIC NOT NULL CHECK (ram_usage_pct >= 0 AND ram_usage_pct <= 100),
    disk_json     JSONB NOT NULL DEFAULT '[]',
    -- Doğal anahtar: ileride zamana göre partition'a geçişi kolaylaştırır.
    PRIMARY KEY (host_id, recorded_at)
);
CREATE INDEX metrics_recorded_at_idx ON metrics (recorded_at);

CREATE TABLE docker_containers (
    host_id        UUID NOT NULL REFERENCES hosts (id) ON DELETE CASCADE,
    name           TEXT NOT NULL,
    image          TEXT NOT NULL,
    status         TEXT NOT NULL CHECK (status IN ('created', 'running', 'paused', 'restarting', 'exited', 'dead', 'removing')),
    cpu_pct        NUMERIC NOT NULL CHECK (cpu_pct >= 0),
    ram_mb         NUMERIC NOT NULL CHECK (ram_mb >= 0),
    restart_count  INTEGER NOT NULL DEFAULT 0 CHECK (restart_count >= 0),
    uptime_seconds BIGINT NOT NULL DEFAULT 0 CHECK (uptime_seconds >= 0),
    reported_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (host_id, name)
);

-- ---------------------------------------------------------------- alert'ler ve eşikler
CREATE TABLE alerts (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    host_id         UUID NOT NULL REFERENCES hosts (id) ON DELETE CASCADE,
    alert_type      TEXT NOT NULL CHECK (alert_type IN ('cpu', 'ram', 'disk', 'docker_restart', 'host_offline', 'disk_missing')),
    subject         TEXT,                            -- mount yolu ya da container adı; sunucu geneli alert'lerde NULL
    level           TEXT NOT NULL CHECK (level IN ('info', 'warning', 'critical')),
    status          TEXT NOT NULL DEFAULT 'open' CHECK (status IN ('open', 'acknowledged', 'resolved')),
    value           NUMERIC,                         -- tetiklendiği andaki ölçülen değer
    threshold       NUMERIC,                         -- tetikleyen eşik (o andaki değer)
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    acknowledged_at TIMESTAMPTZ,
    acknowledged_by UUID REFERENCES users (id) ON DELETE SET NULL,
    resolved_at     TIMESTAMPTZ
);
CREATE INDEX alerts_host_id_idx ON alerts (host_id);
CREATE INDEX alerts_status_idx ON alerts (status);
-- Bir sunucu + tür + konu için aynı anda en fazla bir açık alert.
CREATE UNIQUE INDEX alerts_one_open_uidx ON alerts (host_id, alert_type, subject) NULLS NOT DISTINCT WHERE status = 'open';

-- Varsayılan eşikler: organization_id boşsa genel, doluysa o organizasyonun (ve altındaki dalın) varsayılanı.
CREATE TABLE threshold_defaults (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id UUID REFERENCES organizations (id) ON DELETE CASCADE,
    metric_type     TEXT NOT NULL CHECK (metric_type IN ('cpu', 'ram', 'disk', 'docker_restart')),
    warning_level   NUMERIC NOT NULL,
    critical_level  NUMERIC NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT threshold_defaults_levels_chk CHECK (warning_level <= critical_level),
    CONSTRAINT threshold_defaults_scope_metric_key UNIQUE NULLS NOT DISTINCT (organization_id, metric_type)
);

-- Bir sunucuya özel eşik: tüm sunucu için (subject NULL) ya da tek bir mount / container için.
CREATE TABLE host_custom_thresholds (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    host_id        UUID NOT NULL REFERENCES hosts (id) ON DELETE CASCADE,
    metric_type    TEXT NOT NULL CHECK (metric_type IN ('cpu', 'ram', 'disk', 'docker_restart')),
    subject        TEXT,
    warning_level  NUMERIC NOT NULL,
    critical_level NUMERIC NOT NULL,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT host_custom_thresholds_levels_chk CHECK (warning_level <= critical_level),
    CONSTRAINT host_custom_thresholds_subject_chk CHECK (subject IS NULL OR metric_type IN ('disk', 'docker_restart')),
    CONSTRAINT host_custom_thresholds_key UNIQUE NULLS NOT DISTINCT (host_id, metric_type, subject)
);

-- ---------------------------------------------------------------- bildirim kuralları
-- Alert'in kime, hangi kanaldan, en az hangi seviyeden gideceği. Kapsam bir organizasyon (altındaki dalın tamamı için
-- geçerli) ya da tek bir sunucudur; alıcı bir panel kullanıcısı ya da organizasyonun bir iletişim kişisidir.
-- Bir alert için en özel kapsamdaki (sunucu → organizasyon → üst organizasyon…) kurallar geçerlidir; hiç kural yoksa
-- varsayılan alıcılar (super_admin + kapsamdaki org_admin, e-posta, warning ve üzeri) kullanılır.
CREATE TABLE notification_routes (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id UUID REFERENCES organizations (id) ON DELETE CASCADE,
    host_id         UUID REFERENCES hosts (id) ON DELETE CASCADE,
    user_id         UUID REFERENCES users (id) ON DELETE CASCADE,
    contact_id      UUID REFERENCES organization_contacts (id) ON DELETE CASCADE,
    -- Veritabanı ileride eklenecek kanalları da kabul eder; API yalnızca gerçekten uygulanmış olanları kabul eder.
    channel         TEXT NOT NULL DEFAULT 'email' CHECK (channel IN ('email', 'sms', 'slack', 'discord', 'telegram')),
    min_level       TEXT NOT NULL DEFAULT 'warning' CHECK (min_level IN ('info', 'warning', 'critical')),
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT notification_routes_one_scope_chk CHECK ((organization_id IS NOT NULL) <> (host_id IS NOT NULL)),
    CONSTRAINT notification_routes_one_recipient_chk CHECK ((user_id IS NOT NULL) <> (contact_id IS NOT NULL)),
    CONSTRAINT notification_routes_unique UNIQUE NULLS NOT DISTINCT (organization_id, host_id, user_id, contact_id, channel)
);
CREATE INDEX notification_routes_organization_idx ON notification_routes (organization_id) WHERE organization_id IS NOT NULL;
CREATE INDEX notification_routes_host_idx ON notification_routes (host_id) WHERE host_id IS NOT NULL;

-- ---------------------------------------------------------------- denetim ve oturumlar
CREATE TABLE audit_logs (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id     UUID REFERENCES users (id) ON DELETE SET NULL,
    actor_email TEXT NOT NULL,                       -- kullanıcı silinse de kim olduğu kalsın
    action      TEXT NOT NULL,
    target_type TEXT NOT NULL,
    target_id   TEXT,                                -- hedefin türü değişken olduğu için yabancı anahtar değil
    details     JSONB,
    ip          INET,                                -- isteğin geldiği adres
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX audit_logs_created_at_idx ON audit_logs (created_at DESC);
CREATE INDEX audit_logs_target_idx ON audit_logs (target_type, target_id);
CREATE INDEX audit_logs_user_id_idx ON audit_logs (user_id);

-- Verilmiş refresh token'ların server tarafı kaydı (iptal ve döndürme için); token dizesinin kendisi saklanmaz.
CREATE TABLE refresh_tokens (
    jti        UUID PRIMARY KEY,
    user_id    UUID NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    family_id  UUID NOT NULL,                        -- tek girişten türeyen token'lar bir aileyi paylaşır; yeniden kullanım tüm aileyi iptal eder
    expires_at TIMESTAMPTZ NOT NULL,
    rotated_at TIMESTAMPTZ,
    revoked_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX refresh_tokens_user_id_idx ON refresh_tokens (user_id);
CREATE INDEX refresh_tokens_family_id_idx ON refresh_tokens (family_id);
CREATE INDEX refresh_tokens_expires_at_idx ON refresh_tokens (expires_at);

-- E-posta ile şifre sıfırlama bağlantıları: ham token saklanmaz, yalnızca SHA-256 özeti; kullanıcı başına tek geçerli kayıt.
CREATE TABLE password_reset_tokens (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id    UUID NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    token_hash TEXT NOT NULL UNIQUE,
    expires_at TIMESTAMPTZ NOT NULL,
    used_at    TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX password_reset_tokens_user_id_idx ON password_reset_tokens (user_id);
CREATE INDEX password_reset_tokens_expires_at_idx ON password_reset_tokens (expires_at);
