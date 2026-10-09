import assert from 'node:assert/strict'
import { test } from 'node:test'
import type { MetricPoint, SystemState } from '../types/api.ts'
import { ALERT_TOPIC, PERF_TOPICS, chartLayout, openAlertsByTopic, perfRuleItems, perfTiles, resolvePerfTopic, ruleLine, topicNow } from './perfTopics.ts'
import { hostRow, hostRuleState } from './hostRuleRows.ts'
import { STATUS_RULES } from './statusRules.ts'
import { METRIC_TYPES } from './thresholds.ts'
import { TOPICS, itemKey, topicInfo, topicItems } from './ruleTopics.ts'

test('the topic comes from the address; anything unknown is the overview', () => {
  assert.equal(resolvePerfTopic('disk'), 'disk')
  assert.equal(resolvePerfTopic(' ag '), 'ag')
  assert.equal(resolvePerfTopic(null), 'ozet')
  assert.equal(resolvePerfTopic('yok'), 'ozet')
  assert.equal(new Set(PERF_TOPICS.map((t) => t.id)).size, PERF_TOPICS.length)
})

test('every alert rule a topic names exists in the alert rules catalog', () => {
  for (const t of PERF_TOPICS) {
    if (!t.rules) continue
    assert.ok(TOPICS.some((x) => x.id === t.rules?.topic), t.id)
    const known = topicItems(topicInfo(t.rules.topic), 'sunucu').map(itemKey)
    for (const k of t.rules.keys ?? []) assert.ok(known.includes(k), `${t.id}: ${k}`)
  }
})

test('topics show only their own alert rules; CPU and memory split the shared alert topic', () => {
  assert.deepEqual(perfRuleItems('cpu').map(itemKey), ['threshold:cpu'])
  assert.deepEqual(perfRuleItems('bellek').map(itemKey), ['threshold:ram', 'status:oom_kill'])
  // Disk konusunun bütün eşik ve durum kuralları; seçim ve otomatik alert'ler kural değildir.
  assert.deepEqual(perfRuleItems('disk').map(itemKey), [
    'threshold:disk',
    'threshold:disk_latency',
    'status:fs_readonly',
    'status:raid_degraded',
    'status:raid_rebuilding',
  ])
  assert.deepEqual(perfRuleItems('sicaklik').map(itemKey), ['threshold:temperature'])
  assert.deepEqual(perfRuleItems('ag'), [])
  assert.deepEqual(perfRuleItems('ozet'), [])
})

test('at most two blocks side by side; a wide block or one left alone in its row takes the full width', () => {
  const n = { wide: false }
  const w = { wide: true }
  assert.deepEqual(chartLayout([n, n, n, n]), [false, false, false, false])
  assert.deepEqual(chartLayout([w, n, n]), [true, false, false])
  assert.deepEqual(chartLayout([n, n, n]), [false, false, true])
  assert.deepEqual(chartLayout([n, w, n]), [true, true, true])
  assert.deepEqual(chartLayout([n, n, w, n, n]), [false, false, true, false, false])
  assert.deepEqual(chartLayout([n]), [true])
  assert.deepEqual(chartLayout([]), [])
})

const point = (patch: Partial<MetricPoint> = {}): MetricPoint => ({ timestamp: '', cpu_usage_pct: 23.4, ram_usage_pct: 41.7, disk: [], ...patch })

