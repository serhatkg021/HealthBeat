import assert from 'node:assert/strict'
import { test } from 'node:test'
import type { Alert, OverviewHost } from '../types/api.ts'
import {
  activeChips,
  activeCount,
  alertFiltersActive,
  applyFilters,
  emptyFilters,
  parseFilters,
  pruneOrgs,
  writeFilters,
  type DashboardFilters,
} from './dashboardFilters.ts'
import type { AgentPolicy } from './agentStatus.ts'

const NOW = new Date('2026-09-19T12:00:00Z')
const ago = (ms: number) => new Date(NOW.getTime() - ms).toISOString()
const MIN = 60 * 1000
const HOUR = 60 * MIN
const DAY = 24 * HOUR

const host = (id: string, over: Partial<OverviewHost> = {}): OverviewHost => ({
  id,
  organization_id: 'org-a',
  title: id,
  ip: '10.0.0.1',
  mode: 'push',
  status: 'online',
  ...over,
})

let n = 0
const alert = (host_id: string, over: Partial<Alert> = {}): Alert => ({
  id: `al-${++n}`,
  host_id,
  alert_type: 'cpu',
  level: 'warning',
  status: 'open',
  created_at: ago(10 * MIN),
  ...over,
})

const filters = (over: Partial<DashboardFilters>): DashboardFilters => ({ ...emptyFilters(), ...over })
const ids = (rows: { host: OverviewHost }[]) => rows.map((r) => r.host.id)

const DATA = {
  hosts: [
    host('web-1', { organization_id: 'org-a', ip: '10.0.0.11' }),
    host('web-2', { organization_id: 'org-a', status: 'offline', mode: 'pull', ip: '10.0.0.12' }),
    host('db-1', { organization_id: 'org-b', ip: '192.168.1.5' }),
    host('batch-1', { organization_id: 'org-b', mode: 'pull' }),
  ],
  alerts: [
    alert('web-1', { alert_type: 'cpu', level: 'critical', created_at: ago(5 * MIN) }),
    alert('web-1', { alert_type: 'disk', level: 'warning', created_at: ago(3 * HOUR) }),
    alert('web-2', { alert_type: 'host_offline', level: 'critical', created_at: ago(2 * DAY) }),
    alert('db-1', { alert_type: 'ram', level: 'warning', created_at: ago(30 * MIN) }),
  ],
}

test('no filters shows every server and every alert, with matching counts', () => {
  const v = applyFilters(DATA, emptyFilters(), NOW)
  assert.equal(v.rows.length, 4)
  assert.equal(v.alerts.length, 4)
  assert.deepEqual(v.counts, { total: 4, online: 3, offline: 1, alerts: 4, critical: 2, warning: 2 })
})

test('servers with more critical alerts come first, then warnings, then offline, then by name', () => {
  const v = applyFilters(DATA, emptyFilters(), NOW)
  assert.deepEqual(ids(v.rows), ['web-1', 'web-2', 'db-1', 'batch-1'])
  assert.deepEqual([v.rows[0].critical, v.rows[0].warning], [1, 1])

  const quiet = applyFilters(
    { hosts: [host('b'), host('a'), host('z', { status: 'offline' })], alerts: [] },
    emptyFilters(),
    NOW,
  )
  assert.deepEqual(ids(quiet.rows), ['z', 'a', 'b'])
})

test('organization filter accepts several organizations and hides the others’ alerts', () => {
  assert.deepEqual(ids(applyFilters(DATA, filters({ orgs: ['org-b'] }), NOW).rows), ['db-1', 'batch-1'])
  assert.deepEqual(ids(applyFilters(DATA, filters({ orgs: ['org-a', 'org-b'] }), NOW).rows).length, 4)
  const v = applyFilters(DATA, filters({ orgs: ['org-b'] }), NOW)
  assert.deepEqual(v.counts, { total: 2, online: 2, offline: 0, alerts: 1, critical: 0, warning: 1 })
})

