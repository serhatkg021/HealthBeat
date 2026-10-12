// "Bu alert kime gider?" önizlemesinin saf mantığı (React yok; Node'un çalıştırıcısıyla birim test edilir).

import type { NotificationPreview, PreviewSkipReason, PreviewSource } from '../types/api.ts'
import { channelLabel } from './notificationRules.ts'

// sourceLabel, alıcının nereden geldiğidir; sunucu kuralında sunucunun adı (biliniyorsa) yazılır.
export function sourceLabel(s: PreviewSource, hostTitle?: string): string {
  switch (s.kind) {
    case 'system':
      return 'Sistem kuralı'
    case 'organization':
      return `Organizasyon: ${s.name ?? '—'}`
    default:
      return hostTitle ? `Sunucu: ${hostTitle}` : 'Sunucu kuralı'
  }
}

// alsoLabel, aynı adresi ayrıca veren kaynaklardır ("ayrıca: …"); yoksa boş. Alıcı yine tek ileti alır.
export function alsoLabel(also: PreviewSource[] | undefined, hostTitle?: string): string {
  if (!also || also.length === 0) return ''
  return `ayrıca: ${also.map((s) => sourceLabel(s, hostTitle)).join(', ')}`
}

const SKIP_REASON: Record<PreviewSkipReason, string> = {
  channel_off: 'kanal kapalı',
  channel_not_supported: 'kanal henüz desteklenmiyor',
  no_access: 'erişimi yok: bu sunucuyu artık göremiyor',
}

export const skipReasonLabel = (r: string | undefined): string => (r ? (SKIP_REASON[r as PreviewSkipReason] ?? r) : '')

// systemCounts, sistem kurallarını göremeyen kullanıcıya gelen sayıları metne çevirir: gidenler tek satırda
// ("E-posta: 2 · SMS: 1"), gitmeyenler nedenleriyle ayrı satırlarda. Sistem kuralları görünüyorsa (özet yok) ikisi de
// boştur.
export function systemCounts(summary: NotificationPreview['system_summary']): { sent: string; skipped: string[] } {
  const sent: string[] = []
  const skipped: string[] = []
  for (const c of summary ?? []) {
    const text = `${channelLabel(c.channel)}: ${c.count}`
    if (c.reason) skipped.push(`${text} (${skipReasonLabel(c.reason)})`)
    else sent.push(text)
  }
  return { sent: sent.join(' · '), skipped }
}

// nobodyNotified, bildirimin kimseye gitmeyeceğini söyler: ne gidecek bir alıcı ne de sistem kurallarından gidecek biri
// var.
export function nobodyNotified(p: NotificationPreview): boolean {
  return p.recipients.length === 0 && !(p.system_summary ?? []).some((c) => !c.reason)
}
