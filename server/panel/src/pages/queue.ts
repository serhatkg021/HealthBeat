// Sistem Araçları → Kuyruk Durumu sekmesinin metinleri ve hesapları (React yok; Node'un çalıştırıcısıyla birim test edilir).

import type { QueueItem, QueueKind, QueueStatus, QueueSummary } from '../types/api.ts'

// Durum süzgeci: "active" bitmemiş (bekleyen + yeniden denenecek) satırlardır ve varsayılandır.
export type QueueFilter = 'active' | QueueStatus | ''

export const QUEUE_FILTERS: { value: QueueFilter; label: string }[] = [
  { value: 'active', label: 'Kuyruktakiler' },
  { value: 'retrying', label: 'Yeniden denenecek' },
  { value: 'sent', label: 'Gönderilen' },
  { value: 'failed', label: 'Vazgeçilen' },
  { value: '', label: 'Tümü' },
]

const KIND: Record<QueueKind, string> = {
  alert: 'Alert bildirimi',
  password_reset: 'Şifre sıfırlama',
  password_changed: 'Şifre değişti',
}

export const queueKindLabel = (kind: string): string => KIND[kind as QueueKind] ?? kind

export const QUEUE_KINDS: { value: QueueKind | ''; label: string }[] = [
  { value: '', label: 'Tüm türler' },
  ...(Object.keys(KIND) as QueueKind[]).map((k) => ({ value: k, label: KIND[k] })),
]

const STATUS: Record<QueueStatus, { label: string; tone: 'good' | 'warning' | 'critical' | 'neutral' }> = {
  pending: { label: 'bekliyor', tone: 'neutral' },
  retrying: { label: 'yeniden denenecek', tone: 'warning' },
  sent: { label: 'gönderildi', tone: 'good' },
  failed: { label: 'vazgeçildi', tone: 'critical' },
}

export const queueStatus = (status: QueueStatus) => STATUS[status] ?? { label: status, tone: 'neutral' as const }

// activeCount, kuyrukta teslim bekleyen (bitmemiş) satır sayısıdır.
export const activeCount = (s: Pick<QueueSummary, 'pending' | 'retrying'>): number => s.pending + s.retrying

// durationText, bir süreyi en büyük iki birimiyle söyler: "45 sn", "12 dk", "3 sa 5 dk", "2 gün 4 sa".
export function durationText(ms: number): string {
  const s = Math.max(0, Math.floor(ms / 1000))
  if (s < 60) return `${s} sn`
  const m = Math.floor(s / 60)
  if (m < 60) return `${m} dk`
  const h = Math.floor(m / 60)
  if (h < 24) return m % 60 ? `${h} sa ${m % 60} dk` : `${h} sa`
  const d = Math.floor(h / 24)
  return h % 24 ? `${d} gün ${h % 24} sa` : `${d} gün`
}

// oldestAgeText, bitmemiş en eski satırın ne kadardır beklediğidir; kuyruk boşsa "—".
export function oldestAgeText(oldestActiveAt: string | null, nowMs: number): string {
  if (!oldestActiveAt) return '—'
  const at = Date.parse(oldestActiveAt)
  return Number.isNaN(at) ? '—' : durationText(nowMs - at)
}

// attemptsText, "3 / 10" biçiminde deneme sayısıdır; hiç denenmemişse "—".
export function attemptsText(attempts: number, maxAttempts: number): string {
  return attempts === 0 ? '—' : `${attempts} / ${maxAttempts}`
}

// poolPct, bağlantı havuzunun kullanım yüzdesidir (kullanılan / en çok).
export function poolPct(pool: Pick<QueueSummary['db_pool'], 'acquired' | 'max'>): number {
  return pool.max > 0 ? Math.min(100, (pool.acquired / pool.max) * 100) : 0
}

// itemTime, satırın sonuçlandığı (ya da yeniden deneneceği) zamandır. Bekleyen satırın zamanı geçmişse ya da çok
// yakınsa işçi onu birazdan alacaktır ("sırada").
export function itemTime(i: Pick<QueueItem, 'status' | 'sent_at' | 'failed_at' | 'next_attempt_at'>, nowMs: number): { label: string; at: string | null } {
  switch (i.status) {
    case 'sent':
      return { label: 'gönderildi', at: i.sent_at }
    case 'failed':
      return { label: 'vazgeçildi', at: i.failed_at }
    default: {
      const next = i.next_attempt_at ? Date.parse(i.next_attempt_at) : Number.NaN
      if (Number.isNaN(next) || next <= nowMs) return { label: 'sırada', at: null }
      return { label: `${durationText(next - nowMs)} sonra denenecek`, at: i.next_attempt_at }
    }
  }
}
