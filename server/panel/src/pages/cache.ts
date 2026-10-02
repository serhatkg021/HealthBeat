// Sistem Araçları → Cache Durumu sekmesinin metinleri ve hesapları (React yok; Node'un çalıştırıcısıyla birim test edilir).

import type { CacheStatus, LimiterEntry, LimiterKeyKind, RateLimiterStatus } from '../types/api.ts'
import { durationText } from './queue.ts'

const LIMITERS: Record<string, { title: string; description: string }> = {
  login_failures: { title: 'Başarısız girişler', description: 'Panel girişi ve oturum yenilemede başarısız denemeler; kaynak IP başına.' },
  ingest_failures: { title: 'Başarısız agent kimlik doğrulamaları', description: 'Rapor gönderirken kimliği doğrulanamayan istekler; kaynak IP başına.' },
  ingest_rate: { title: 'Agent rapor hızı', description: 'Kimliği doğrulanmış push agent’ların gönderdiği raporlar; sunucu başına.' },
  reset_ips: { title: 'Şifre sıfırlama istekleri (IP)', description: '“Şifremi unuttum” istekleri; kaynak IP başına.' },
  reset_emails: { title: 'Şifre sıfırlama istekleri (e-posta)', description: '“Şifremi unuttum” istekleri; hedef e-posta adresi başına.' },
}

// limiterInfo, bir sınırlayıcının başlığı ve açıklamasıdır; panelin tanımadığı bir sınırlayıcı kimliğiyle gösterilir.
export const limiterInfo = (id: string) => LIMITERS[id] ?? { title: id, description: '' }

const KEY_KIND: Record<LimiterKeyKind, string> = { ip: 'IP adresi', email: 'E-posta', host: 'Sunucu' }

export const keyKindLabel = (kind: string): string => KEY_KIND[kind as LimiterKeyKind] ?? 'Anahtar'

// usedOf, bir anahtarın harcadığı hakkı kapasiteye oranlar: "anlık kullanım / toplam". Kalan hak kesirli olabilir
// (kova sürekli dolar); harcanan yukarı yuvarlanır ki tek bir istek "0 / 5" görünmesin.
export function usedOf(entry: Pick<LimiterEntry, 'remaining'>, burst: number): { used: number; pct: number; text: string } {
  const used = Math.min(burst, Math.max(0, Math.ceil(burst - entry.remaining - 1e-9)))
  return { used, pct: burst > 0 ? (used / burst) * 100 : 0, text: `${used} / ${burst}` }
}

// limiterRate, sınırlayıcının kuralını söyler: "dakikada 10, kapasite 20"; kapalıysa "kapalı".
export function limiterRate(l: Pick<RateLimiterStatus, 'enabled' | 'per_minute' | 'burst'>): string {
  if (!l.enabled) return 'kapalı'
  const perMinute = Number.isInteger(l.per_minute) ? String(l.per_minute) : l.per_minute.toFixed(1)
  return `dakikada ${perMinute}, kapasite ${l.burst}`
}

// hiddenKeys, listelenmeyen anahtar sayısıdır (server en çok 200 anahtar gönderir).
export const hiddenKeys = (l: Pick<RateLimiterStatus, 'keys' | 'entries'>): number => Math.max(0, l.keys - l.entries.length)

// expiresInText, bir önbellek girdisinin kalan ömrüdür; süresi geçtiyse "süresi doldu".
export function expiresInText(expiresAt: string, nowMs: number): string {
  const at = Date.parse(expiresAt)
  if (Number.isNaN(at) || at <= nowMs) return 'süresi doldu'
  return durationText(at - nowMs)
}

// agoText, geçmiş bir anı "12 dk önce" diye söyler.
export function agoText(at: string, nowMs: number): string {
  const t = Date.parse(at)
  return Number.isNaN(t) ? '—' : `${durationText(nowMs - t)} önce`
}

type Cert = NonNullable<CacheStatus['tls_certificate']>

// certExpiry, sertifikanın kalan süresi ve durum rengidir: süresi dolmuş ya da 14 günden az kritik, 30 günden az uyarı.
export function certExpiry(cert: Pick<Cert, 'not_after'>, nowMs: number): { text: string; tone: 'good' | 'warning' | 'critical' } {
  const left = Date.parse(cert.not_after) - nowMs
  if (Number.isNaN(left)) return { text: '—', tone: 'warning' }
  if (left <= 0) return { text: 'süresi doldu', tone: 'critical' }
  const days = Math.floor(left / 86_400_000)
  const text = days >= 1 ? `${days} gün kaldı` : `${durationText(left)} kaldı`
  return { text, tone: days < 14 ? 'critical' : days < 30 ? 'warning' : 'good' }
}

// certNames, sertifikanın geçerli olduğu adlar ve IP'lerdir; hiç yoksa (eski tip sertifika) boş.
export const certNames = (cert: Pick<Cert, 'dns_names' | 'ips'>): string[] => [...cert.dns_names, ...cert.ips]
