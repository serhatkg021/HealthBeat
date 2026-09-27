-- Çözülmüş alert'lerin saklama temizliği (RESOLVED_ALERT_RETENTION_DAYS) eski olanları çözülme zamanına göre parça parça
-- siler; bu indeks olmadan her parça tabloyu baştan tarardı. Yalnızca indeks eklenir, veri değişmez: eski bir server süreci
-- bu şemayla çalışmaya devam eder.
CREATE INDEX alerts_resolved_at_idx ON alerts (resolved_at) WHERE status = 'resolved';
