// Sunucu sayfasındaki Durum kutusunun süre bilgisi — saf mantık. Çevrimiçiyse agent'ın bildirdiği
// çalışma süresi (uptime); çevrimdışıysa son verinin gelmesinden bu yana geçen süre, yani sunucunun
// çevrimdışı olduğu süre. Çevrimdışı sunucunun son bildirdiği uptime bayattır, bu yüzden gösterilmez.
import { formatUptime } from './inventory.ts'

export interface StatusDuration {
  text: string
  // Kutunun altındaki kısa açıklama.
  hint: string
  // Fareyle üzerine gelince görünen tam açıklama.
  title: string
}

export interface StatusFields {
  status: string
  last_seen?: string | null
  host_info?: { uptime_seconds?: number }
}

export function statusDuration(c: StatusFields, nowMs: number): StatusDuration | null {
  if (c.status === 'online') {
    const up = c.host_info?.uptime_seconds
    if (up === undefined) return null
    return { text: formatUptime(up), hint: 'çalışma süresi', title: 'Sunucunun kesintisiz çalışma süresi (uptime)' }
  }
  const seen = c.last_seen ? Date.parse(c.last_seen) : Number.NaN
  if (Number.isNaN(seen)) return { text: '—', hint: 'hiç veri gelmedi', title: 'Bu sunucudan henüz hiç veri alınmadı' }
  // Tarayıcı ile server saati birkaç saniye farklı olabilir: negatif süre gösterilmez.
  const seconds = Math.max(0, Math.floor((nowMs - seen) / 1000))
  return {
    text: formatUptime(seconds),
    hint: 'son veriden bu yana',
    title: 'Sunucudan son veri alındığından bu yana geçen süre (çevrimdışı kalma süresi)',
  }
}
