-- Geri alma: bildirim kuyruğu kalkar. Bekleyen (gönderilmemiş) bildirimler de silinir; eski sürüm bellek içi kuyrukla
-- yalnızca bundan sonraki alert'leri bildirir.
DROP TABLE notification_outbox;
