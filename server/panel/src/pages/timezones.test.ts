import assert from 'node:assert/strict'
import { test } from 'node:test'
import { formatOffset, offsetMinutes, timezoneOptions } from './timezones.ts'

test('offsets are written like the server writes them', () => {
  assert.equal(formatOffset(0), 'UTC')
  assert.equal(formatOffset(180), 'UTC+3')
  assert.equal(formatOffset(330), 'UTC+5:30')
  assert.equal(formatOffset(-240), 'UTC−4')
})

test('the offset follows daylight saving at that moment', () => {
  assert.equal(offsetMinutes('Europe/Berlin', new Date('2026-07-15T10:00:00Z')), 120)
  assert.equal(offsetMinutes('Europe/Berlin', new Date('2026-12-15T10:00:00Z')), 60)
  assert.equal(offsetMinutes('Europe/Istanbul', new Date('2026-12-15T10:00:00Z')), 180)
  assert.equal(offsetMinutes('Mars/Olympus', new Date()), null)
})

test('options are sorted by offset, then name, and always include UTC', () => {
  const opts = timezoneOptions(new Date('2026-12-15T10:00:00Z'), ['Europe/Istanbul', 'Europe/Berlin', 'Asia/Kolkata', 'Mars/Olympus'])
  assert.deepEqual(opts.map((o) => o.label), ['UTC (UTC)', 'Europe/Berlin (UTC+1)', 'Europe/Istanbul (UTC+3)', 'Asia/Kolkata (UTC+5:30)'])
})