test('status and mode filter servers', () => {
  assert.deepEqual(ids(applyFilters(DATA, filters({ status: 'offline' }), NOW).rows), ['web-2'])
  assert.deepEqual(ids(applyFilters(DATA, filters({ status: 'online' }), NOW).rows).sort(), ['batch-1', 'db-1', 'web-1'])
  assert.deepEqual(ids(applyFilters(DATA, filters({ mode: 'pull' }), NOW).rows).sort(), ['batch-1', 'web-2'])
  assert.deepEqual(ids(applyFilters(DATA, filters({ mode: 'pull', status: 'online' }), NOW).rows), ['batch-1'])
})

test('search matches host name or IP, ignoring case', () => {
  assert.deepEqual(ids(applyFilters(DATA, filters({ query: 'WEB' }), NOW).rows), ['web-1', 'web-2'])
  assert.deepEqual(ids(applyFilters(DATA, filters({ query: '192.168' }), NOW).rows), ['db-1'])
  assert.deepEqual(ids(applyFilters(DATA, filters({ query: 'nothing' }), NOW).rows), [])
  const mixed = { hosts: [host('Web-Prod', { ip: 'FE80::1' })], alerts: [] }
  assert.equal(applyFilters(mixed, filters({ query: 'web' }), NOW).rows.length, 1)
  assert.equal(applyFilters(mixed, filters({ query: 'fe80' }), NOW).rows.length, 1)
})

test('an alert filter also drops servers that have no matching alert', () => {
  const critical = applyFilters(DATA, filters({ levels: ['critical'] }), NOW)
  assert.deepEqual(ids(critical.rows), ['web-2', 'web-1']) // eşitlikte çevrimdışı olan önce
  assert.deepEqual(critical.counts, { total: 2, online: 1, offline: 1, alerts: 2, critical: 2, warning: 0 })
  // web-1'in uyarı seviyesindeki disk alert'i de sayılmaz.
  assert.deepEqual([critical.rows[1].critical, critical.rows[1].warning], [1, 0])

  const disk = applyFilters(DATA, filters({ metrics: ['disk'] }), NOW)
  assert.deepEqual(ids(disk.rows), ['web-1'])
  assert.equal(disk.alerts[0].alert_type, 'disk')

  const either = applyFilters(DATA, filters({ metrics: ['ram', 'host_offline'] }), NOW)
  assert.deepEqual(ids(either.rows).sort(), ['db-1', 'web-2'])
})

test('"only servers with alerts" keeps every alert of those servers', () => {
  const v = applyFilters(DATA, filters({ withAlerts: true }), NOW)
  assert.deepEqual(ids(v.rows), ['web-1', 'web-2', 'db-1'])
  assert.equal(v.counts.alerts, 4)
})

test('time window keeps alerts created at or after the cut-off', () => {
  assert.deepEqual(ids(applyFilters(DATA, filters({ since: '1saat' }), NOW).rows), ['web-1', 'db-1'])
  assert.deepEqual(ids(applyFilters(DATA, filters({ since: '24saat' }), NOW).rows), ['web-1', 'db-1'])
  assert.deepEqual(ids(applyFilters(DATA, filters({ since: '7gun' }), NOW).rows), ['web-1', 'web-2', 'db-1'])

  const edge = { hosts: [host('x')], alerts: [alert('x', { created_at: ago(HOUR) })] }
  assert.equal(applyFilters(edge, filters({ since: '1saat' }), NOW).rows.length, 1) // tam sınır dahil
  const past = { hosts: [host('x')], alerts: [alert('x', { created_at: ago(HOUR + 1) })] }
  assert.equal(applyFilters(past, filters({ since: '1saat' }), NOW).rows.length, 0)
})

test('filters combine: server and alert filters must both hold', () => {
  const v = applyFilters(DATA, filters({ orgs: ['org-a'], levels: ['critical'], status: 'online' }), NOW)
  assert.deepEqual(ids(v.rows), ['web-1'])
  assert.equal(applyFilters(DATA, filters({ orgs: ['org-b'], levels: ['critical'] }), NOW).rows.length, 0)
})

