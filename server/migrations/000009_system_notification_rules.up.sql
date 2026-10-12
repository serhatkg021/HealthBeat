-- 000009: Bildirim modeli sadeleşir. "Kime gider" tek yerde, bildirim kurallarında tanımlanır: kurallara SİSTEM kapsamı
-- (organizasyon ve sunucu boş) eklenir ve sistem sahipleri bu kapsamdaki kurallara dönüşür. Kanal yalnızca "nasıl gider"i
-- (bağlantı ayarı, açık/kapalı) tutar; sahiplere gönderim seviyesi (owner_min_level) kalkar. Sistem sahipleri ile
-- organizasyonların iletişim kişileri tek tabloda birleşir: organization_contacts → contacts, organization_id boş olan
-- satır sistem kişisidir (panel hesabı olmayan kişi ya da ortak adres). notification_owners kalkar.
--
-- UYUMLULUK (docs/COMPATIBILITY.md kural 6 ve §4): bu migration yalnızca ekleme yapmaz; tablo adı değişir, bir tablo ve
-- bir sütun silinir. Güncelleme anında hâlâ çalışan eski bir server sürecinin kişi, sistem sahibi ve alıcı sorguları hata
-- verir: o arada açılan alert kaydedilir ama bildirimi gitmez. Belgelenen kurulumda (Docker Compose, tek server kopyası,
-- sabit port) yeni server migration'ı uygularken eski süreç durmuş olduğu için bu aralık oluşmaz. Eski bir binary bu
-- veritabanıyla yeniden BAŞLATILAMAZ; geri dönüş .down.sql ya da yedekle (docs/DISTRIBUTION.md §8.3).

-- ---------------------------------------------------------------- kişiler: organization_contacts → contacts
ALTER TABLE organization_contacts RENAME TO contacts;
ALTER TABLE contacts RENAME CONSTRAINT organization_contacts_pkey TO contacts_pkey;
ALTER TABLE contacts RENAME CONSTRAINT organization_contacts_org_id_key TO contacts_org_id_key;
ALTER TABLE contacts RENAME CONSTRAINT organization_contacts_organization_id_fkey TO contacts_organization_id_fkey;
ALTER TABLE contacts RENAME CONSTRAINT organization_contacts_manager_fkey TO contacts_manager_fkey;
ALTER TABLE contacts RENAME CONSTRAINT organization_contacts_not_own_manager_chk TO contacts_not_own_manager_chk;
ALTER TABLE contacts RENAME CONSTRAINT organization_contacts_reachable_chk TO contacts_reachable_chk;

-- Organizasyonu boş kişi sistem kişisidir. Yönetici bağı organizasyon içindir: bileşik yabancı anahtar organizasyon boşken
-- denetlenmediği için sistem kişisinde yönetici ayrıca yasaklanır. Sistem kişileri arasında e-posta tekildir (sistem
-- sahiplerindeki kural); organizasyon kişilerinde bugünkü gibi tekillik aranmaz.
ALTER TABLE contacts ALTER COLUMN organization_id DROP NOT NULL;
ALTER TABLE contacts ADD CONSTRAINT contacts_system_no_manager_chk
    CHECK (organization_id IS NOT NULL OR manager_contact_id IS NULL);
CREATE UNIQUE INDEX contacts_system_email_uidx ON contacts (lower(email))
    WHERE organization_id IS NULL AND email IS NOT NULL;

-- Sistem sahipleri aynı kimliklerle sistem kişisi olur (denetim kaydındaki hedef kimlikleri anlamını korur).
INSERT INTO contacts (id, organization_id, name, email, phone, created_at, updated_at)
SELECT id, NULL, name, email, phone, created_at, updated_at FROM notification_owners;

-- ---------------------------------------------------------------- bildirim kuralları: sistem kapsamı
-- Kapsam organizasyon, sunucu ya da hiçbiri (= sistem: bütün sunucular). Alıcı kısıtı değişmez (kullanıcı ya da kişi);
-- notification_routes_unique (NULLS NOT DISTINCT) sistem kurallarında da aynı alıcı + kanal tekrarını engeller.
ALTER TABLE notification_routes DROP CONSTRAINT notification_routes_one_scope_chk;
ALTER TABLE notification_routes ADD CONSTRAINT notification_routes_one_scope_chk
    CHECK (organization_id IS NULL OR host_id IS NULL);

-- Bugünkü davranış kurala taşınır: e-posta almak isteyen ve adresi olan her sahip, e-posta kanalının sahip seviyesinden
-- itibaren bildirim alır. SMS kanalı tanımlı olmadığı için SMS kuralı üretilmez (sms_enabled bugün de etkisizdi).
INSERT INTO notification_routes (organization_id, host_id, user_id, contact_id, channel, min_level, created_at)
SELECT NULL, NULL, NULL, o.id, c.channel, c.owner_min_level, o.created_at
  FROM notification_owners o
  JOIN notification_channels c ON c.channel = 'email'
 WHERE o.email_enabled AND o.email IS NOT NULL;

-- ---------------------------------------------------------------- artık kullanılmayanlar
DROP TABLE notification_owners;
ALTER TABLE notification_channels DROP COLUMN owner_min_level;
