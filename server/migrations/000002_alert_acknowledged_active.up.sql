-- Onaylanan (acknowledged) alert çözülene kadar AKTİF sayılır: onay "gördüm, sustur ama izle" demektir. Önceden
-- yalnızca 'open' aktifti; onaylanan bir alert hiç çözülmüyor, metrik hâlâ eşik üstündeyse bir sonraki raporda aynı
-- olay için yeni bir alert (ve yeni bir e-posta) açılıyordu (bkz. docs/MIMARI.md bölüm 8).
--
-- Güncelleme sırasında hâlâ çalışan eski bir server süreci bu şemayla da çalışır: onaylanmış bir alert varken yeni alert
-- açamaz (ON CONFLICT DO NOTHING), yalnızca onaylanmışları çözemez. Eski bir binary ise bu veritabanıyla yeniden
-- BAŞLATILAMAZ (migrate, bilmediği bir migration görünce açılmayı reddeder); geri dönüş yedekten yapılır.

-- 1) Eski davranışın bıraktığı kopyalar: aynı sunucu + tür + konu için birden fazla çözülmemiş alert varsa (onaylanmış
--    eski olay + yeniden açılmış olay), en yenisi dışındakiler çözülmüş sayılır; aksi halde aşağıdaki index kurulamaz.
UPDATE alerts a
SET status = 'resolved', resolved_at = now()
WHERE a.status <> 'resolved'
  AND EXISTS (
      SELECT 1 FROM alerts b
      WHERE b.host_id = a.host_id
        AND b.alert_type = a.alert_type
        AND b.subject IS NOT DISTINCT FROM a.subject
        AND b.status <> 'resolved'
        AND (b.created_at, b.id) > (a.created_at, a.id)
  );

-- 2) Bir sunucu + tür + konu için aynı anda en fazla bir AKTİF (açık ya da onaylanmış) alert.
CREATE UNIQUE INDEX alerts_one_active_uidx ON alerts (host_id, alert_type, subject) NULLS NOT DISTINCT WHERE status <> 'resolved';
DROP INDEX alerts_one_open_uidx;
