-- Geri alma: "tek açık alert" kuralına döner. 1. adımda çözülmüş sayılan kopyalar geri açılmaz.
CREATE UNIQUE INDEX alerts_one_open_uidx ON alerts (host_id, alert_type, subject) NULLS NOT DISTINCT WHERE status = 'open';
DROP INDEX alerts_one_active_uidx;