test('the alert list is newest first and only holds visible servers’ alerts', () => {
  const v = applyFilters(DATA, emptyFilters(), NOW)
  const times = v.alerts.map((a) => Date.parse(a.created_at))
  assert.deepEqual(times, [...times].sort((a, b) => b - a))
  const hidden = applyFilters(DATA, filters({ status: 'online' }), NOW)
  assert.ok(hidden.alerts.every((a) => a.host_id !== 'web-2'))
})

test('an alert of a server that is not in the data is ignored', () => {
  const v = applyFilters({ hosts: [host('a')], alerts: [alert('ghost')] }, emptyFilters(), NOW)
  assert.equal(v.counts.alerts, 0)
})

test('filters survive a round trip through the address bar, and keep unrelated parameters', () => {
  const f = filters({
    orgs: ['o1', 'o2'],
    status: 'offline',
    mode: 'pull',
    query: 'web',
    withAlerts: true,
    levels: ['critical', 'warning'],
    metrics: ['disk', 'docker_restart'],
    since: '24saat',
  })
  const url = writeFilters(new URLSearchParams('sekme=x'), f)
  assert.equal(url.get('sekme'), 'x')
  assert.deepEqual(parseFilters(url), f)
  assert.deepEqual(parseFilters(new URLSearchParams('')), emptyFilters())
  // Boş süzgeç adreste hiçbir şey bırakmaz; eskisini de siler.
  assert.equal(writeFilters(url, emptyFilters()).toString(), 'sekme=x')
})

test('a search typed with spaces is written to the address trimmed, and nothing is written for blank input', () => {
  assert.equal(writeFilters(new URLSearchParams(), filters({ query: '  web ' })).get('durum'), null)
  assert.equal(writeFilters(new URLSearchParams(), filters({ query: '  web ' })).get('q'), 'web')
  assert.equal(writeFilters(new URLSearchParams(), filters({ query: '   ' })).toString(), '')
})

test('unknown or malformed values in the address are dropped instead of breaking the page', () => {
  const p = new URLSearchParams(
    'durum=maybe&mod=udp&seviye=fatal&seviye=critical&metrik=gpu&metrik=cpu&metrik=cpu&zaman=1yil&alertli=yes&org=&org=o1&org=o1&q=%20%20web%20',
  )
  assert.deepEqual(parseFilters(p), filters({ levels: ['critical'], metrics: ['cpu'], orgs: ['o1'], query: 'web' }))
})

test('unknown organizations are pruned from the filter', () => {
  const f = filters({ orgs: ['o1', 'gone'] })
  assert.deepEqual(pruneOrgs(f, ['o1', 'o2']).orgs, ['o1'])
  assert.equal(pruneOrgs(f, ['o1', 'gone']), f) // değişiklik yoksa aynı nesne
})

test('active filter groups are counted once each', () => {
  assert.equal(activeCount(emptyFilters()), 0)
  assert.equal(activeCount(filters({ orgs: ['a', 'b'], levels: ['critical', 'warning'] })), 2)
  assert.equal(
    activeCount(filters({ orgs: ['a'], status: 'online', mode: 'push', query: 'x', withAlerts: true, levels: ['critical'], metrics: ['cpu'], since: '1saat' })),
    8,
  )
  assert.equal(alertFiltersActive(filters({ withAlerts: true, status: 'online' })), false)
  assert.equal(alertFiltersActive(filters({ since: '7gun' })), true)
})

