import assert from 'node:assert/strict'
import { test } from 'node:test'
import { serverClock } from './serverClock.ts'

test('the server clock runs on from the meta answer in the installation offset', () => {
  const meta = { timezone: 'Europe/Istanbul', utc_offset: 'UTC+3', utc_offset_seconds: 3 * 3600, server_time: '2026-10-10T18:11:30Z', fetchedAt: 1000 }
  assert.deepEqual(serverClock(meta, 1000), { timezone: 'Europe/Istanbul', offset: 'UTC+3', nowLocal: '2026-10-10T21:11', label: '2026.10.10 21:11 (+3)', short: '+3' })
  assert.equal(serverClock(meta, 1000 + 3 * 3600_000).label, '2026.10.11 00:11 (+3)') // gece yarısını geçer
  assert.equal(serverClock(meta, 0).nowLocal, '2026-10-10T21:11') // tarayıcı saati geri gitse de geri sarmaz
  assert.equal(serverClock({ ...meta, timezone: 'UTC', utc_offset: 'UTC', utc_offset_seconds: 0 }, 1000).label, '2026.10.10 18:11 (UTC)')
  assert.equal(serverClock({ ...meta, utc_offset: 'UTC+5:30', utc_offset_seconds: 19800 }, 1000).label, '2026.10.10 23:41 (+5:30)')
})
