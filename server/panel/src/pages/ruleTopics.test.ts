import assert from 'node:assert/strict'
import { test } from 'node:test'
import { STATUS_RULES } from './statusRules.ts'
import { METRIC_TYPES } from './thresholds.ts'
import { TOPICS, itemKey, resolveTopic, ruleCount, topicHasChanges, topicInfo, topicItems, type TopicItem } from './ruleTopics.ts'

const allItems = TOPICS.flatMap((x) => x.items)
const keys = (items: TopicItem[]) => items.map(itemKey)

test('every threshold type and status rule belongs to exactly one topic', () => {
  const counts = new Map<string, number>()
  for (const k of keys(allItems)) counts.set(k, (counts.get(k) ?? 0) + 1)
  for (const m of METRIC_TYPES) assert.equal(counts.get(`threshold:${m}`), 1, `threshold ${m}`)
  for (const r of STATUS_RULES) assert.equal(counts.get(`status:${r.rule}`), 1, `status ${r.rule}`)
  // Katalog, server'ın bilmediği bir kural da içermez.
  assert.equal(allItems.filter((i) => i.kind === 'threshold').length, METRIC_TYPES.length)
  assert.equal(allItems.filter((i) => i.kind === 'status').length, STATUS_RULES.length)
})

test('topic ids are unique', () => {
  assert.equal(new Set(TOPICS.map((x) => x.id)).size, TOPICS.length)
})

test('the topic comes from the address; anything unknown is the first topic', () => {
  assert.equal(resolveTopic('disk'), 'disk')
  assert.equal(resolveTopic(' saat '), 'saat')
  assert.equal(resolveTopic(null), 'cpu-bellek')
  assert.equal(resolveTopic(''), 'cpu-bellek')
  assert.equal(resolveTopic('yok'), 'cpu-bellek')
})

test('selections are shown only in the host scope; automatic alerts in every scope', () => {
  const disk = topicInfo('disk')
  assert.ok(keys(topicItems(disk, 'sunucu')).includes('selection:disk_mounts'))
  for (const scope of ['sistem', 'org'] as const) {
    const shown = keys(topicItems(disk, scope))
    assert.ok(!shown.includes('selection:disk_mounts'), scope)
    assert.ok(shown.includes('auto:disk_missing'), scope)
  }
})

test('the count covers only rules that can be switched on, not selections or automatic alerts', () => {
  const items = topicItems(topicInfo('disk'), 'sunucu')
  const on = new Set(['threshold:disk', 'status:fs_readonly'])
  assert.deepEqual(
    ruleCount(items, (i) => on.has(itemKey(i))),
    { on: 2, total: 5 },
  )
  assert.deepEqual(
    ruleCount(topicItems(topicInfo('erisilebilirlik'), 'sunucu'), () => true),
    { on: 0, total: 0 },
  )
})

test('a topic is marked when one of its items has unsaved changes', () => {
  const changed = new Set(['threshold:disk_latency'])
  assert.equal(topicHasChanges(topicInfo('disk').items, changed), true)
  assert.equal(topicHasChanges(topicInfo('cpu-bellek').items, changed), false)
  assert.equal(topicHasChanges(topicInfo('disk').items, new Set()), false)
})
