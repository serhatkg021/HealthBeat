// "Yakında" kartlarının ÖRNEK verisi: henüz gelmemiş özelliklerin nasıl görüneceğini anlatır. Gerçek bir sunucudan
// gelmez ve hiçbir gerçek hesaba (sayaç, özet, alert) girmez; yalnızca ComingSoon içinde çizilir. Özellik geldiğinde
// ilgili örnek buradan silinir.

// Bildirim sayfasında "Bu alert kime gider?" önizlemesi: tüm kurallar birleştirilince çıkan alıcılar.
export const SAMPLE_RECIPIENTS = {
  host: 'web-01',
  level: 'kritik',
  recipients: [
    { name: 'Sistem sahibi', source: 'sistem sahibi', channel: 'e-posta' },
    { name: 'Nöbetçi DBA', source: 'Organizasyon: Üretim', channel: 'e-posta, SMS' },
    { name: 'DC ekibi', source: 'Organizasyon: İstanbul DC', channel: 'e-posta' },
    { name: 'Uygulama sorumlusu', source: 'Sunucu: web-01', channel: 'Telegram' },
  ],
}

// Bakım pencereleri sayfasının örnekleri: planlı pencereler (bu sürede alert'ler kaydedilir ama bildirim gönderilmez).
export const SAMPLE_MAINTENANCE = [
  { title: 'PostgreSQL 16 → 17 yükseltmesi', scope: 'db-02, web-01', when: '06.10 02:00–04:00', mode: 'kaydet, bildirme', status: 'sürüyor · 1 sa 12 dk' },
  { title: 'Haftalık yedek ve güncelleme', scope: 'Organizasyon: Üretim (96 sunucu)', when: 'Her pazar 03:00–03:30', mode: 'kaydet, bildirme', status: 'planlı' },
  { title: 'Veri merkezi ağ bakımı', scope: 'Organizasyon: İstanbul DC', when: '12.10 01:00–05:00', mode: 'hiç alert açma', status: 'planlı' },
  { title: 'Disk değişimi', scope: 'backup-02', when: '28.09 10:00–11:30', mode: 'kaydet, bildirme', status: 'tamamlandı' },
]
