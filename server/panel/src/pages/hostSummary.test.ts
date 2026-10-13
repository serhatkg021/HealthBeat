import assert from 'node:assert/strict'
import { test } from 'node:test'
import type { AlertType, HostThresholdsResponse, MetricPoint } from '../types/api.ts'
import { setDisplayTimeZone } from './datetime.ts'
import {
  ALERT_CARD,
  ALL_RULE_ITEMS,
  alertTally,
  capacityWarnings,
  freshness,
  openAlertsByCard,
  recipientSummary,
  ruleCounts,
  summaryCards,
  type SummaryCard,
  type SummaryCardId,
  type SummaryContext,
} from './hostSummary.ts'
import { itemKey } from './ruleTopics.ts'

const NOW = Date.parse('2026-10-13T12:00:00Z')

const point = (p: Partial<MetricPoint> = {}): MetricPoint => ({ timestamp: '2026-10-13T11:59:30Z', cpu_usage_pct: 35, ram_usage_pct: 40, disk: [], ...p })

const thresholds: HostThresholdsResponse = {
  thresholds: [
    { metric_type: 'cpu', default: { warning_level: 80, critical_level: 90 }, custom: null },
    { metric_type: 'ram', default: null, custom: null },
    { metric_type: 'disk', default: { warning_level: 80, critical_level: 90 }, custom: null },
  ],
  mount_thresholds: [],
  container_thresholds: [],
  subject_thresholds: [],
}

const ctx = (c: Partial<SummaryContext> = {}): SummaryContext => ({
  host: { agent_protocol: 4 },
  latest: point(),
  thresholds,
  containers: [],
  services: null,
  openAlerts: [],
  now: NOW,
  ...c,
})

const card = (cards: SummaryCard[], id: SummaryCardId): SummaryCard => cards.find((c) => c.id === id)!

test('cards follow the alert rule topics; every card links to the tab with the detail', () => {
  const cards = summaryCards(ctx())
  assert.deepEqual(
    cards.map((c) => c.id),
    ['cpu', 'bellek', 'disk', 'ag', 'sicaklik', 'servis', 'container', 'saat', 'bakim'],
  )
  assert.deepEqual(card(cards, 'cpu').link, { tab: 'performans', params: { konu: 'cpu' } })
  assert.deepEqual(card(cards, 'container').link, { tab: 'servisler', params: { tur: 'docker' } })
  assert.equal(card(cards, 'saat').link.tab, 'envanter')
  // Ağın alert kuralı yok; diğer kartlar konunun kurallarını taşır.
  assert.equal(card(cards, 'ag').rules, undefined)
  assert.deepEqual(card(cards, 'saat').rules?.items.map(itemKey), ['threshold:time_offset', 'status:time_unsynced', 'status:time_source'])
})

test('every alert type has a card except the offline alert (that one is the status tile)', () => {
  for (const [type, id] of Object.entries(ALERT_CARD)) {
    if (type === 'host_offline') assert.equal(id, null)
    else assert.ok(id, type)
  }
})

const facts = (c: SummaryCard) => c.facts.map((f) => (f.tone ? `${f.label}: ${f.value} (${f.tone})` : `${f.label}: ${f.value}`))

test('CPU: the value says what it is, the colour comes from the threshold and the chip names it', () => {
  const host = { agent_protocol: 4, cpu_cores: 8, host_info: { load_avg: [1.2, 0.8, 0.5] } } as SummaryContext['host']
  const ok = card(summaryCards(ctx({ host })), 'cpu')
  assert.equal(ok.value, '%35')
  assert.equal(ok.caption, 'kullanım')
  assert.equal(ok.tone, 'good')
  assert.equal(ok.status, 'normal')
  assert.deepEqual(facts(ok), ['Çekirdek: 8', 'Yük (1 · 5 · 15 dk): 1,20 · 0,80 · 0,50'])

  const hot = card(summaryCards(ctx({ latest: point({ cpu_usage_pct: 97 }) })), 'cpu')
  assert.equal(hot.tone, 'critical')
  assert.equal(hot.status, 'kritik')
})

test('without a report the cards say so instead of showing zeros', () => {
  const cards = summaryCards(ctx({ latest: null }))
  assert.equal(card(cards, 'cpu').value, '—')
  assert.equal(card(cards, 'cpu').status, 'bilgi yok')
  assert.equal(card(cards, 'cpu').note, 'Henüz rapor gelmedi.')
  assert.equal(card(cards, 'disk').value, '—')
})

test('memory adds used/total, swap and a recent OOM kill', () => {
  const host = {
    agent_protocol: 4,
    ram_total_mb: 16384,
    host_info: { swap: { total_mb: 2048, used_mb: 0 } },
    system_state: { oom_last_increase_at: '2026-10-13T11:00:00Z' },
  } as SummaryContext['host']
  const c = card(summaryCards(ctx({ host })), 'bellek')
  assert.equal(c.value, '%40')
  assert.deepEqual(facts(c).slice(0, 2), ['Kullanılan: 6.4 / 16 GB', 'Swap: 0 / 2 GB'])
  assert.match(facts(c)[2], /^Bellek yetmedi \(OOM\): .* önce \(warning\)$/)
  assert.equal(c.tone, 'warning', 'no RAM threshold, but the OOM kill still warns')
  assert.equal(c.status, 'OOM')
})

