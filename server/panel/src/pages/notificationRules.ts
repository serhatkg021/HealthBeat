// Bildirim kuralları için saf mantık (React yok; Node'un çalıştırıcısıyla birim test edilir).

import type { AlertLevel, NotificationChannel, NotificationRoute, RecipientCandidate } from '../types/api.ts'

// Kanallar: veritabanı hepsini tanır, API yalnızca implemented olanı (şimdilik e-posta) kabul eder.
export const CHANNELS: { id: NotificationChannel; label: string; implemented: boolean }[] = [
  { id: 'email', label: 'E-posta', implemented: true },
  { id: 'sms', label: 'SMS', implemented: false },
  { id: 'slack', label: 'Slack', implemented: false },
  { id: 'discord', label: 'Discord', implemented: false },
  { id: 'telegram', label: 'Telegram', implemented: false },
]

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

// Kapsamın hangi alıcılara gittiğini anlatan tek paragraf.
export function describeCoverage(scope: 'organization' | 'host', ruleCount: number): string {
  if (ruleCount === 0) {
    return scope === 'host'
      ? 'Bu sunucuya özel kural yok: organizasyonun kuralları (o da yoksa varsayılan alıcılar: süper adminler ve organizasyon yöneticileri, e-posta, uyarı ve üstü) uygulanır.'
      : 'Bu organizasyonda kural yok: varsayılan alıcılar bilgilendirilir (süper adminler ve organizasyon yöneticileri, e-posta, uyarı ve üstü). Alt organizasyonlar üst şirketin kurallarını miras alır.'
  }
  return scope === 'host'
    ? 'Bu sunucu için YALNIZCA aşağıdaki kurallar geçerlidir; organizasyon kuralları ve varsayılan alıcılar bu sunucu için devre dışıdır.'
    : 'Bu organizasyon (ve kendi kuralı olmayan alt organizasyonları) için YALNIZCA aşağıdaki kurallar geçerlidir; varsayılan alıcılar devre dışıdır. Bir sunucuya özel kural eklenirse o sunucuda yalnızca o geçerli olur.'
}
