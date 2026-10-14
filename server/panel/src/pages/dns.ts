// Envanter'in Ağ kartındaki DNS satırlarının metinleri — saf mantık. DNS yalnızca bilgidir: alert üretmez; hiç sunucu
// tanımlı olmaması da bir uyarı değil, nottur (bazı makineler bilerek DNS'siz çalışır).
import type { DNSConfig } from '../types/api.ts'

// Çözümleyici: uygulamaların adları kime sorduğu.
export function resolverLabel(d: DNSConfig): string {
  if (d.resolver === 'resolv.conf') return '/etc/resolv.conf (doğrudan)'
  switch (d.mode) {
    case 'stub':
      return 'systemd-resolved (stub, 127.0.0.53)'
    case 'uplink':
      return 'systemd-resolved (resolv.conf sunucuları doğrudan listeliyor)'
    case 'static':
      return 'systemd-resolved (sabit resolv.conf)'
    default:
      return 'systemd-resolved'
  }
}

const ON_OFF: Record<string, string> = { yes: 'açık', no: 'kapalı' }

export function dnssecLabel(v?: string): string {
  if (!v) return '—'
  return v === 'allow-downgrade' ? 'destekleniyorsa' : (ON_OFF[v] ?? v)
}

export function dnsOverTLSLabel(v?: string): string {
  if (!v) return '—'
  return v === 'opportunistic' ? 'fırsat buldukça' : (ON_OFF[v] ?? v)
}

export interface ServerGroup {
  // Arayüz adı; genel sunucular için undefined.
  iface?: string
  addresses: string[]
}

// Sunucular arayüze göre gruplanır (agent'ın gönderdiği sırayla); genel sunucular önce.
export function serverGroups(d: DNSConfig): ServerGroup[] {
  const groups: ServerGroup[] = []
  for (const s of d.servers ?? []) {
    const iface = s.interface || undefined
    let g = groups.find((x) => x.iface === iface)
    if (!g) {
      g = { iface, addresses: [] }
      groups.push(g)
    }
    g.addresses.push(s.address)
  }
  return [...groups.filter((g) => !g.iface), ...groups.filter((g) => g.iface)]
}

export interface DomainItem {
  name: string
  routingOnly: boolean
}

// Alan adları: önce arama alanları, sonra yalnızca yönlendirme alanları.
export function domainItems(d: DNSConfig): DomainItem[] {
  return [
    ...(d.search_domains ?? []).map((name) => ({ name, routingOnly: false })),
    ...(d.routing_domains ?? []).map((name) => ({ name, routingOnly: true })),
  ]
}