test('the menu shows the latest value per topic, or nothing when unknown', () => {
  const latest = point({
    disk: [
      { mount: '/', used_pct: 17.8, total: 1, free: 1 },
      { mount: '/boot', used_pct: 64.2, total: 1, free: 1 },
    ],
    net_io: [
      { interface: 'enp3s0', rx_bps: 2_400_000, tx_bps: 800_000, rx_errors: 0, tx_errors: 0, rx_drops: 0, tx_drops: 0 },
      { interface: 'docker0', rx_bps: 100_000, tx_bps: 0, rx_errors: 0, tx_errors: 0, rx_drops: 0, tx_drops: 0 },
    ],
  })
  const state: SystemState = {
    temperatures: [
      { sensor: 'a', celsius: 41.9 },
      { sensor: 'b', celsius: 52 },
    ],
    capacity: { file_handles: 18_432, file_handles_max: 1_048_576, conntrack: 1024, conntrack_max: 262_144 },
  }
  assert.equal(topicNow('cpu', latest, state), '%23')
  assert.equal(topicNow('bellek', latest, state), '%42')
  assert.equal(topicNow('disk', latest, state), '%64')
  assert.equal(topicNow('ag', latest, state), '3,3 Mbit/sn')
  assert.equal(topicNow('sicaklik', latest, state), '52,0 °C')
  assert.equal(topicNow('sinirlar', latest, state), '%2')
  assert.equal(topicNow('ozet', latest, state), '')

  for (const id of ['cpu', 'bellek', 'disk', 'ag', 'sicaklik', 'sinirlar'] as const) assert.equal(topicNow(id, null, undefined), '', id)
})

const NOW = Date.parse('2026-10-10T12:00:00Z')
const values = (tiles: { value: string }[]) => tiles.map((t) => t.value)

test('CPU and memory tiles: usage with its total, load, swap, OOM and pressure', () => {
  const latest = point({
    system: { cpu_detail: { iowait_pct: 1.24 }, pressure: { cpu: { some10: 0, some60: 0.3 }, memory: { some10: 0, some60: 0 } } },
  })
  const ctx = { latest, cores: 8, ramTotalMB: 16_000, info: { load_avg: [0.82, 0.74, 0.69], swap: { total_mb: 2048, used_mb: 0 } }, now: NOW }
  const cpu = perfTiles('cpu', ctx)
  assert.deepEqual(values(cpu), ['%23,4', '0,82 · 0,74 · 0,69', '%1,2', '%0,3'])
  assert.equal(cpu[0].hint, '8 çekirdek')
  const mem = perfTiles('bellek', { ...ctx, state: { oom_kills: 2, oom_last_increase_at: '2026-10-10T11:48:00Z' } })
  assert.deepEqual(values(mem), ['%41,7', '0', '2', '%0,0'])
  assert.equal(mem[0].hint, '6.5 GB / 15.6 GB')
  assert.equal(mem[1].hint, '2 GB içinden')
  assert.deepEqual([mem[2].hint, mem[2].tone], ['son: 12 dk önce', 'warning'])
})

test('disk and network tiles follow the chosen disk and interface; RAID shows the worst array', () => {
  const latest = point({
    disk: [
      { mount: '/', used_pct: 17.8, total: 1, free: 1 },
      { mount: '/srv', used_pct: 64.2, total: 1, free: 1 },
    ],
    disk_io: [{ name: 'nvme0n1', read_iops: 1, write_iops: 1, read_bps: 1024, write_bps: 2048, util_pct: 1, await_ms: 0.37, queue_depth: 0 }],
    net_io: [{ interface: 'enp3s0', rx_bps: 2_400_000, tx_bps: 800_000, rx_errors: 0, tx_errors: 0, rx_drops: 0, tx_drops: 0 }],
    system: { tcp: { established: 142, time_wait: 37, retrans_pct: 0.012 } },
  })
  const disk = perfTiles('disk', { latest, state: { raid: [{ name: 'md0', state: 'clean', devices: 2, active: 2 }, { name: 'md1', state: 'degraded', devices: 2, active: 1 }] }, disk: 'nvme0n1', now: NOW })
  assert.deepEqual(values(disk), ['%64,2', '0,37 ms', '3,0 KB/sn', '2 dizi'])
  assert.deepEqual([disk[3].hint, disk[3].tone], ['bozuk', 'critical'])
  assert.deepEqual(values(perfTiles('disk', { latest, state: { raid: [] }, disk: 'sda', now: NOW })).slice(1), ['—', '—', 'yok'])
  // Agent dizi yokken alanı göndermez; son rapor hiç yoksa bilinmiyor.
  assert.equal(perfTiles('disk', { latest, state: {}, now: NOW })[3].value, 'yok')
  assert.equal(perfTiles('disk', { latest, now: NOW })[3].value, '—')

  const net = perfTiles('ag', { latest, iface: 'enp3s0', now: NOW })
  assert.deepEqual(values(net), ['2,4 Mbit/sn', '800 Kbit/sn', '142', '%0,01'])
  assert.equal(net[2].hint, 'TIME_WAIT 37')
})

