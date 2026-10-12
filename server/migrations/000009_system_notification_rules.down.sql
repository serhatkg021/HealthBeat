-- Geri alma: sistem kişileri aynı kimliklerle notification_owners'a, e-posta kanalının sahip seviyesi owner_min_level'a
-- geri döner; sistem kuralları ve sistem kişileri silinir, contacts yeniden organization_contacts olur.
--
-- Sistem kurallarından geri türetilen değerler: sahip, e-posta kanalında bir sistem kuralı varsa e-posta almaya devam eder
-- (email_enabled); sms_enabled varsayılanına (true) döner. Sahip seviyesi kanal başına tek olduğu için sistem e-posta
-- kurallarının EN DÜŞÜK seviyesi alınır (kural yoksa 'warning'): kişi başına farklı seviyeler tek seviyeye iner.
-- Sistem kuralındaki kullanıcılar (süper adminler) geri alınamaz; bu kurallar silinir.

-- ---------------------------------------------------------------- sistem sahipleri
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

INSERT INTO notification_owners (id, name, email, phone, email_enabled, sms_enabled, created_at, updated_at)
SELECT k.id, k.name, lower(k.email), k.phone,
       EXISTS (SELECT 1 FROM notification_routes r
                WHERE r.contact_id = k.id AND r.channel = 'email' AND r.organization_id IS NULL AND r.host_id IS NULL),
       true, k.created_at, k.updated_at
  FROM contacts k
 WHERE k.organization_id IS NULL;

-- ---------------------------------------------------------------- kanalın sahip seviyesi
ALTER TABLE notification_channels ADD COLUMN owner_min_level TEXT NOT NULL DEFAULT 'warning';
ALTER TABLE notification_channels ADD CONSTRAINT notification_channels_owner_min_level_chk
    CHECK (owner_min_level IN ('info', 'warning', 'critical'));
UPDATE notification_channels c
   SET owner_min_level = (ARRAY['info', 'warning', 'critical'])[m.rank]
  FROM (SELECT r.channel, min(array_position(ARRAY['info', 'warning', 'critical'], r.min_level)) AS rank
          FROM notification_routes r
         WHERE r.organization_id IS NULL AND r.host_id IS NULL
         GROUP BY r.channel) m
 WHERE m.channel = c.channel;

-- ---------------------------------------------------------------- sistem kuralları ve sistem kişileri
DELETE FROM notification_routes WHERE organization_id IS NULL AND host_id IS NULL;
ALTER TABLE notification_routes DROP CONSTRAINT notification_routes_one_scope_chk;
ALTER TABLE notification_routes ADD CONSTRAINT notification_routes_one_scope_chk
    CHECK ((organization_id IS NOT NULL) <> (host_id IS NOT NULL));

DELETE FROM contacts WHERE organization_id IS NULL;
DROP INDEX contacts_system_email_uidx;
ALTER TABLE contacts DROP CONSTRAINT contacts_system_no_manager_chk;
ALTER TABLE contacts ALTER COLUMN organization_id SET NOT NULL;

-- ---------------------------------------------------------------- contacts → organization_contacts
ALTER TABLE contacts RENAME CONSTRAINT contacts_reachable_chk TO organization_contacts_reachable_chk;
ALTER TABLE contacts RENAME CONSTRAINT contacts_not_own_manager_chk TO organization_contacts_not_own_manager_chk;
ALTER TABLE contacts RENAME CONSTRAINT contacts_manager_fkey TO organization_contacts_manager_fkey;
ALTER TABLE contacts RENAME CONSTRAINT contacts_organization_id_fkey TO organization_contacts_organization_id_fkey;
ALTER TABLE contacts RENAME CONSTRAINT contacts_org_id_key TO organization_contacts_org_id_key;
ALTER TABLE contacts RENAME CONSTRAINT contacts_pkey TO organization_contacts_pkey;
ALTER TABLE contacts RENAME TO organization_contacts;
