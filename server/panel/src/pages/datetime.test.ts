import assert from 'node:assert/strict'
import { test } from 'node:test'
import { EMPTY, formatDate, formatDateTime, formatTime, inputValue, parseInputValue, setDisplayTimeZone } from './datetime.ts'

test('times are shown in the installation zone as DD.MM.YYYY HH:MM:SS, whatever the browser', () => {
  setDisplayTimeZone('Europe/Istanbul', 10800)
  assert.equal(formatDateTime('2026-10-11T19:51:41Z'), '11.10.2026 22:51:41')
  assert.equal(formatDateTime('2026-10-11T19:51:41Z', { seconds: false }), '11.10.2026 22:51')
  assert.equal(formatDate('2026-10-11T22:30:00Z'), '12.10.2026') // kurulumun gününde
  assert.equal(formatTime(Date.parse('2026-10-11T19:51:41Z')), '22:51:41')
  assert.equal(formatTime(new Date('2026-10-11T19:51:41Z'), { seconds: false }), '22:51')
})

test('a named zone follows daylight saving time', () => {
  setDisplayTimeZone('America/New_York', -14400)
  assert.equal(formatDateTime('2026-07-01T12:00:00Z'), '01.07.2026 08:00:00')
  assert.equal(formatDateTime('2026-12-01T12:00:00Z'), '01.12.2026 07:00:00')
})

test('an unknown zone name falls back to the fixed offset; no zone at all means the browser zone', () => {
  setDisplayTimeZone('Local', 10800)
  assert.equal(formatDateTime('2026-10-11T19:51:41Z'), '11.10.2026 22:51:41')
  setDisplayTimeZone(undefined, undefined)
  const local = new Date(2026, 9, 11, 22, 51, 41)
  assert.equal(formatDateTime(local), '11.10.2026 22:51:41')
})

test('empty or invalid values are a dash', () => {
  setDisplayTimeZone('UTC', 0)
  for (const v of [null, undefined, '', 'bozuk']) {
    assert.equal(formatDateTime(v), EMPTY)
    assert.equal(formatDate(v), EMPTY)
    assert.equal(formatTime(v), EMPTY)
  }
})

test('datetime-local values are read and written in the installation zone', () => {
  setDisplayTimeZone('Europe/Istanbul', 10800)
  assert.equal(inputValue('2026-10-11T19:51:41Z'), '2026-10-11T22:51')
  assert.equal(parseInputValue('2026-10-11T22:51')?.toISOString(), '2026-10-11T19:51:00.000Z')
  assert.equal(parseInputValue('2026-10-11 22:51'), null)
  assert.equal(parseInputValue('2026-10-02T25:99'), null)
  assert.equal(parseInputValue('2026-02-31T10:00'), null)

  setDisplayTimeZone('America/New_York', -14400)
  assert.equal(parseInputValue('2026-07-01T08:00')?.toISOString(), '2026-07-01T12:00:00.000Z')
  assert.equal(parseInputValue('2026-12-01T07:00')?.toISOString(), '2026-12-01T12:00:00.000Z')
  for (const v of ['2026-03-09T10:15', '2026-11-02T00:30']) assert.equal(inputValue(parseInputValue(v)!), v)

  setDisplayTimeZone('Local', 10800)
  assert.equal(parseInputValue('2026-10-11T22:51')?.toISOString(), '2026-10-11T19:51:00.000Z')
})
