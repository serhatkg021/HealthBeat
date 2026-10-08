import assert from 'node:assert/strict'
import { test } from 'node:test'
import { alertReading } from './alertText.ts'

test('percent metrics show the value and the threshold that fired', () => {
  assert.equal(alertReading({ alert_type: 'cpu', value: 97.5, threshold: 95 }), '%97,5 (eşik %95)')
  assert.equal(alertReading({ alert_type: 'disk', value: 90, threshold: 85 }), '%90 (eşik %85)')
})

test('docker restarts are counts, not percentages', () => {
  assert.equal(alertReading({ alert_type: 'docker_restart', value: 7, threshold: 5 }), '7 restart (eşik 5)')
})

test('event alerts have no reading', () => {
  assert.equal(alertReading({ alert_type: 'host_offline' }), '')
  assert.equal(alertReading({ alert_type: 'disk_missing', value: undefined, threshold: 3 }), '')
})

test('protocol 4 numeric alerts show their own unit', () => {
  assert.equal(alertReading({ alert_type: 'disk_latency', value: 14.236, threshold: 10 }), '14,2 ms (eşik 10 ms)')
  assert.equal(alertReading({ alert_type: 'time_sync', value: 1234.5678, threshold: 1000 }), '1235 ms (eşik 1000 ms)')
  assert.equal(alertReading({ alert_type: 'time_sync', value: 152.25, threshold: 100 }), '152,3 ms (eşik 100 ms)')
  assert.equal(alertReading({ alert_type: 'temperature', value: 87.25, threshold: 85 }), '87,3 °C (eşik 85 °C)')
  assert.equal(alertReading({ alert_type: 'service_restart_loop', value: 6, threshold: 3 }), '10 dakikada 6 yeniden başlatma (eşik 3)')
})

test('status alerts have no reading', () => {
  for (const t of ['service_failed', 'raid_degraded', 'reboot_required', 'oom_kill'] as const) assert.equal(alertReading({ alert_type: t }), '')
})

test('a small threshold is not rounded away', () => {
  assert.equal(alertReading({ alert_type: 'disk_latency', value: 1.0123, threshold: 0.002 }), '1,01 ms (eşik 0,002 ms)')
  assert.equal(alertReading({ alert_type: 'cpu', value: 97.5, threshold: 92.5 }), '%97,5 (eşik %92,5)')
  assert.equal(alertReading({ alert_type: 'temperature', value: 40, threshold: 38.25 }), '40,0 °C (eşik 38,25 °C)')
})
