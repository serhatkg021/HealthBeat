// API'nin döndürdüğü ham durum değerlerinin panelde gösterilen Türkçe karşılıkları. Bilinmeyen
// bir değer olduğu gibi gösterilir (yeni bir değer eklenirse ekran boş kalmasın).

const HOST_STATUS: Record<string, string> = {
  online: 'çevrimiçi',
  offline: 'çevrimdışı',
}

const ALERT_LEVEL: Record<string, string> = {
  info: 'bilgi',
  critical: 'kritik',
  warning: 'uyarı',
}

const ALERT_STATUS: Record<string, string> = {
  open: 'açık',
  acknowledged: 'onaylanmış',
  resolved: 'çözülmüş',
}

// Alert'in ait olduğu metrik.
const ALERT_METRIC: Record<string, string> = {
  cpu: 'CPU',
  ram: 'RAM',
  disk: 'Disk',
  docker_restart: 'Docker restart',
  host_offline: 'Sunucu çevrimdışı',
  disk_missing: 'Disk kayboldu',
}

// Docker Engine'in container durumları.
const CONTAINER_STATUS: Record<string, string> = {
  running: 'çalışıyor',
  restarting: 'yeniden başlıyor',
  exited: 'durdu',
  paused: 'duraklatıldı',
  created: 'oluşturuldu',
  dead: 'ölü',
  removing: 'kaldırılıyor',
}

// Bir bildirimin teslim durumu (bkz. GET /alerts/:id/notifications).
const NOTIFICATION_STATUS: Record<string, string> = {
  sent: 'gönderildi',
  pending: 'bekliyor',
  failed: 'gönderilemedi',
}

// Bildirimi doğuran alert olayı.
const ALERT_EVENT: Record<string, string> = {
  opened: 'açıldı',
  level_changed: 'seviye değişti',
  resolved: 'çözüldü',
}

// Bildirim kanalları (notification_routes.channel).
const CHANNEL: Record<string, string> = {
  email: 'e-posta',
  sms: 'SMS',
  slack: 'Slack',
  discord: 'Discord',
  telegram: 'Telegram',
}

const label = (table: Record<string, string>, value: string): string => table[value] ?? value

export const hostStatusLabel = (status: string): string => label(HOST_STATUS, status)
// Alert seviyesinin rozet rengi: info yalnızca bilgi (nötr), warning sarı, critical kırmızı.
export const alertLevelTone = (level: string): 'neutral' | 'warning' | 'critical' =>
  level === 'critical' ? 'critical' : level === 'info' ? 'neutral' : 'warning'
export const alertLevelLabel = (level: string): string => label(ALERT_LEVEL, level)
export const alertStatusLabel = (status: string): string => label(ALERT_STATUS, status)
export const alertMetricLabel = (metric: string): string => label(ALERT_METRIC, metric)
export const containerStatusLabel = (status: string): string => label(CONTAINER_STATUS, status)
export const notificationStatusLabel = (status: string): string => label(NOTIFICATION_STATUS, status)
// Bildirim durumunun rozet rengi: gittiyse yeşil, bekliyorsa (yeniden denenecek) sarı, gitmediyse kırmızı.
export const notificationStatusTone = (status: string): 'good' | 'warning' | 'critical' | 'neutral' =>
  status === 'sent' ? 'good' : status === 'pending' ? 'warning' : status === 'failed' ? 'critical' : 'neutral'
export const alertEventLabel = (event: string): string => label(ALERT_EVENT, event)
export const channelLabel = (channel: string): string => label(CHANNEL, channel)
