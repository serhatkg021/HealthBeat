// Performans sekmesindeki geçmiş grafiklerinin saf mantığı: hazır aralıklar, zaman etiketleri ve metrik noktalarının
// grafik satırlarına çevrilmesi. React içermez; Node'un çalıştırıcısıyla birim test edilir.
import type { MetricPoint } from '../types/api'
import { formatDateTime, formatTime, inputValue } from './datetime.ts'

export type RangeKey = '1h' | '6h' | '24h' | '7d'

export const RANGES: { key: RangeKey; label: string; hours: number }[] = [
  { key: '1h', label: 'Son 1 saat', hours: 1 },
  { key: '6h', label: 'Son 6 saat', hours: 6 },
  { key: '24h', label: 'Son 24 saat', hours: 24 },
  { key: '7d', label: 'Son 7 gün', hours: 24 * 7 },
]

// Grafikte en çok bu kadar mount çizilir (renkler ayırt edilebilir kalsın diye).
export const MAX_DISK_SERIES = 8

const DAY_MS = 24 * 60 * 60 * 1000

// toInputValue, aralık seçicisinin (datetime-local) değeridir; kurulumun saat diliminde (bkz. datetime.ts).
export const toInputValue = (d: Date): string => inputValue(d)

// Aralık bir günden uzunsa saat:dakika:saniye tek başına belirsiz kalır (hangi gün?); tarih de eklenir. Biçim ve saat
// dilimi panelin geri kalanıyla aynıdır (datetime.ts).
export function formatPoint(ms: number, spanMs: number): string {
  return spanMs > DAY_MS ? formatDateTime(ms) : formatTime(ms)
}

export const isLongSpan = (spanMs: number): boolean => spanMs > DAY_MS

// Aralıkta görülen mount'lar, alfabetik ve en çok MAX_DISK_SERIES tane.
export function diskMounts(points: MetricPoint[]): string[] {
  return [...new Set(points.flatMap((p) => p.disk.map((d) => d.mount)))].sort().slice(0, MAX_DISK_SERIES)
}

export function cpuRamRows(points: MetricPoint[]): Record<string, number>[] {
  return points.map((p) => ({
    ts: new Date(p.timestamp).getTime(),
    cpu: Number(p.cpu_usage_pct.toFixed(1)),
    ram: Number(p.ram_usage_pct.toFixed(1)),
  }))
}

// Her satır bir zaman noktasıdır; o anda raporlanmayan mount satırda yer almaz (çizgi orada kesilir).
export function diskRows(points: MetricPoint[], mounts: string[]): Record<string, number>[] {
  return points.map((p) => {
    const row: Record<string, number> = { ts: new Date(p.timestamp).getTime() }
    for (const d of p.disk) {
      if (mounts.includes(d.mount)) row[d.mount] = Number(d.used_pct.toFixed(1))
    }
    return row
  })
}
