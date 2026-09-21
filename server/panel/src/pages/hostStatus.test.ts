import assert from 'node:assert/strict'
import { test } from 'node:test'
import { statusDuration } from './hostStatus.ts'

const NOW = Date.parse('2026-09-21T12:00:00Z')
const ago = (s: number) => new Date(NOW - s * 1000).toISOString()

test('online: shows the reported uptime', () => {
  const d = statusDuration({ status: 'online', last_seen: ago(5), host_info: { uptime_seconds: 3 * 3600 + 120 } }, NOW)
  assert.equal(d?.text, '3 sa 2 dk')
  assert.equal(d?.hint, 'çalışma süresi')
})

test('online without an inventory (old agent) has no duration', () => {
  assert.equal(statusDuration({ status: 'online', last_seen: ago(5) }, NOW), null)
})

test('offline: shows the time since the last data, never the stale uptime', () => {
  const d = statusDuration({ status: 'offline', last_seen: ago(2 * 3600 + 5 * 60), host_info: { uptime_seconds: 999999 } }, NOW)
  assert.equal(d?.text, '2 sa 5 dk')
  assert.equal(d?.hint, 'son veriden bu yana')
  const days = statusDuration({ status: 'offline', last_seen: ago(3 * 86400 + 4 * 3600) }, NOW)
  assert.equal(days?.text, '3 gün 4 sa')
  assert.equal(statusDuration({ status: 'offline', last_seen: ago(45) }, NOW)?.text, '45 sn')
})

test('offline without any data ever received says so', () => {
  const d = statusDuration({ status: 'offline' }, NOW)
  assert.equal(d?.text, '—')
  assert.equal(d?.hint, 'hiç veri gelmedi')
  assert.equal(statusDuration({ status: 'offline', last_seen: 'bozuk' }, NOW)?.hint, 'hiç veri gelmedi')
})

test('a last_seen slightly in the future (clock skew) is clamped to zero', () => {
  assert.equal(statusDuration({ status: 'offline', last_seen: ago(-30) }, NOW)?.text, '0 sn')
})
