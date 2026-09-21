import { alertLevelLabel, alertMetricLabel, hostStatusLabel } from '../labels.ts'
import { agentKind, needsUpdate, noPolicy, type AgentPolicy } from './agentStatus.ts'
import type { Alert, AlertLevel, AlertType, HostMode, HostStatus, DashboardOverview, OverviewHost } from '../types/api.ts'

// Özet ekranı süzgeçleri: durum (adres çubuğunda tutulur), süzme ve sayım mantığı — hepsi saf.
//
// İki tür süzgeç vardır ve birlikte çalışır:
//  - sunucu süzgeçleri (organizasyon, durum, mod, arama) hangi sunucuların listeleneceğini seçer;
//  - alert süzgeçleri (seviye, metrik, zaman) hangi alert'lerin sayılacağını seçer.
// Bir alert süzgeci (ya da "yalnızca alert'i olanlar") açıksa, eşleşen alert'i olmayan sunucular da düşer:
// "kritik disk alert'i olan sunucular" sorusu böyle cevaplanır.

export const SINCE_WINDOWS = {
  '1saat': 60 * 60 * 1000,
  '24saat': 24 * 60 * 60 * 1000,
  '7gun': 7 * 24 * 60 * 60 * 1000,
} as const

export type SinceKey = keyof typeof SINCE_WINDOWS

export const LEVELS: readonly AlertLevel[] = ['critical', 'warning', 'info']
export const METRICS: readonly AlertType[] = ['cpu', 'ram', 'disk', 'docker_restart', 'host_offline', 'disk_missing']
const STATUSES: readonly HostStatus[] = ['online', 'offline']
const MODES: readonly HostMode[] = ['push', 'pull']

export interface DashboardFilters {
  orgs: string[]
  status: HostStatus | ''
  mode: HostMode | ''
  query: string
  withAlerts: boolean
  // Yalnızca agent'ı güncellenmesi gerekenler (eski / güncelleme var / desteklenmiyor).
  agentUpdate: boolean
  levels: AlertLevel[]
  metrics: AlertType[]
  since: SinceKey | ''
}

export const emptyFilters = (): DashboardFilters => ({
  orgs: [],
  status: '',
  mode: '',
  query: '',
  withAlerts: false,
  agentUpdate: false,
  levels: [],
  metrics: [],
  since: '',
})

// Adres çubuğundaki parametre adları.
const P = { org: 'org', status: 'durum', mode: 'mod', query: 'q', withAlerts: 'alertli', agentUpdate: 'agentguncelle', level: 'seviye', metric: 'metrik', since: 'zaman' } as const
const OWNED = Object.values(P)

const oneOf = <T extends string>(allowed: readonly T[], raw: string | null): T | '' => (raw !== null && (allowed as readonly string[]).includes(raw) ? (raw as T) : '')

// Bilinmeyen/bozuk değerler sessizce atılır (elle düzenlenmiş ya da eski bir bağlantı ekranı bozmasın);
// tekrarlar tekilleştirilir.
const manyOf = <T extends string>(allowed: readonly T[], raws: string[]): T[] => allowed.filter((a) => raws.includes(a))

export function parseFilters(params: URLSearchParams): DashboardFilters {
  return {
    orgs: [...new Set(params.getAll(P.org).filter((id) => id !== ''))],
    status: oneOf(STATUSES, params.get(P.status)),
    mode: oneOf(MODES, params.get(P.mode)),
    query: (params.get(P.query) ?? '').trim(),
    withAlerts: params.get(P.withAlerts) === '1',
    agentUpdate: params.get(P.agentUpdate) === '1',
    levels: manyOf(LEVELS, params.getAll(P.level)),
    metrics: manyOf(METRICS, params.getAll(P.metric)),
    since: oneOf(Object.keys(SINCE_WINDOWS) as SinceKey[], params.get(P.since)),
  }
}

// Süzgeç parametrelerini `params` üzerinde günceller; başka parametrelere (ör. sekme) dokunmaz.
export function writeFilters(params: URLSearchParams, f: DashboardFilters): URLSearchParams {
  const next = new URLSearchParams(params)
  for (const key of OWNED) next.delete(key)
  for (const id of f.orgs) next.append(P.org, id)
  if (f.status) next.set(P.status, f.status)
  if (f.mode) next.set(P.mode, f.mode)
  if (f.query.trim()) next.set(P.query, f.query.trim())
  if (f.withAlerts) next.set(P.withAlerts, '1')
  if (f.agentUpdate) next.set(P.agentUpdate, '1')
  for (const l of f.levels) next.append(P.level, l)
  for (const m of f.metrics) next.append(P.metric, m)
  if (f.since) next.set(P.since, f.since)
  return next
}

// Bilinmeyen organizasyon kimliklerini (silinmiş ya da yetkisiz) atar; aksi halde hiçbir şeyle
// eşleşmeyen bir süzgeç ekranı sebepsiz boşaltırdı.
export function pruneOrgs(f: DashboardFilters, knownOrgIds: string[]): DashboardFilters {
  const orgs = f.orgs.filter((id) => knownOrgIds.includes(id))
  return orgs.length === f.orgs.length ? f : { ...f, orgs }
}

export function alertFiltersActive(f: DashboardFilters): boolean {
  return f.levels.length > 0 || f.metrics.length > 0 || f.since !== ''
}

