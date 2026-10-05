// Performans sekmesindeki geçmiş grafiklerinin saf mantığı: hazır aralıklar, zaman etiketleri ve metrik noktalarının
// grafik satırlarına çevrilmesi. React içermez; Node'un çalıştırıcısıyla birim test edilir.
import type { MetricPoint } from '../types/api'

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

const pad = (n: number) => String(n).padStart(2, '0')

export function toInputValue(d: Date): string {
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}T${pad(d.getHours())}:${pad(d.getMinutes())}`
}

// Aralık bir günden uzunsa saat:dakika:saniye tek başına belirsiz kalır (hangi gün?); tarihi de
// ekle — yerel biçime (ay/gün sırası belirsiz olabilir) değil, sabit gün-ay-yıl sırasına göre.
export function formatPoint(ms: number, spanMs: number): string {
  const d = new Date(ms)
  const time = `${pad(d.getHours())}:${pad(d.getMinutes())}:${pad(d.getSeconds())}`
  if (spanMs > DAY_MS) {
    return `${pad(d.getDate())}-${pad(d.getMonth() + 1)}-${d.getFullYear()} ${time}`
  }
  return time
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
