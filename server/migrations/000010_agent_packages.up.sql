-- 000010: server'dan dağıtılan agent paketleri. Süper admin paketleri panelden yükler ve sürümle mimariyi seçer; biçim
-- dosyanın içeriğinden anlaşılır. Dosya diskte (AGENT_PACKAGES_DIR) server'ın ürettiği release adıyla durur (filename),
-- yüklenen asıl ad original_filename'dedir. Paketler kimlik doğrulamasız bir uçtan indirilir; her indirme
-- agent_package_downloads'a yazılır.
--
-- Yalnızca tablo, satır ve varsayılanlı sütun eklenir: güncelleme sırasında hâlâ çalışan eski bir server süreci bu şemayla
-- çalışır. Geri dönüş .down.sql ya da yedekle (docs/DISTRIBUTION.md §8.3).

-- ---------------------------------------------------------------- agent_packages
CREATE TABLE agent_packages (
    id                 UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    version            TEXT NOT NULL,
    filename           TEXT NOT NULL,
    original_filename  TEXT NOT NULL,
    arch               TEXT NOT NULL,
    format             TEXT NOT NULL,
    size_bytes         BIGINT NOT NULL,
    sha256             TEXT NOT NULL,
    uploaded_by        UUID REFERENCES users (id) ON DELETE SET NULL,
    uploaded_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    download_count     BIGINT NOT NULL DEFAULT 0,
    last_downloaded_at TIMESTAMPTZ,
    CONSTRAINT agent_packages_filename_key UNIQUE (filename),
    CONSTRAINT agent_packages_variant_key UNIQUE (version, arch, format),
    CONSTRAINT agent_packages_version_chk CHECK (version ~ '^[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.]+)?$' AND length(version) <= 64),
    CONSTRAINT agent_packages_filename_chk CHECK (filename ~ '^[0-9A-Za-z._~-]+$' AND length(filename) <= 128),
    CONSTRAINT agent_packages_original_filename_chk CHECK (length(original_filename) BETWEEN 1 AND 255),
    CONSTRAINT agent_packages_arch_chk CHECK (arch IN ('amd64', 'arm64')),
    CONSTRAINT agent_packages_format_chk CHECK (format IN ('deb', 'rpm', 'tar.gz')),
    CONSTRAINT agent_packages_size_chk CHECK (size_bytes > 0),
    CONSTRAINT agent_packages_sha256_chk CHECK (sha256 ~ '^[0-9a-f]{64}$'),
    CONSTRAINT agent_packages_download_count_chk CHECK (download_count >= 0)
);

-- ---------------------------------------------------------------- agent_package_downloads
CREATE TABLE agent_package_downloads (
    id            BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    package_id    UUID NOT NULL REFERENCES agent_packages (id) ON DELETE CASCADE,
    client_ip     TEXT NOT NULL,
    user_agent    TEXT NOT NULL DEFAULT '',
    downloaded_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT agent_package_downloads_user_agent_chk CHECK (length(user_agent) <= 256)
);
CREATE INDEX agent_package_downloads_package_idx ON agent_package_downloads (package_id, downloaded_at DESC);
CREATE INDEX agent_package_downloads_downloaded_at_idx ON agent_package_downloads (downloaded_at);

-- ---------------------------------------------------------------- izin ve ayar
INSERT INTO permissions (key, description) VALUES
    ('agent.package.manage', 'Agent paketlerini yükleme, silme ve indirmelerini görme');

INSERT INTO role_permissions (role, permission_key) VALUES
    ('super_admin', 'agent.package.manage');

ALTER TABLE app_settings ADD COLUMN agent_download_enabled BOOLEAN NOT NULL DEFAULT true;
