// Alert detayındaki bildirim geçmişinin metinleri (React yok; Node'un çalıştırıcısıyla birim test edilir).

import type { AlertNotification } from '../types/api.ts'

// recipientsText, alıcıları ya da (adresleri görme izni yoksa) yalnızca sayısını söyler.
export function recipientsText(n: Pick<AlertNotification, 'recipients' | 'recipient_count'>): string {
  if (n.recipients && n.recipients.length > 0) return n.recipients.join(', ')
  return `${n.recipient_count} alıcı`
}

// attemptsText, deneme sayısını söyler; tek denemede gittiyse boştur (söylenecek bir şey yok).
export function attemptsText(n: Pick<AlertNotification, 'status' | 'attempts'>): string {
  if (n.status === 'sent' && n.attempts <= 1) return ''
  if (n.attempts === 0) return 'henüz denenmedi'
  return `${n.attempts} deneme`
}

// deliveryTime, satırın ne zaman sonuçlandığını (ya da bir sonraki denemeyi) döndürür.
export function deliveryTime(n: Pick<AlertNotification, 'status' | 'sent_at' | 'failed_at' | 'next_attempt_at'>): {
  label: string
  at?: string
} {
  switch (n.status) {
    case 'sent':
      return { label: 'gönderildi', at: n.sent_at }
    case 'failed':
      return { label: 'vazgeçildi', at: n.failed_at }
    default:
      return { label: 'sonraki deneme', at: n.next_attempt_at }
  }
}

// NotificationGroup, bir alert olayının (açıldı, seviye değişti, çözüldü) bildirimleridir: her alıcı ayrı bir satırdır
// (alıcılar birbirini görmez), ama aynı olayın satırları aynı transaction'da, aynı zamanla yazılır.
export interface NotificationGroup {
  key: string
  event: AlertNotification['event']
  level: AlertNotification['level']
  created_at: string
  subject: string
  body: string
  items: AlertNotification[]
}

// groupNotifications, satırları olay başına gruplar; sıra korunur.
export function groupNotifications(items: AlertNotification[]): NotificationGroup[] {
  const groups: NotificationGroup[] = []
  const byKey = new Map<string, NotificationGroup>()
  for (const n of items) {
    const key = `${n.event}|${n.level}|${n.created_at}`
    let g = byKey.get(key)
    if (!g) {
      g = { key, event: n.event, level: n.level, created_at: n.created_at, subject: n.subject, body: n.body, items: [] }
      byKey.set(key, g)
      groups.push(g)
    }
    g.items.push(n)
  }
  return groups
}

const STATUS_WORD: Record<AlertNotification['status'], string> = { sent: 'gönderildi', pending: 'bekliyor', failed: 'vazgeçildi' }

// groupSummary, bir olayın teslim özetidir: "3 alıcı: 2 gönderildi, 1 bekliyor".
export function groupSummary(items: Pick<AlertNotification, 'status' | 'recipient_count'>[]): string {
  const total = items.reduce((sum, n) => sum + n.recipient_count, 0)
  const parts = (['sent', 'pending', 'failed'] as const)
    .map((s) => ({ s, n: items.filter((i) => i.status === s).reduce((sum, i) => sum + i.recipient_count, 0) }))
    .filter((p) => p.n > 0)
    .map((p) => `${p.n} ${STATUS_WORD[p.s]}`)
  return `${total} alıcı: ${parts.join(', ')}`
}