// Kaç süzgeç grubu açık (düğmedeki sayı rozeti için).
export function activeCount(f: DashboardFilters): number {
  return [
    f.orgs.length > 0,
    f.status !== '',
    f.mode !== '',
    f.query !== '',
    f.withAlerts,
    f.agentUpdate,
    f.levels.length > 0,
    f.metrics.length > 0,
    f.since !== '',
  ].filter(Boolean).length
}

export interface ServerRow {
  host: OverviewHost
  critical: number
  warning: number
}

export interface DashboardView {
  rows: ServerRow[]
  alerts: Alert[]
  counts: { total: number; online: number; offline: number; alerts: number; critical: number; warning: number }
}

const byNewest = (a: Alert, b: Alert) => Date.parse(b.created_at) - Date.parse(a.created_at)

export function applyFilters(data: Pick<DashboardOverview, 'hosts' | 'alerts'>, f: DashboardFilters, now: Date, policy: AgentPolicy = noPolicy): DashboardView {
  const needle = f.query.toLowerCase()
  const serverMatches = (c: OverviewHost) =>
    (f.orgs.length === 0 || f.orgs.includes(c.organization_id)) &&
    (f.status === '' || c.status === f.status) &&
    (f.mode === '' || c.mode === f.mode) &&
    (!f.agentUpdate || needsUpdate(agentKind(c, policy))) &&
    (needle === '' || c.title.toLowerCase().includes(needle) || c.ip.toLowerCase().includes(needle))

  const cutoff = f.since === '' ? -Infinity : now.getTime() - SINCE_WINDOWS[f.since]
  const alertMatches = (a: Alert) =>
    (f.levels.length === 0 || f.levels.includes(a.level)) &&
    (f.metrics.length === 0 || f.metrics.includes(a.alert_type)) &&
    Date.parse(a.created_at) >= cutoff

  const servers = data.hosts.filter(serverMatches)
  const matched = data.alerts.filter(alertMatches)

  const perHost = new Map<string, { critical: number; warning: number }>()
  for (const a of matched) {
    const n = perHost.get(a.host_id) ?? { critical: 0, warning: 0 }
    if (a.level === 'critical') n.critical++
    else n.warning++
    perHost.set(a.host_id, n)
  }

  const requireAlerts = f.withAlerts || alertFiltersActive(f)
  const rows = servers
    .filter((c) => !requireAlerts || perHost.has(c.id))
    .map((host) => ({ host, critical: perHost.get(host.id)?.critical ?? 0, warning: perHost.get(host.id)?.warning ?? 0 }))
    .sort(
      (a, b) =>
        b.critical - a.critical ||
        b.warning - a.warning ||
        Number(b.host.status === 'offline') - Number(a.host.status === 'offline') ||
        a.host.title.localeCompare(b.host.title),
    )

  // Alert listesi yalnızca görünen sunucularınkidir (sunucu düşmüşse alert'i de düşer).
  const visible = new Set(rows.map((r) => r.host.id))
  const alerts = matched.filter((a) => visible.has(a.host_id)).sort(byNewest)
  const online = rows.filter((r) => r.host.status === 'online').length
  return {
    rows,
    alerts,
    counts: {
      total: rows.length,
      online,
      offline: rows.length - online,
      alerts: alerts.length,
      critical: alerts.filter((a) => a.level === 'critical').length,
      warning: alerts.filter((a) => a.level === 'warning').length,
    },
  }
}

export const SINCE_LABELS: Record<SinceKey, string> = {
  '1saat': 'son 1 saat',
  '24saat': 'son 24 saat',
  '7gun': 'son 7 gün',
}

export interface FilterChip {
  key: string
  label: string
  // Bu süzgeç kaldırılınca ortaya çıkan durum.
  without: DashboardFilters
}

// Açık süzgeçlerin her biri için tek tek kaldırılabilen bir etiket (çoklu seçimlerde seçenek başına bir tane).
export function activeChips(f: DashboardFilters, orgName: (id: string) => string): FilterChip[] {
  const chips: FilterChip[] = []
  for (const id of f.orgs) chips.push({ key: `org:${id}`, label: `Organizasyon: ${orgName(id)}`, without: { ...f, orgs: f.orgs.filter((o) => o !== id) } })
  if (f.status) chips.push({ key: 'status', label: `Durum: ${hostStatusLabel(f.status)}`, without: { ...f, status: '' } })
  if (f.mode) chips.push({ key: 'mode', label: `Mod: ${f.mode}`, without: { ...f, mode: '' } })
  if (f.query) chips.push({ key: 'query', label: `Ara: ${f.query}`, without: { ...f, query: '' } })
  if (f.withAlerts) chips.push({ key: 'withAlerts', label: "Yalnızca alert'i olanlar", without: { ...f, withAlerts: false } })
  if (f.agentUpdate) chips.push({ key: 'agentUpdate', label: "Yalnızca agent'ı güncellenmesi gerekenler", without: { ...f, agentUpdate: false } })
  for (const l of f.levels) chips.push({ key: `level:${l}`, label: `Seviye: ${alertLevelLabel(l)}`, without: { ...f, levels: f.levels.filter((x) => x !== l) } })
  for (const m of f.metrics) chips.push({ key: `metric:${m}`, label: `Metrik: ${alertMetricLabel(m)}`, without: { ...f, metrics: f.metrics.filter((x) => x !== m) } })
  if (f.since) chips.push({ key: 'since', label: `Alert zamanı: ${SINCE_LABELS[f.since]}`, without: { ...f, since: '' } })
  return chips
}