test('every open filter has its own chip, and removing a chip removes only that filter', () => {
  const f = filters({ orgs: ['o1', 'o2'], status: 'offline', mode: 'pull', query: 'web', withAlerts: true, levels: ['critical'], metrics: ['disk', 'cpu'], since: '7gun' })
  const chips = activeChips(f, (id) => `Org ${id}`)
  assert.deepEqual(
    chips.map((c) => c.label),
    [
      'Organizasyon: Org o1',
      'Organizasyon: Org o2',
      'Durum: çevrimdışı',
      'Mod: pull',
      'Ara: web',
      "Yalnızca alert'i olanlar",
      'Seviye: kritik',
      'Metrik: Disk',
      'Metrik: CPU',
      'Alert zamanı: son 7 gün',
    ],
  )
  assert.equal(new Set(chips.map((c) => c.key)).size, chips.length)
  const byKey = (k: string) => chips.find((c) => c.key === k)!.without
  assert.deepEqual(byKey('org:o1'), { ...f, orgs: ['o2'] })
  assert.deepEqual(byKey('metric:cpu'), { ...f, metrics: ['disk'] })
  assert.deepEqual(byKey('level:critical'), { ...f, levels: [] })
  assert.deepEqual(byKey('status'), { ...f, status: '' })
  assert.deepEqual(byKey('mode'), { ...f, mode: '' })
  assert.deepEqual(byKey('query'), { ...f, query: '' })
  assert.deepEqual(byKey('withAlerts'), { ...f, withAlerts: false })
  assert.deepEqual(byKey('since'), { ...f, since: '' })
  assert.deepEqual(activeChips(emptyFilters(), String), [])
  // Her etiket bir grubu sayar: çip sayısı ≥ süzgeç grubu sayısı.
  assert.ok(chips.length >= activeCount(f))
})

// --- Agent sürümü süzgeci ---------------------------------------------------------------------

const POLICY: AgentPolicy = { latest: '1.5.0', min: '1.2.0' }
const AGENT_DATA = {
  hosts: [
    host('legacy-1', { agent_protocol: 1 }), // sürüm bildirmiyor
    host('old-1', { agent_version: '1.4.0', agent_protocol: 2 }), // güncelleme var
    host('dead-1', { agent_version: '1.0.0', agent_protocol: 2 }), // desteklenmiyor
    host('fresh-1', { agent_version: '1.5.0', agent_protocol: 2 }), // güncel
    host('new-1', {}), // henüz rapor yok: bilinmiyor
  ],
  alerts: [],
}

test('the agent filter keeps only agents that need an update (legacy, outdated, unsupported)', () => {
  const v = applyFilters(AGENT_DATA, filters({ agentUpdate: true }), NOW, POLICY)
  assert.deepEqual(ids(v.rows).sort(), ['dead-1', 'legacy-1', 'old-1'])
  // Süzgeç kapalıyken hepsi görünür.
  assert.equal(applyFilters(AGENT_DATA, emptyFilters(), NOW, POLICY).rows.length, 5)
})

test('without a policy only legacy agents need an update', () => {
  const v = applyFilters(AGENT_DATA, filters({ agentUpdate: true }), NOW)
  assert.deepEqual(ids(v.rows), ['legacy-1'])
})

test('the agent filter combines with the other server filters', () => {
  const data = { hosts: [host('a', { agent_protocol: 1, status: 'offline' }), host('b', { agent_protocol: 1 })], alerts: [] }
  assert.deepEqual(ids(applyFilters(data, filters({ agentUpdate: true, status: 'offline' }), NOW, POLICY).rows), ['a'])
})

test('the agent filter round-trips through the address bar, counts as a group, and has its own chip', () => {
  const f = filters({ agentUpdate: true })
  const url = writeFilters(new URLSearchParams(), f)
  assert.equal(url.get('agentguncelle'), '1')
  assert.deepEqual(parseFilters(url), f)
  assert.deepEqual(parseFilters(new URLSearchParams('agentguncelle=evet')), emptyFilters()) // bozuk değer atılır
  assert.equal(activeCount(f), 1)
  const chips = activeChips(f, String)
  assert.deepEqual(chips.map((c) => c.label), ["Yalnızca agent'ı güncellenmesi gerekenler"])
  assert.deepEqual(chips[0].without, emptyFilters())
  assert.equal(writeFilters(url, emptyFilters()).toString(), '')
})
