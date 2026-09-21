// Makine envanterinin (Host.host_info) panelde nasıl gösterileceği — saf mantık. Envanter yalnızca
// bilgi içindir: uyarılar (hostname/IP uyuşmazlığı) alert üretmez, ilgili satırın yanında gösterilir.
import type { HostInfo } from '../types/api.ts'

// Agent envanteri (host_info) protokol 3 ile gelir.
export const supportsInventory = (c: { agent_protocol?: number | null }): boolean => (c.agent_protocol ?? 0) >= 3

export function osLabel(h?: HostInfo): string {
  const os = h?.os
  if (!os) return '—'
  return os.pretty_name || [os.name, os.version_id].filter(Boolean).join(' ') || os.id || '—'
}

export function kernelLabel(h?: HostInfo): string {
  const k = h?.kernel
  if (!k || (!k.release && !k.arch)) return '—'
  return [k.release, k.arch].filter(Boolean).join(' · ')
}

export function virtualizationLabel(h?: HostInfo): string {
  const v = h?.virtualization
  const machine = [h?.machine?.vendor, h?.machine?.model].filter(Boolean).join(' ')
  const vendor = v?.vendor ? ` (${v.vendor})` : ''
  switch (v?.kind) {
    case 'physical':
      return machine ? `Fiziksel · ${machine}` : 'Fiziksel'
    case 'vm':
      return `Sanal makine${vendor}${machine ? ` · ${machine}` : ''}`
    case 'container':
      return `Konteyner${vendor}`
    default:
      return machine || '—'
  }
}

export const SECURITY_MODULE_LABEL: Record<string, string> = {
  apparmor: 'AppArmor',
  'selinux-enforcing': 'SELinux (zorlayıcı)',
  'selinux-permissive': 'SELinux (izin verici)',
  none: 'Yok',
}

// 90 sn -> "1 dk"; 3 gün 4 sa -> "3 gün 4 sa"; en çok iki birim.
export function formatUptime(seconds?: number): string {
  if (seconds === undefined || seconds === null || !(seconds >= 0)) return '—'
  const s = Math.floor(seconds)
  const days = Math.floor(s / 86400)
  const hours = Math.floor((s % 86400) / 3600)
  const mins = Math.floor((s % 3600) / 60)
  if (days > 0) return hours > 0 ? `${days} gün ${hours} sa` : `${days} gün`
  if (hours > 0) return mins > 0 ? `${hours} sa ${mins} dk` : `${hours} sa`
  return mins > 0 ? `${mins} dk` : `${s} sn`
}

export function loadText(h?: HostInfo): string {
  const la = h?.load_avg
  if (!la || la.length !== 3) return '—'
  return `${la.map((v) => v.toFixed(2)).join(' · ')} (1 / 5 / 15 dk)`
}

export function swapText(h?: HostInfo): string {
  const s = h?.swap
  if (!s) return 'Yok'
  return `${s.used_mb} / ${s.total_mb} MB`
}

// Adresi ön eksiz ve karşılaştırılabilir hâle getirir (IPv6'da büyük/küçük harf ve sıfır sıkıştırması).
export function normalizeIP(addr: string): string {
  const bare = addr.trim().split('/')[0].toLowerCase()
  if (bare.includes(':')) {
    try {
      return new URL(`http://[${bare}]`).hostname.replace(/^\[|\]$/g, '')
    } catch {
      return bare
    }
  }
  return bare
}

// Panelde kayıtlı IP, agent'ın bildirdiği adresler arasında yoksa bunu söyler. Agent adres bildirmediyse
// (boş liste) bilinmiyor: uyarı yok. NAT/genel IP arkasındaki push sunucularında bu uyarı beklenebilir.
export function ipMismatch(registered: string, h?: HostInfo): string {
  const addrs = h?.addresses
  if (!addrs || addrs.length === 0 || !registered.trim()) return ''
  const want = normalizeIP(registered)
  return addrs.some((a) => normalizeIP(a.address) === want) ? '' : `Panelde kayıtlı IP (${registered}) agent'ın bildirdiği adresler arasında yok`
}

export function shortMachineId(h?: HostInfo): string {
  return h?.machine_id_hash ? `${h.machine_id_hash.slice(0, 8)}…` : '—'
}
