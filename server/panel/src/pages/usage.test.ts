import assert from 'node:assert/strict'
import { test } from 'node:test'
import { clampPct, containerLevels, effectiveLevels, mountLevels, pctText, usageAriaLabel, usageTone } from './usage.ts'
import type { HostThresholdView } from '../types/api.ts'

const lv = (warning_level: number, critical_level: number) => ({ warning_level, critical_level })

test('usageTone follows the alert engine: >= critical, then >= warning, else good', () => {
  const l = lv(80, 90)
  assert.equal(usageTone(79.9, l), 'good')
  assert.equal(usageTone(80, l), 'warning', 'the warning level itself is already a warning')
  assert.equal(usageTone(89.9, l), 'warning')
  assert.equal(usageTone(90, l), 'critical', 'the critical level itself is already critical')
  assert.equal(usageTone(100, l), 'critical')
  assert.equal(usageTone(0, l), 'good')
})

test('usageTone: without thresholds (or a NaN value) nothing is coloured as a problem', () => {
  assert.equal(usageTone(99, null), 'good')
  assert.equal(usageTone(99, undefined), 'good')
  assert.equal(usageTone(Number.NaN, lv(80, 90)), 'good')
})

const views: HostThresholdView[] = [
  { metric_type: 'cpu', default: lv(70, 90), custom: null },
  { metric_type: 'ram', default: lv(80, 95), custom: lv(60, 75) },
  { metric_type: 'disk', default: lv(85, 95), custom: null },
  { metric_type: 'docker_restart', default: null, custom: null },
]

test('effectiveLevels: a custom value wins over the default; nothing defined is null', () => {
  assert.deepEqual(effectiveLevels(views, 'cpu'), lv(70, 90))
  assert.deepEqual(effectiveLevels(views, 'ram'), lv(60, 75))
  assert.equal(effectiveLevels(views, 'docker_restart'), null)
  assert.equal(effectiveLevels(undefined, 'cpu'), null)
  assert.equal(effectiveLevels([], 'cpu'), null)
})

test('mountLevels: a mount with its own threshold uses it, others follow the server disk threshold', () => {
  const mounts = [{ mount: '/storage', custom: lv(50, 60) }]
  assert.deepEqual(mountLevels(views, mounts, '/storage'), lv(50, 60))
  assert.deepEqual(mountLevels(views, mounts, '/'), lv(85, 95))
  assert.deepEqual(mountLevels(views, undefined, '/'), lv(85, 95))
  assert.equal(mountLevels([], mounts, '/'), null)
})

test('clampPct keeps the bar inside its track', () => {
  assert.equal(clampPct(-5), 0)
  assert.equal(clampPct(42.5), 42.5)
  assert.equal(clampPct(180), 100)
  assert.equal(clampPct(Number.NaN), 0)
  assert.equal(clampPct(Number.POSITIVE_INFINITY), 0)
})

test('pctText and the screen-reader label never rely on colour alone', () => {
  assert.equal(pctText(42.46), '%42.5')
  assert.equal(pctText(undefined), '—')
  assert.equal(pctText(null), '—')
  assert.equal(usageAriaLabel('RAM', 91, 'critical'), 'RAM: %91.0, kritik seviyede')
  assert.equal(usageAriaLabel('CPU', 10, 'good'), 'CPU: %10.0, normal')
  assert.equal(usageAriaLabel('/', 82, 'warning'), '/: %82.0, uyarı seviyesinde')
})

test('containerLevels: a container with its own restart threshold uses it, others follow the server one', () => {
  const v: HostThresholdView[] = [{ metric_type: 'docker_restart', default: lv(3, 5), custom: null }]
  const cs = [{ container: 'db', custom: lv(1, 2) }]
  assert.deepEqual(containerLevels(v, cs, 'db'), lv(1, 2))
  assert.deepEqual(containerLevels(v, cs, 'web'), lv(3, 5))
  assert.equal(containerLevels([], undefined, 'web'), null)
})
