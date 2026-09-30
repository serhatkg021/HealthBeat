// Bildirim kuralları için saf mantık (React yok; Node'un çalıştırıcısıyla birim test edilir).

import type { AlertLevel, ChannelOption, NotificationChannel, NotificationRoute, RecipientCandidate } from '../types/api.ts'

const CHANNEL_LABELS: Record<NotificationChannel, string> = {
  email: 'E-posta',
  sms: 'SMS',
  slack: 'Slack',
  discord: 'Discord',
  telegram: 'Telegram',
}

export const channelLabel = (c: string): string => CHANNEL_LABELS[c as NotificationChannel] ?? c

export interface ChannelChoice {
  id: NotificationChannel
  label: string
  // Seçilemiyorsa nedeni ("ayar gerekli", "kapalı"); seçilebiliyorsa undefined.
  unavailable?: string
}

// channelChoices, kural eklerken gösterilecek kanallardır: yalnızca server'ın gönderebildiği ve kişiye giden kanallar
// (ortak kanallar yalnızca sistem sahiplerine gider, kurallarda yer almaz). Kapalı ya da ayarı eksik olan listede kalır
// ama seçilemez; neden yanında yazar.
export function channelChoices(options: ChannelOption[]): ChannelChoice[] {
  return options
    .filter((o) => o.implemented && o.personal)
    .map((o) => ({
      id: o.channel,
      label: channelLabel(o.channel),
      unavailable: !o.ready ? 'ayar gerekli' : !o.enabled ? 'kapalı' : undefined,
    }))
}

export const LEVEL_CHOICES: AlertLevel[] = ['info', 'warning', 'critical']

const SOURCE_LABEL: Record<RecipientCandidate['source'], string> = {
  super_admin: 'Süper Admin',
  org_admin: 'Organizasyon Admin',
  operator: 'Operatör',
  contact: 'İletişim kişisi',
}

// Aday listesindeki bir alıcının benzersiz anahtarı (kullanıcı ve kişi kimlikleri ayrı tablolardan gelir).
export const candidateKey = (c: Pick<RecipientCandidate, 'user_id' | 'contact_id'>): string =>
  c.user_id ? `user:${c.user_id}` : `contact:${c.contact_id ?? ''}`

export const routeRecipientKey = (r: Pick<NotificationRoute, 'user_id' | 'contact_id'>): string =>
  r.user_id ? `user:${r.user_id}` : `contact:${r.contact_id ?? ''}`

export function candidateLabel(c: RecipientCandidate): string {
  const target = c.email ?? c.phone ?? ''
  return `${c.name}${target && target !== c.name ? ` — ${target}` : ''} (${SOURCE_LABEL[c.source]})`
}

// Yeni kural için seçilebilecek alıcılar: aynı kanalda zaten kuralı olanlar çıkarılır (server aynı kapsam+alıcı+kanal
// tekrarını reddeder).
export function candidatesForNewRoute(candidates: RecipientCandidate[], routes: NotificationRoute[], channel: NotificationChannel): RecipientCandidate[] {
  const taken = new Set(routes.filter((r) => r.channel === channel).map(routeRecipientKey))
  return candidates.filter((c) => !taken.has(candidateKey(c)))
}

// Kapsamın bildirimlerinin kime gittiğini anlatan tek paragraf: sistem sahipleri her zaman alır, kurallar ek alıcıdır ve
// sunucu, organizasyon ve üst organizasyon kuralları toplanır (hiçbiri diğerini ezmez).
export function describeCoverage(scope: 'organization' | 'host', ruleCount: number): string {
  const base = 'Alert bildirimleri her zaman sistem sahiplerine gider (Ayarlar → Sistem sahipleri); buradaki kişiler ek alıcıdır.'
  if (scope === 'host') {
    return `${base} Bu sunucunun alert’lerinde sunucu kuralları, organizasyonunun ve üst organizasyonlarının kurallarıyla birlikte uygulanır.${ruleCount === 0 ? ' Bu sunucuya özel ek alıcı yok.' : ''}`
  }
  return `${base} Bu kurallar organizasyondaki ve alt organizasyonlarındaki bütün sunucular için geçerlidir; sunucu kurallarıyla birlikte uygulanır.${ruleCount === 0 ? ' Bu organizasyonda ek alıcı yok.' : ''}`
}
