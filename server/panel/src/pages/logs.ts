// Sistem Araçları → Log Analiz sekmesinin metinleri ve hesapları (React yok; Node'un çalıştırıcısıyla birim test edilir).

import type { LogDay, LogEntry, LogFiles } from '../types/api.ts'
import { formatBytes } from './hardwareTotals.ts'

// En düşük seviye süzgeci: seçilen seviye ve üstü gösterilir.
export const LOG_LEVELS: { value: string; label: string }[] = [
  { value: '', label: 'Tüm seviyeler' },
  { value: 'info', label: 'Info ve üstü' },
  { value: 'warn', label: 'Uyarı ve üstü' },
  { value: 'error', label: 'Yalnızca hata' },
]

// levelBadge, bir satırın seviye rozetidir. Seviyesi okunamayan (ayrıştırılamayan) satır "ham" olarak gösterilir.
export function levelBadge(level: string): { label: string; tone: 'good' | 'warning' | 'critical' | 'neutral' } {
  const name = level.split('+')[0]
  switch (name) {
    case 'ERROR':
      return { label: 'ERROR', tone: 'critical' }
    case 'WARN':
      return { label: 'WARN', tone: 'warning' }
    case 'INFO':
      return { label: 'INFO', tone: 'neutral' }
    case 'DEBUG':
      return { label: 'DEBUG', tone: 'neutral' }
    default:
      return { label: level || 'ham', tone: 'neutral' }
  }
}

// Satırda ileti yanında hemen gösterilen alanlar (bu sırayla); kalanlar satır açılınca görünür.
const SUMMARY_KEYS = ['method', 'path', 'status', 'duration_ms', 'err', 'ip', 'channel', 'kind', 'host_id', 'user_id']

export function summaryAttrs(e: Pick<LogEntry, 'attrs'>): { key: string; value: string }[] {
  return SUMMARY_KEYS.flatMap((k) => e.attrs.filter((a) => a.key === k))
}

// requestIdOf, satırın istek kimliğidir (yoksa null): aynı isteğin bütün satırlarını süzmek için.
export function requestIdOf(e: Pick<LogEntry, 'attrs'>): string | null {
  return e.attrs.find((a) => a.key === 'request_id')?.value || null
}

// prettyValue, JSON taşıyan bir alanı (hata gövdesi, iç içe grup) girintili gösterir; JSON değilse olduğu gibi bırakır.
export function prettyValue(value: string): string {
  const v = value.trim()
  if (!(v.startsWith('{') && v.endsWith('}')) && !(v.startsWith('[') && v.endsWith(']'))) return value
  try {
    return JSON.stringify(JSON.parse(v), null, 2)
  } catch {
    return value
  }
}

// entryTime, satır zamanını tarayıcının saatinde, milisaniyesiyle gösterir ("22:41:10.631"); zamanı yoksa "—".
export function entryTime(time: string | null): string {
  if (!time) return '—'
  const d = new Date(time)
  if (Number.isNaN(d.getTime())) return '—'
  const p = (n: number, w = 2) => String(n).padStart(w, '0')
  return `${p(d.getHours())}:${p(d.getMinutes())}:${p(d.getSeconds())}.${p(d.getMilliseconds(), 3)}`
}

// dayOptionLabel, gün seçicideki satırdır: "2026-10-02 · 86.1 KB · 3 parça".
export function dayOptionLabel(d: LogDay): string {
  return [d.day, formatBytes(d.bytes), d.parts > 1 ? `${d.parts} parça` : '', d.compressed ? 'sıkıştırılmış' : ''].filter(Boolean).join(' · ')
}

// diskUsage, log dosyalarının toplam boyutunu sınıra oranlar; sınır bilinmiyorsa (0) yüzde 0'dır.
export function diskUsage(f: Pick<LogFiles, 'total_bytes' | 'max_total_bytes'>): { pct: number; text: string } {
  const used = f.total_bytes > 0 ? formatBytes(f.total_bytes) : '0 B'
  if (f.max_total_bytes <= 0) return { pct: 0, text: used }
  return { pct: Math.min(100, (f.total_bytes / f.max_total_bytes) * 100), text: `${used} / ${formatBytes(f.max_total_bytes)}` }
}

// clockToInstant, seçili günün tarayıcı saatindeki "SS:DD" anını RFC3339'a çevirir; boş ya da geçersizse undefined.
// Günler server'ın tarihine göre ayrılır: gece yarısına yakın bir saat komşu günün dosyasında olabilir.
export function clockToInstant(day: string, clock: string): string | undefined {
  if (!/^\d{2}:\d{2}$/.test(clock)) return undefined
  const d = new Date(`${day}T${clock}:00`)
  return Number.isNaN(d.getTime()) ? undefined : d.toISOString()
}

export interface LogFilters {
  level: string
  q: string
  requestId: string
  from: string
  to: string
}

export const NO_FILTERS: LogFilters = { level: '', q: '', requestId: '', from: '', to: '' }

// filtersActive, varsayılan dışında bir süzgeç olup olmadığıdır ("süzgeçleri temizle" düğmesi için).
export const filtersActive = (f: LogFilters): boolean => Object.values(f).some((v) => v.trim() !== '')
