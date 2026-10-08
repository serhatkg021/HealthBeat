// Alert satırında gösterilen ölçüm metni (React yok; Node'un çalıştırıcısıyla birim test edilir).

import type { Alert } from '../types/api.ts'
import { formatCelsius, formatMs, formatOffsetMs } from './units.ts'

const num = (n: number): string => (Number.isInteger(n) ? String(n) : n.toFixed(1)).replace('.', ',')

// Eşik kullanıcının girdiği değerdir: tek ondalık onu değiştiriyorsa (0,002 ms "0,0" olurdu) tam hâliyle yazılır.
const thr = (n: number): string => (Number.isInteger(n) || Number(n.toFixed(1)) === n ? num(n) : String(n).replace('.', ','))

// "%97,5 (eşik %95)", "7 restart (eşik 5)" ya da birimiyle ("14,2 ms (eşik 10 ms)"); olay ve durum alert'lerinde (sunucu
// çevrimdışı, servis çalışmıyor …) ölçüm yoktur: ''.
export function alertReading(a: Pick<Alert, 'alert_type' | 'value' | 'threshold'>): string {
  if (a.value === undefined || a.threshold === undefined) return ''
  switch (a.alert_type) {
    case 'docker_restart':
      return `${num(a.value)} restart (eşik ${thr(a.threshold)})`
    case 'service_restart_loop':
      return `10 dakikada ${num(a.value)} yeniden başlatma (eşik ${thr(a.threshold)})`
    case 'disk_latency':
      return `${formatMs(a.value)} (eşik ${thr(a.threshold)} ms)`
    case 'time_sync': // yalnızca saat farkı (offset) alert'inin ölçümü vardır: farkın mutlak değeri
      return `${formatOffsetMs(a.value)} (eşik ${thr(a.threshold)} ms)`
    case 'temperature':
      return `${formatCelsius(a.value)} (eşik ${thr(a.threshold)} °C)`
  }
  return `%${num(a.value)} (eşik %${thr(a.threshold)})`
}
