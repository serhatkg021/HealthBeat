-- Geri alma: protokol 4 sütunları ve tabloları silinir, CHECK'ler eski hâline döner. Yeni türlerdeki alert'ler ve
-- eşikler önce silinir (eski CHECK onları kabul etmez); servis listesi, izlenen servis seçimi, durum kuralları ve yeni
-- zaman serisi verileri kaybolur.
DELETE FROM alerts WHERE alert_type NOT IN ('cpu', 'ram', 'disk', 'docker_restart', 'host_offline', 'disk_missing');
DELETE FROM threshold_defaults WHERE metric_type NOT IN ('cpu', 'ram', 'disk', 'docker_restart');
DELETE FROM host_custom_thresholds WHERE metric_type NOT IN ('cpu', 'ram', 'disk', 'docker_restart');

ALTER TABLE host_custom_thresholds DROP CONSTRAINT host_custom_thresholds_subject_chk;
ALTER TABLE host_custom_thresholds ADD CONSTRAINT host_custom_thresholds_subject_chk CHECK (
    subject IS NULL OR metric_type IN ('disk', 'docker_restart'));

ALTER TABLE host_custom_thresholds DROP CONSTRAINT host_custom_thresholds_metric_type_check;
ALTER TABLE host_custom_thresholds ADD CONSTRAINT host_custom_thresholds_metric_type_check CHECK (
    metric_type IN ('cpu', 'ram', 'disk', 'docker_restart'));

ALTER TABLE threshold_defaults DROP CONSTRAINT threshold_defaults_metric_type_check;
ALTER TABLE threshold_defaults ADD CONSTRAINT threshold_defaults_metric_type_check CHECK (
    metric_type IN ('cpu', 'ram', 'disk', 'docker_restart'));

ALTER TABLE alerts DROP CONSTRAINT alerts_alert_type_check;
ALTER TABLE alerts ADD CONSTRAINT alerts_alert_type_check CHECK (
    alert_type IN ('cpu', 'ram', 'disk', 'docker_restart', 'host_offline', 'disk_missing'));

ALTER TABLE host_custom_thresholds DROP COLUMN duration_seconds;
ALTER TABLE threshold_defaults DROP COLUMN duration_seconds;
DROP TABLE alert_pending;
DROP TABLE status_alert_rules;
DROP TABLE host_watched_services;
DROP TABLE host_services;

ALTER TABLE docker_containers
    DROP COLUMN health,
    DROP COLUMN health_failing_streak,
    DROP COLUMN exit_code,
    DROP COLUMN oom_killed;

ALTER TABLE host_status DROP COLUMN system_state;

ALTER TABLE metrics
    DROP COLUMN system_json,
    DROP COLUMN disk_io_json,
    DROP COLUMN net_io_json;
