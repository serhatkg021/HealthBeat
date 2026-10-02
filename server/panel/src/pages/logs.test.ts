import assert from 'node:assert/strict'
import { test } from 'node:test'
import { NO_FILTERS, clockToInstant, dayOptionLabel, diskUsage, entryTime, filtersActive, levelBadge, prettyValue, requestIdOf, summaryAttrs } from './logs.ts'

test('levelBadge: tone by level; unparsed lines are "ham"', () => {
  assert.deepEqual(levelBadge('ERROR'), { label: 'ERROR', tone: 'critical' })
  assert.deepEqual(levelBadge('ERROR+2'), { label: 'ERROR', tone: 'critical' })
  assert.deepEqual(levelBadge('WARN'), { label: 'WARN', tone: 'warning' })
  assert.deepEqual(levelBadge('INFO'), { label: 'INFO', tone: 'neutral' })
  assert.deepEqual(levelBadge(''), { label: 'ham', tone: 'neutral' })
  assert.deepEqual(levelBadge('TRACE'), { label: 'TRACE', tone: 'neutral' })
})

const attrs = [
  { key: 'status', value: '401' },
  { key: 'request_id', value: 'r-1' },
  { key: 'method', value: 'POST' },
  { key: 'resp_body', value: '{"code":"unauthorized"}' },
  { key: 'path', value: '/api/v1/auth/login' },
]

test('summaryAttrs shows the request fields first, in a fixed order, and leaves the rest for the detail', () => {
  assert.deepEqual(
    summaryAttrs({ attrs }).map((a) => `${a.key}=${a.value}`),
    ['method=POST', 'path=/api/v1/auth/login', 'status=401'],
  )
  assert.deepEqual(summaryAttrs({ attrs: [] }), [])
})

test('requestIdOf', () => {
  assert.equal(requestIdOf({ attrs }), 'r-1')
  assert.equal(requestIdOf({ attrs: [{ key: 'request_id', value: '' }] }), null)
  assert.equal(requestIdOf({ attrs: [] }), null)
})

test('prettyValue indents JSON and leaves everything else alone', () => {
  assert.equal(prettyValue('{"code":"unauthorized","n":1}'), '{\n  "code": "unauthorized",\n  "n": 1\n}')
  assert.equal(prettyValue('[1,2]'), '[\n  1,\n  2\n]')
  assert.equal(prettyValue('{bozuk}'), '{bozuk}')
  assert.equal(prettyValue('smtp auth: 535'), 'smtp auth: 535')
  assert.equal(prettyValue(''), '')
})

test('entryTime shows the browser-local clock with milliseconds', () => {
  const local = new Date(2026, 9, 2, 22, 41, 10, 631)
  assert.equal(entryTime(local.toISOString()), '22:41:10.631')
  assert.equal(entryTime(null), '—')
  assert.equal(entryTime('bozuk'), '—')
})

test('dayOptionLabel', () => {
  assert.equal(dayOptionLabel({ day: '2026-10-02', parts: 1, bytes: 512, compressed: false }), '2026-10-02 · 512 B')
  assert.match(dayOptionLabel({ day: '2026-10-01', parts: 3, bytes: 5 * 1024 * 1024, compressed: true }), /^2026-10-01 · .+ · 3 parça · sıkıştırılmış$/)
})

test('diskUsage: used over the cap; no percentage while the cap is unknown', () => {
  const u = diskUsage({ total_bytes: 512, max_total_bytes: 2048 })
  assert.equal(u.pct, 25)
  assert.match(u.text, /^512 B \/ /)
  assert.deepEqual(diskUsage({ total_bytes: 0, max_total_bytes: 0 }), { pct: 0, text: '0 B' })
  assert.equal(diskUsage({ total_bytes: 4096, max_total_bytes: 2048 }).pct, 100)
})

test('clockToInstant turns a local clock on the chosen day into an instant', () => {
  assert.equal(clockToInstant('2026-10-02', '10:30'), new Date(2026, 9, 2, 10, 30, 0).toISOString())
  assert.equal(clockToInstant('2026-10-02', ''), undefined)
  assert.equal(clockToInstant('2026-10-02', '25:99'), undefined)
  assert.equal(clockToInstant('2026-10-02', '9:5'), undefined)
})

test('filtersActive', () => {
  assert.equal(filtersActive(NO_FILTERS), false)
  assert.equal(filtersActive({ ...NO_FILTERS, q: '  ' }), false)
  assert.equal(filtersActive({ ...NO_FILTERS, level: 'warn' }), true)
  assert.equal(filtersActive({ ...NO_FILTERS, requestId: 'r-1' }), true)
})