test('disk shows the fullest mount; read-only and broken RAID outrank the usage', () => {
  const disk = [
    { mount: '/', used_pct: 45, total: 1, free: 1 },
    { mount: '/var', used_pct: 82, total: 1, free: 1 },
  ]
  const warn = card(summaryCards(ctx({ latest: point({ disk }) })), 'disk')
  assert.equal(warn.value, '%82')
  assert.equal(warn.caption, 'en dolu mount · /var')
  assert.equal(warn.tone, 'warning')
  assert.equal(warn.status, 'uyarı')
  assert.deepEqual(facts(warn), ['Mount: 2'])

  const host = { agent_protocol: 4, system_state: { raid: [{ name: 'md0', level: 'raid1', state: 'degraded', devices: 2, active: 1 }] } } as SummaryContext['host']
  const ro = card(summaryCards(ctx({ host, latest: point({ disk: [{ ...disk[0], read_only: true }] }) })), 'disk')
  assert.equal(ro.tone, 'critical')
  assert.equal(ro.status, 'salt okunur')
  assert.deepEqual(facts(ro), ['Mount: 1', 'Salt okunur: / (critical)', 'RAID: md0 bozuk (critical)'])
})

test('cards an old agent cannot fill keep their place and say why', () => {
  const cards = summaryCards(ctx({ host: { agent_protocol: 2 } }))
  for (const id of ['ag', 'sicaklik', 'saat', 'bakim'] as const) {
    assert.equal(card(cards, id).value, '—', id)
    assert.equal(card(cards, id).status, 'bilgi yok', id)
    assert.match(card(cards, id).note ?? '', /agent güncellenince görünür/, id)
  }
})

test('services: the value is the number of problems', () => {
  const svc = (name: string, active: string, watched: boolean) => ({ name, active, updated_at: '', watched })
  const fine = card(summaryCards(ctx({ services: { services: [svc('nginx', 'active', true), svc('cron', 'active', false)], watched: ['nginx'] } })), 'servis')
  assert.equal(fine.value, '0')
  assert.equal(fine.caption, 'sorunlu servis')
  assert.equal(fine.tone, 'good')
  assert.deepEqual(facts(fine), ['İzlenen: 1', 'Toplam servis: 2'])

  const bad = card(summaryCards(ctx({ services: { services: [svc('nginx', 'failed', true)], watched: ['nginx', 'redis'] } })), 'servis')
  assert.equal(bad.value, '1')
  assert.equal(bad.tone, 'critical')
  assert.equal(bad.status, 'çalışmıyor')
  assert.deepEqual(facts(bad), ['İzlenen: 2', 'Toplam servis: 1', 'Raporda olmayan: 1 (warning)'])
})

test('containers: unhealthy ones are counted, stopped ones are information, no Docker is neutral', () => {
  const c = (name: string, status: string, extra = {}) => ({ name, image: 'x', status, cpu_pct: 0, ram_mb: 0, restart_count: 0, uptime_seconds: 0, ...extra })
  assert.equal(card(summaryCards(ctx()), 'container').tone, 'neutral')
  const ok = card(summaryCards(ctx({ containers: [c('a', 'running'), c('b', 'exited')] })), 'container')
  assert.equal(ok.value, '1/2')
  assert.equal(ok.caption, 'çalışıyor')
  assert.equal(ok.tone, 'good')
  assert.deepEqual(facts(ok), ['Durmuş: 1'])
  const sick = card(summaryCards(ctx({ containers: [c('a', 'running', { health: 'unhealthy' })] })), 'container')
  assert.equal(sick.status, 'sağlıksız')
  assert.equal(sick.tone, 'critical')
  assert.deepEqual(facts(sick), ['Sağlıksız: 1 (critical)', 'Durmuş: 0'])
})

test('clock: unsynchronized is critical, other NTP issues warn', () => {
  const host = (time_sync: object) => ({ agent_protocol: 4, system_state: { time_sync } }) as SummaryContext['host']
  const ok = card(summaryCards(ctx({ host: host({ synchronized: true, offset_ms: 3, daemon: 'chrony', stratum: 2 }) })), 'saat')
  assert.equal(ok.value, 'senkron')
  assert.deepEqual(facts(ok), ['Fark: 3,000 ms', 'Servis: chrony', 'Stratum: 2'])
  assert.equal(ok.tone, 'good')
  const off = card(summaryCards(ctx({ host: host({ synchronized: false }) })), 'saat')
  assert.equal(off.value, 'senkron değil')
  assert.equal(off.tone, 'critical')
})

