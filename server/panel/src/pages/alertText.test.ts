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
