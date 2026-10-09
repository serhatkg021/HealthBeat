import assert from 'node:assert/strict'
import { test } from 'node:test'
import type { StatusRuleConfig, ThresholdConfig } from '../types/api.ts'
import { changedKeys, customizeItem, hostRow, inheritItem } from './hostRuleRows.ts'
import { hostSources, keepFailedScope, scopeRules, scopeSavePlan } from './scopeRules.ts'

const t = (id: string, metric: ThresholdConfig['metric_type'], w: number, c: number, org?: string, duration?: number): ThresholdConfig => ({
  id,
  metric_type: metric,
  warning_level: w,
  critical_level: c,
  created_at: '',
  updated_at: '',
  ...(org ? { organization_id: org } : {}),
  ...(duration ? { duration_seconds: duration } : {}),
})
const r = (rule: StatusRuleConfig['rule'], level: StatusRuleConfig['level'], org?: string): StatusRuleConfig => ({ rule, level, updated_at: '', ...(org ? { organization_id: org } : {}) })

// Ana → Alt; Ana'nın CPU'su ve RAID kuralı var, sistemin CPU/RAM'i ve OOM kuralı var.
const parents = new Map<string, string | undefined>([
  ['ana', undefined],
  ['alt', 'ana'],
])
const names: Record<string, string> = { ana: 'Ana Şirket', alt: 'Alt Şirket' }
const nameOf = (id: string) => names[id] ?? id
const thresholds = [t('g-cpu', 'cpu', 80, 90), t('g-ram', 'ram', 80, 95), t('a-cpu', 'cpu', 70, 85, 'ana'), t('x-ram', 'ram', 60, 70, 'alt')]
const status = [r('oom_kill', 'warning'), r('raid_degraded', 'critical', 'ana'), r('fs_readonly', 'off')]

test('the system scope has its own rows only; "off" counts as undefined there', () => {
  const { state, ids, sources } = scopeRules(thresholds, status, {}, parents, nameOf)
  assert.deepEqual([state.thresholds.cpu.mode, state.thresholds.cpu.warning], ['custom', '80'])
  assert.equal(state.thresholds.disk.mode, 'default')
  assert.deepEqual(ids, { cpu: 'g-cpu', ram: 'g-ram' })
  assert.deepEqual(state.defaults, {})
  assert.equal(state.status.oom_kill.level, 'warning')
  assert.equal(state.status.fs_readonly.level, '')
  assert.deepEqual(sources, { thresholds: {}, status: {} })
})

test('an organization inherits from its parent, then the system, and names the source', () => {
  const { state, ids, sources } = scopeRules(thresholds, status, { organizationId: 'alt' }, parents, nameOf)
  assert.deepEqual(ids, { ram: 'x-ram' })
  assert.equal(state.thresholds.cpu.mode, 'default')
  assert.deepEqual(state.defaults.cpu, { warning_level: 70, critical_level: 85 })
  assert.equal(sources.thresholds.cpu, 'Ana Şirket')
  // Kendi RAM'i olsa da devralınacak değer (sistem) bilinir: "Devral" deyince ne olacağı görünür.
  assert.equal(sources.thresholds.ram, 'Sistem')
  assert.deepEqual(state.statusDefaults.raid_degraded, { level: 'critical' })
  assert.equal(sources.status.raid_degraded, 'Ana Şirket')
  assert.equal(sources.status.oom_kill, 'Sistem')
  assert.equal(hostRow({ kind: 'threshold', metric: 'cpu' }, state).source, 'inherited')
  assert.equal(hostRow({ kind: 'threshold', metric: 'ram' }, state).source, 'own')
})

test('a host names where each inherited value comes from', () => {
  const sources = hostSources(thresholds, status, 'alt', parents, nameOf)
  assert.equal(sources.thresholds.cpu, 'Ana Şirket')
  assert.equal(sources.thresholds.ram, 'Alt Şirket')
  assert.equal(sources.thresholds.disk, undefined)
  assert.equal(sources.status.raid_degraded, 'Ana Şirket')
  assert.equal(sources.status.oom_kill, 'Sistem')
})

test('the save plan creates, updates and removes one threshold at a time; status rules go together', () => {
  const { state: saved, ids } = scopeRules(thresholds, status, { organizationId: 'alt' }, parents, nameOf)
  let draft = customizeItem({ kind: 'threshold', metric: 'cpu' }, saved)
  draft = inheritItem({ kind: 'threshold', metric: 'ram' }, draft)
  draft = customizeItem({ kind: 'status', rule: 'oom_kill' }, draft)
  const plan = scopeSavePlan(saved, draft, ids)
  assert.deepEqual(plan.thresholds, [
    { metric: 'cpu', op: 'create', body: { warning_level: 70, critical_level: 85 } },
    { metric: 'ram', op: 'remove', id: 'x-ram' },
  ])
  assert.deepEqual(plan.status, { oom_kill: { level: 'warning' } })

  const edited = { ...saved, thresholds: { ...saved.thresholds, ram: { ...saved.thresholds.ram, warning: '65' } } }
  assert.deepEqual(scopeSavePlan(saved, edited, ids), { thresholds: [{ metric: 'ram', op: 'update', id: 'x-ram', body: { warning_level: 65, critical_level: 70 } }] })
  // Süre alan türde süre her zaman gönderilir (boş = null, süreyi kaldır).
  const latency = customizeItem({ kind: 'threshold', metric: 'disk_latency' }, saved)
  const withLevels = { ...latency, thresholds: { ...latency.thresholds, disk_latency: { ...latency.thresholds.disk_latency, warning: '30', critical: '50' } } }
  assert.deepEqual(scopeSavePlan(saved, withLevels, ids).thresholds[0], {
    metric: 'disk_latency',
    op: 'create',
    body: { warning_level: 30, critical_level: 50, duration_seconds: null },
  })
})

test('after a partial failure the failed thresholds and status rules stay in the draft', () => {
  const { state: saved } = scopeRules(thresholds, status, { organizationId: 'alt' }, parents, nameOf)
  let draft = customizeItem({ kind: 'threshold', metric: 'cpu' }, saved)
  draft = inheritItem({ kind: 'threshold', metric: 'ram' }, draft)
  draft = customizeItem({ kind: 'status', rule: 'oom_kill' }, draft)
  // cpu kaydedildi (server'dan özel hâliyle geldi), ram ve durum kuralları kaydedilemedi.
  const fresh = { ...saved, thresholds: { ...saved.thresholds, cpu: draft.thresholds.cpu } }
  const next = keepFailedScope(fresh, draft, new Set(['ram']), true)
  assert.deepEqual([...changedKeys(fresh, next)].sort(), ['status:oom_kill', 'threshold:ram'])
})
