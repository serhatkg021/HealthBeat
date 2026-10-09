import assert from 'node:assert/strict'
import { test } from 'node:test'
import type { MetricPoint, SystemState } from '../types/api.ts'
import { PERF_TOPICS, chartLayout, perfRuleItems, resolvePerfTopic, topicNow } from './perfTopics.ts'
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
