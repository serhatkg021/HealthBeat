import assert from 'node:assert/strict'
import { test } from 'node:test'
import { diskAlertSummary, effectiveRules } from './effectiveRules.ts'

const L = (w: number, c: number) => ({ warning_level: w, critical_level: c })

test('a custom value overrides the inherited one; a metric with neither raises no alert', () => {
  const rows = effectiveRules({
    thresholds: [
      { metric_type: 'cpu', default: L(80, 95), custom: L(70, 90) },
      { metric_type: 'ram', default: L(85, 95), custom: null },
      { metric_type: 'disk', default: null, custom: null },
    ],
    mount_thresholds: [],
    container_thresholds: [],
  })
  const by = Object.fromEntries(rows.map((r) => [r.key, r]))
  assert.deepEqual(by.cpu, { key: 'cpu', label: 'CPU', value: 'uyarı 70 % / kritik 90 %', source: 'custom' })
  assert.equal(by.ram.source, 'inherited')
  assert.equal(by.ram.value, 'uyarı 85 % / kritik 95 %')
  assert.deepEqual(by.disk, { key: 'disk', label: 'Disk', value: null, source: 'none' })
  // Yanıtta hiç görünmeyen metrik de "tanımlı değil" satırı olarak listelenir.
  assert.equal(by.docker_restart.source, 'none')
})

test('mount and container thresholds follow the metric rows, sorted and marked as custom', () => {
  const rows = effectiveRules({
    thresholds: [],
    mount_thresholds: [
      { mount: '/var', custom: L(70, 80) },
      { mount: '/backup', custom: L(90, 95) },
    ],
    container_thresholds: [{ container: 'api', custom: L(3, 5) }],
  })
  assert.deepEqual(
    rows.slice(4).map((r) => [r.label, r.source]),
    [
      ['Disk · /backup', 'custom'],
      ['Disk · /var', 'custom'],
      ['Docker restart · api', 'custom'],
    ],
  )
})

test('the disk alert selection is summarised in one line', () => {
  assert.equal(diskAlertSummary({ all_mounts_alert: true, custom_alert_mounts: ['/x'] }), 'Raporlanan tüm mount’lar')
  assert.equal(diskAlertSummary({ all_mounts_alert: false, custom_alert_mounts: [] }), 'Hiçbiri (disk alert’i üretilmez)')
  assert.equal(diskAlertSummary({ all_mounts_alert: false, custom_alert_mounts: ['/var', '/'] }), '/, /var')
})
