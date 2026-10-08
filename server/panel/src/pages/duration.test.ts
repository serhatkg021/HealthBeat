import assert from 'node:assert/strict'
import { test } from 'node:test'
import { durationDraft, durationSeconds, emptyDuration, formatDuration, parseDuration, sameDuration } from './duration.ts'

test('an empty duration means "immediately"', () => {
  assert.deepEqual(parseDuration(emptyDuration()), { seconds: undefined })
  assert.deepEqual(parseDuration({ value: '  ', unit: 'sa' }), { seconds: undefined })
})

test('durations are entered with a unit and stored in seconds', () => {
  assert.deepEqual(parseDuration({ value: '45', unit: 'sn' }), { seconds: 45 })
  assert.deepEqual(parseDuration({ value: '10', unit: 'dk' }), { seconds: 600 })
  assert.deepEqual(parseDuration({ value: '1,5', unit: 'dk' }), { seconds: 90 })
  assert.deepEqual(parseDuration({ value: '2', unit: 'sa' }), { seconds: 7200 })
})

test('invalid durations are explained', () => {
  assert.ok('error' in parseDuration({ value: 'abc', unit: 'dk' }))
  assert.ok('error' in parseDuration({ value: '0', unit: 'dk' }))
  assert.ok('error' in parseDuration({ value: '-5', unit: 'sn' }))
  assert.ok('error' in parseDuration({ value: '0,2', unit: 'sn' }))
  assert.match((parseDuration({ value: '721', unit: 'sa' }) as { error: string }).error, /30 gün/)
  assert.deepEqual(parseDuration({ value: '720', unit: 'sa' }), { seconds: 30 * 24 * 3600 })
})

test('a stored duration is shown in the largest unit that divides it', () => {
  assert.deepEqual(durationDraft(undefined), emptyDuration())
  assert.deepEqual(durationDraft(600), { value: '10', unit: 'dk' })
  assert.deepEqual(durationDraft(7200), { value: '2', unit: 'sa' })
  assert.deepEqual(durationDraft(90), { value: '90', unit: 'sn' })
})

test('sameDuration compares the meaning, not the spelling', () => {
  assert.ok(sameDuration({ value: '10', unit: 'dk' }, { value: '600', unit: 'sn' }))
  assert.ok(sameDuration(emptyDuration(), { value: '', unit: 'sa' }))
  assert.ok(!sameDuration({ value: '10', unit: 'dk' }, emptyDuration()))
  assert.ok(!sameDuration({ value: 'x', unit: 'dk' }, { value: 'y', unit: 'dk' }))
  assert.equal(durationSeconds({ value: 'x', unit: 'dk' }), undefined)
})

test('formatDuration is exact', () => {
  assert.equal(formatDuration(45), '45 sn')
  assert.equal(formatDuration(90), '1 dk 30 sn')
  assert.equal(formatDuration(600), '10 dk')
  assert.equal(formatDuration(5400), '1 sa 30 dk')
  assert.equal(formatDuration(86400), '1 gün')
  assert.equal(formatDuration(93600), '1 gün 2 sa')
  assert.equal(formatDuration(0), '0 sn')
})
