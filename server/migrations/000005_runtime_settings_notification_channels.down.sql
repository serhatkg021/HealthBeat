-- Geri alma: ayarlar, kanallar ve sistem sahipleri silinir (panelden girilen SMTP ayarı ve sahipler kaybolur; eski sürüm
-- bunları env'den okur). Kurallardaki kanal yeniden sabit listeyle sınırlanır. Bölünmüş kuyruk satırları birleştirilmez:
-- tek alıcılı satır eski şemada da geçerlidir.
DELETE FROM permissions WHERE key IN ('settings.view', 'settings.manage');

ALTER TABLE notification_routes DROP CONSTRAINT notification_routes_channel_fkey;
ALTER TABLE notification_routes
    ADD CONSTRAINT notification_routes_channel_check CHECK (channel IN ('email', 'sms', 'slack', 'discord', 'telegram'));

DROP TABLE notification_owners;
DROP TABLE notification_channels;
DROP TABLE app_settings;
