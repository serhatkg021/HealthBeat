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
