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
