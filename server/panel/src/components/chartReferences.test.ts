import assert from 'node:assert/strict'
import { test } from 'node:test'
import type { HostThresholdsResponse } from '../types/api.ts'
import { rawValue, referenceLabels, splitReferences, thresholdReferences } from './chartReferences.ts'

const res = (views: HostThresholdsResponse['thresholds']): HostThresholdsResponse => ({ thresholds: views, mount_thresholds: [], container_thresholds: [], subject_thresholds: [] })
const pct = (v: number) => `%${v}`

test('a host value wins over the inherited one; nothing is drawn when undefined', () => {
  const r = res([
    { metric_type: 'cpu', default: { warning_level: 60, critical_level: 90 }, custom: null },
    { metric_type: 'ram', default: { warning_level: 80, critical_level: 95 }, custom: { warning_level: 70, critical_level: 85 } },
    { metric_type: 'disk', default: null, custom: null },
  ])
  assert.deepEqual(thresholdReferences(r, 'cpu', pct), [
    { value: 60, label: 'uyarı %60', tone: 'warning' },
    { value: 90, label: 'kritik %90', tone: 'critical' },
  ])
  assert.deepEqual(
    thresholdReferences(r, 'ram', pct).map((x) => x.value),
    [70, 85],
  )
  assert.deepEqual(thresholdReferences(r, 'disk', pct), [])
  assert.deepEqual(thresholdReferences(null, 'cpu', pct), [])
})

test('a threshold above the scale does not stretch it; it is listed instead', () => {
  const refs = [
    { value: 30, label: 'uyarı 30 ms', tone: 'warning' as const },
    { value: 50, label: 'kritik 50 ms', tone: 'critical' as const },
  ]
  assert.deepEqual(splitReferences(refs, 40), { inside: [refs[0]], above: [refs[1]] })
  assert.deepEqual(splitReferences(refs, 2), { inside: [], above: refs })
  assert.deepEqual(splitReferences(refs, 50), { inside: refs, above: [] })
})

test('small thresholds keep their value; close lines share one label', () => {
  assert.equal(rawValue(0.001, 'ms'), '0,001 ms')
  assert.equal(rawValue(30, 'ms'), '30 ms')
  const warn = { value: 0.001, label: 'uyarı 0,001 ms', tone: 'warning' as const }
  const crit = { value: 0.002, label: 'kritik 0,002 ms', tone: 'critical' as const }
  assert.deepEqual(referenceLabels([warn, crit], 1.3), ['', 'uyarı 0,001 ms · kritik 0,002 ms'])
  const w60 = { value: 60, label: 'uyarı %60', tone: 'warning' as const }
  const c90 = { value: 90, label: 'kritik %90', tone: 'critical' as const }
  assert.deepEqual(referenceLabels([w60, c90], 100), ['uyarı %60', 'kritik %90'])
  assert.deepEqual(referenceLabels([c90], 100), ['kritik %90'])
})