test('upkeep: pending and security updates, reboot, uptime', () => {
  const host = { agent_protocol: 4, host_info: { reboot_required: true, uptime_seconds: 3 * 86400 }, system_state: { updates: { pending: 12, security: 3 } } } as SummaryContext['host']
  const c = card(summaryCards(ctx({ host })), 'bakim')
  assert.equal(c.value, '12')
  assert.equal(c.caption, 'bekleyen güncelleme')
  assert.equal(c.tone, 'warning')
  assert.equal(c.status, 'güvenlik')
  assert.deepEqual(facts(c), ['Güvenlik güncellemesi: 3 (warning)', 'Yeniden başlatma: gerekli (warning)', 'Çalışma süresi: 3 gün'])
  const clean = card(summaryCards(ctx({ host: { agent_protocol: 4, host_info: { reboot_required: false }, system_state: { updates: { pending: 0, security: 0 } } } as SummaryContext['host'] })), 'bakim')
  assert.equal(clean.value, '0')
  assert.equal(clean.tone, 'good')
  assert.equal(clean.status, 'normal')
})

test('open alerts mark their card and raise its colour to the alert level', () => {
  const alerts: { alert_type: AlertType; level: 'warning' | 'critical' }[] = [
    { alert_type: 'service_failed', level: 'warning' },
    { alert_type: 'service_failed', level: 'critical' },
    { alert_type: 'host_offline', level: 'critical' },
  ]
  assert.deepEqual(openAlertsByCard(alerts), { servis: { count: 2, level: 'critical' } })
  const c = card(summaryCards(ctx({ openAlerts: alerts })), 'servis')
  assert.deepEqual(c.open, { count: 2, level: 'critical' })
  assert.equal(c.tone, 'critical')
  assert.equal(c.status, 'kritik')
})

test('rule counts: on, total and own', () => {
  assert.deepEqual(ruleCounts([{ on: true, source: 'Bu sunucu' }, { on: true, source: 'Devralındı' }, { on: false, source: '' }]), { on: 2, total: 3, custom: 1 })
  assert.ok(ALL_RULE_ITEMS.length > 10)
  assert.equal(new Set(ALL_RULE_ITEMS.map(itemKey)).size, ALL_RULE_ITEMS.length)
})

test('recipients are counted per channel, system summary included, skipped ones not', () => {
  const r = (channel: string) => ({ name: 'x', channel, address: 'a', source: { kind: 'system' as const } })
  assert.deepEqual(
    recipientSummary({ recipients: [r('email'), r('email')], system_summary: [{ channel: 'email', count: 2 }, { channel: 'sms', count: 1, reason: 'channel_off' }], in_maintenance: false }),
    { total: 4, channels: 'E-posta 4' },
  )
  assert.deepEqual(recipientSummary({ recipients: [], system_summary: null, in_maintenance: false }), { total: 0, channels: '' })
})

test('freshness: online shows the report time, offline says the values are old', () => {
  setDisplayTimeZone('UTC')
  assert.equal(freshness('online', null, NOW), null)
  assert.deepEqual(freshness('online', { timestamp: '2026-10-13T11:59:30Z' }, NOW), { stale: false, text: 'Son rapor · 13.10.2026 11:59:30' })
  const old = freshness('offline', { timestamp: '2026-10-11T12:00:00Z' }, NOW)
  assert.equal(old?.stale, true)
  assert.match(old!.text, /^Son değerler · 2 gün önce · /)
  setDisplayTimeZone()
})

test('kernel limits appear only when close to full', () => {
  assert.deepEqual(capacityWarnings(undefined), [])
  assert.deepEqual(capacityWarnings({ capacity: { file_handles: 10, file_handles_max: 100, conntrack: 96, conntrack_max: 100 } }), [
    { tone: 'critical', text: 'Bağlantı izleme (conntrack) sınırın %96 kadarında' },
  ])
})

test('a coloured card always names its state in the chip', () => {
  const svc = (name: string, active: string, watched: boolean) => ({ name, active, updated_at: '', watched })
  const host = { agent_protocol: 4, system_state: { time_sync: { synchronized: true, enabled: false } } } as SummaryContext['host']
  const cards = summaryCards(ctx({ host, latest: point({ cpu_usage_pct: 85 }), services: { services: [svc('a', 'active', true)], watched: ['a', 'b'] } }))
  for (const c of cards) if (c.tone === 'warning' || c.tone === 'critical') assert.ok(!['normal', 'bilgi yok'].includes(c.status), c.id)
  assert.equal(card(cards, 'servis').status, 'raporda yok')
  assert.equal(card(cards, 'saat').status, 'sorun')
})

test('alert tally: count per level and the age of the oldest', () => {
  assert.deepEqual(alertTally([], NOW), { critical: 0, warning: 0, info: 0 })
  const t = alertTally(
    [
      { level: 'critical', created_at: '2026-10-13T11:00:00Z' },
      { level: 'critical', created_at: '2026-10-12T12:00:00Z' },
      { level: 'warning', created_at: '2026-10-13T11:30:00Z' },
    ],
    NOW,
  )
  assert.equal(t.critical, 2)
  assert.equal(t.warning, 1)
  assert.equal(t.info, 0)
  assert.match(t.oldest ?? '', /^1 gün.* önce$/)
})
