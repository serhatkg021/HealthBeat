// Agent sürümünün panelde nasıl sınıflandırılıp gösterileceği — saf mantık (bkz.
// docs/COMPATIBILITY.md). Server hiçbir agent'ı sürüm yüzünden reddetmez; bu sınıflandırma yalnızca
// "hangi sunucuların agent'ı güncellenmeli" sorusunu cevaplamak içindir.

// Server'ın GET /api/v1/meta ile bildirdiği sürüm politikası; boş = tanımsız.
export interface AgentPolicy {
  latest: string
  min: string
}

export const noPolicy: AgentPolicy = { latest: '', min: '' }

// legacy    : sürüm bildirmeyen (protokol 1) eski agent — büyük ihtimalle güncellenmeli
// unknown   : henüz hiç rapor gelmedi ya da sürüm okunamadı
export type AgentKind = 'current' | 'outdated' | 'unsupported' | 'legacy' | 'unknown'

export interface AgentFields {
  agent_version?: string | null
  agent_protocol?: number | null
}

const SEMVER = /^(\d+)\.(\d+)\.(\d+)(?:-([0-9A-Za-z.-]+))?(?:\+[0-9A-Za-z.-]+)?$/

// a < b ise -1, a > b ise 1, eşitse 0. Geçersiz bir sürüm karşılaştırılamaz: 0 döner ki bilinmeyen
// bir sürüm sebepsiz yere "eski" diye işaretlenmesin. Önsürüm, aynı çekirdeğin sürümünden küçüktür.
export function compareVersions(a: string, b: string): number {
  const ma = SEMVER.exec(a)
  const mb = SEMVER.exec(b)
  if (!ma || !mb) return 0
  for (let i = 1; i <= 3; i++) {
    const d = Number(ma[i]) - Number(mb[i])
    if (d !== 0) return d < 0 ? -1 : 1
  }
  const pa = ma[4]
  const pb = mb[4]
  if (pa === pb) return 0
  if (pa === undefined) return 1
  if (pb === undefined) return -1
  return pa < pb ? -1 : 1
}

export function agentKind(c: AgentFields, policy: AgentPolicy): AgentKind {
  const version = c.agent_version || ''
  const protocol = c.agent_protocol ?? 0
  if (version === '') {
    if (protocol === 0) return 'unknown'
    return protocol <= 1 ? 'legacy' : 'unknown'
  }
  if (policy.min && compareVersions(version, policy.min) < 0) return 'unsupported'
  if (policy.latest && compareVersions(version, policy.latest) < 0) return 'outdated'
  return 'current'
}

// Güncellenmesi gereken agent mı (sayaç ve süzgeç bunu kullanır).
export const needsUpdate = (k: AgentKind): boolean => k === 'legacy' || k === 'outdated' || k === 'unsupported'

export const AGENT_KIND_LABEL: Record<AgentKind, string> = {
  current: 'güncel',
  outdated: 'güncelleme var',
  unsupported: 'desteklenmiyor',
  legacy: 'eski agent',
  unknown: 'bilinmiyor',
}

export const AGENT_KIND_TONE: Record<AgentKind, 'good' | 'warning' | 'critical' | 'neutral'> = {
  current: 'good',
  outdated: 'warning',
  unsupported: 'critical',
  legacy: 'warning',
  unknown: 'neutral',
}

export type Tone = 'good' | 'warning' | 'critical' | 'neutral'

// Listelerdeki ve sunucu sayfasındaki tek rozet: metin yalnızca sürümdür ("v1.3.0"), durum rengiyle
// anlatılır; durumun sözlü karşılığı fareyle üzerine gelince (title) ve ekran okuyucu için verilir.
export interface AgentBadgeInfo {
  text: string
  tone: Tone
  title: string
}

const capitalize = (s: string) => s.charAt(0).toLocaleUpperCase('tr-TR') + s.slice(1)

export function agentBadgeInfo(c: AgentFields, policy: AgentPolicy): AgentBadgeInfo {
  const kind = agentKind(c, policy)
  const hint = agentHint(kind, policy)
  const status = capitalize(AGENT_KIND_LABEL[kind])
  const title = hint ? `${status}. ${hint}` : status
  if (c.agent_version) return { text: `v${c.agent_version}`, tone: AGENT_KIND_TONE[kind], title }
  // Sürüm yok: eski (protokol 1) agent kendini "eski agent" diye gösterir; hiç rapor gelmediyse çizgi.
  if (kind === 'legacy') return { text: AGENT_KIND_LABEL.legacy, tone: AGENT_KIND_TONE.legacy, title }
  return { text: '—', tone: 'neutral', title: 'Sürüm bilgisi yok (henüz rapor gelmedi ya da okunamadı).' }
}

// Ne yapılması gerektiğini söyleyen açıklama; güncel/bilinmeyen için boş.
export function agentHint(kind: AgentKind, policy: AgentPolicy): string {
  switch (kind) {
    case 'legacy':
      return `Bu agent sürüm bildirmiyor (eski sürüm)${policy.latest ? `; güncel sürüm ${policy.latest}` : ''}. Yeni özellikler (donanım özeti gibi) için güncelleyin.`
    case 'outdated':
      return `Güncel agent sürümü ${policy.latest}. Bu agent'ı güncelleyin.`
    case 'unsupported':
      return `Desteklenen en düşük agent sürümü ${policy.min}. Bu agent'ı güncelleyin.`
    default:
      return ''
  }
}

// Agent donanım özetini (cpu_cores, ram_total_mb, physical_disks) gönderebilir mi? Protokol 2+.
export const supportsHardwareSummary = (c: AgentFields): boolean => (c.agent_protocol ?? 0) >= 2

// Agent, server'ın tanımadığı alanlar gönderiyorsa (agent server'dan yeni) operatöre söylenecek
// uyarı; yoksa boş metin. Alan adları server'da temizlenip sınırlandığı için olduğu gibi gösterilir.
export function unsupportedFieldsNotice(fields?: string[] | null): string {
  if (!fields || fields.length === 0) return ''
  return `Bu agent, server'ın bu sürümünün tanımadığı alanlar gönderiyor (${fields.join(', ')}). Bu alanlar şu an işlenmiyor; server'ı güncelleyin.`
}
