// Sunucunun systemd servisleri (protokol 4) ve izlenen servis seçimi — saf mantık. İzlenen bir servis çalışmazsa
// "Servis çalışmıyor" durum kuralına göre alert açılır; seçim yalnızca sunucu bazındadır. React yok: Node'un
// çalıştırıcısıyla birim test edilir.

import type { HostService } from '../types/api.ts'

export type Tone = 'good' | 'warning' | 'critical' | 'neutral'

// Servisin durumu: systemd'nin active (genel) ve sub (ayrıntı) alanlarından.
export function serviceState(s: Pick<HostService, 'active' | 'sub'>): { label: string; tone: Tone } {
  switch (s.active) {
    case 'active':
      if (s.sub === 'exited') return { label: 'tamamlandı', tone: 'neutral' } // tek seferlik servis çalışıp bitti
      return { label: 'çalışıyor', tone: 'good' }
    case 'failed':
      return { label: 'hata', tone: 'critical' }
    case 'activating':
      return s.sub === 'auto-restart' ? { label: 'yeniden başlatılıyor', tone: 'warning' } : { label: 'başlatılıyor', tone: 'warning' }
    case 'deactivating':
      return { label: 'durduruluyor', tone: 'warning' }
    case 'reloading':
      return { label: 'yeniden yükleniyor', tone: 'warning' }
    case 'inactive':
      return { label: 'durmuş', tone: 'neutral' }
  }
  return { label: s.active || '—', tone: 'neutral' }
}

const ENABLED: Record<string, string> = {
  enabled: 'evet',
  'enabled-runtime': 'evet (geçici)',
  disabled: 'hayır',
  static: 'bağımlılıkla',
  indirect: 'dolaylı',
  masked: 'maskeli',
  generated: 'üretilmiş',
  alias: 'takma ad',
}

// Açılışta başlayıp başlamadığı (UnitFileState).
export const enabledLabel = (enabled?: string): string => (enabled ? (ENABLED[enabled] ?? enabled) : '—')

// Dikkat isteyen servis: hata vermiş, sürekli yeniden başlıyor ya da izlendiği hâlde çalışmıyor.
export function isProblem(s: Pick<HostService, 'active' | 'sub' | 'watched'>): boolean {
  if (s.active === 'failed' || s.sub === 'auto-restart') return true
  return s.watched && s.active !== 'active'
}

export interface ServiceFilter {
  q: string
  problemsOnly: boolean
  watchedOnly: boolean
}

// Süzülmüş liste: önce sorunlular, sonra ada göre. Arama ad ve açıklamada, büyük/küçük harf ayırmadan.
export function filterServices(list: HostService[], f: ServiceFilter): HostService[] {
  const q = f.q.trim().toLocaleLowerCase('tr-TR')
  return list
    .filter((s) => (!f.problemsOnly || isProblem(s)) && (!f.watchedOnly || s.watched))
    .filter((s) => q === '' || s.name.toLocaleLowerCase('tr-TR').includes(q) || (s.description ?? '').toLocaleLowerCase('tr-TR').includes(q))
    .sort((a, b) => Number(isProblem(b)) - Number(isProblem(a)) || a.name.localeCompare(b.name))
}

// İzlenen ama son tam listede olmayan servisler (kaldırılmış ya da adı yanlış yazılmış olabilir).
export function notReported(watched: string[], services: HostService[]): string[] {
  const names = new Set(services.map((s) => s.name))
  return watched.filter((w) => !names.has(w)).sort()
}

// Seçimin yeni hâli (sıralı, tekrarsız).
export function toggleWatched(watched: string[], name: string, on: boolean): string[] {
  const set = new Set(watched)
  if (on) set.add(name)
  else set.delete(name)
  return [...set].sort()
}

// Server'ın model.ValidateServiceList sınırları.
const MAX_WATCHED = 256
const MAX_NAME_BYTES = 256

// Elle eklenen ad: uzantısız yazılırsa ".service" eklenir (agent adları uzantısıyla bildirir; server birebir karşılaştırır).
export function parseServiceName(text: string, watched: string[]): { name: string } | { error: string } {
  let name = text.trim()
  if (name === '') return { error: 'Bir servis adı girin.' }
  if (!name.includes('.')) name += '.service'
  // eslint-disable-next-line no-control-regex
  if (/[\u0000-\u001f\u007f-\u009f]/.test(name)) return { error: 'Servis adı kontrol karakteri içeremez.' }
  if (new TextEncoder().encode(name).length > MAX_NAME_BYTES) return { error: `Servis adı ${MAX_NAME_BYTES} bayttan uzun olamaz.` }
  if (watched.includes(name)) return { error: `${name} zaten izleniyor.` }
  if (watched.length >= MAX_WATCHED) return { error: `En fazla ${MAX_WATCHED} servis izlenebilir.` }
  return { name }
}

export interface ServiceCounts {
  total: number
  running: number
  problems: number
  watched: number
}

export function serviceCounts(list: HostService[], watched: string[]): ServiceCounts {
  return {
    total: list.length,
    running: list.filter((s) => s.active === 'active' && s.sub !== 'exited').length,
    problems: list.filter(isProblem).length,
    watched: watched.length,
  }
}