test('an old agent or a missing report shows dashes instead of breaking', () => {
  for (const id of ['cpu', 'bellek', 'disk', 'ag'] as const) {
    const tiles = perfTiles(id, { latest: null, now: NOW })
    assert.ok(tiles.length > 0, id)
    assert.ok(tiles.every((t) => t.value === '—'), id)
  }
  assert.deepEqual(perfTiles('ozet', { latest: null, now: NOW }), [])
})

test('rule lines say name, value, duration and source; subject values are counted; undefined ones only their name', () => {
  const state = hostRuleState(
    {
      thresholds: METRIC_TYPES.map((m) =>
        m === 'ram'
          ? { metric_type: m, default: { warning_level: 80, critical_level: 95 }, custom: null }
          : m === 'disk_latency'
            ? { metric_type: m, default: null, custom: { warning_level: 30, critical_level: 50, duration_seconds: 600 } }
            : { metric_type: m, default: null, custom: null },
      ),
      mount_thresholds: [],
      container_thresholds: [],
      subject_thresholds: [{ metric_type: 'disk_latency', subject: 'sda', custom: { warning_level: 60, critical_level: 90 } }],
    },
    STATUS_RULES.map((r) => ({ rule: r.rule, takes_duration: !!r.duration, default: r.rule === 'oom_kill' ? { level: 'warning' as const } : null, custom: null })),
    null,
    null,
  )
  assert.deepEqual(ruleLine(hostRow({ kind: 'threshold', metric: 'ram' }, state), 'E2E Org'), {
    name: 'RAM',
    value: 'uyarı %80 · kritik %95',
    duration: 'anlık',
    source: 'Devralındı · E2E Org',
    extra: '',
    on: true,
    tone: 'accent',
  })
  assert.deepEqual(ruleLine(hostRow({ kind: 'threshold', metric: 'disk_latency' }, state)), {
    name: 'Disk gecikmesi',
    value: 'uyarı 30 ms · kritik 50 ms',
    duration: '10 dk boyunca',
    source: 'Bu sunucu',
    extra: '+1 diske özel',
    on: true,
    tone: 'accent',
  })
  const oom = ruleLine(hostRow({ kind: 'status', rule: 'oom_kill' }, state))
  assert.deepEqual([oom.value, oom.source, oom.tone], ['Uyarı', 'Devralındı', 'warning'])
  const off = ruleLine(hostRow({ kind: 'status', rule: 'fs_readonly' }, state))
  assert.deepEqual([off.name, off.on, off.value, off.source], ['Dosya sistemi salt okunur', false, '', ''])
})

test('open alerts are counted per topic with the worst level; other alert types stay out', () => {
  assert.deepEqual(
    openAlertsByTopic([
      { alert_type: 'disk', level: 'warning' },
      { alert_type: 'raid_degraded', level: 'critical' },
      { alert_type: 'ram', level: 'warning' },
      { alert_type: 'service_failed', level: 'critical' },
      { alert_type: 'host_offline', level: 'critical' },
    ]),
    { disk: { count: 2, level: 'critical' }, bellek: { count: 1, level: 'warning' } },
  )
  // Bir konuya eşlenen her alert türü o konunun kurallarından gelir (yanlış konuya nokta düşmez).
  for (const [type, topic] of Object.entries(ALERT_TOPIC)) if (topic) assert.ok(PERF_TOPICS.some((t) => t.id === topic), type)
})
