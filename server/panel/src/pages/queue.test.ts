import assert from 'node:assert/strict'
import { test } from 'node:test'
import { activeCount, attemptsText, durationText, itemTime, oldestAgeText, poolPct, queueKindLabel, queueStatus } from './queue.ts'

test('durationText uses the two largest units', () => {
  assert.equal(durationText(-5_000), '0 sn')
  assert.equal(durationText(45_000), '45 sn')
  assert.equal(durationText(12 * 60_000 + 30_000), '12 dk')
  assert.equal(durationText(3 * 3_600_000), '3 sa')
  assert.equal(durationText(3 * 3_600_000 + 5 * 60_000), '3 sa 5 dk')
  assert.equal(durationText(2 * 86_400_000 + 4 * 3_600_000 + 59_000), '2 gün 4 sa')
  assert.equal(durationText(2 * 86_400_000), '2 gün')
})

test('oldestAgeText: an empty queue has no age', () => {
  const now = Date.parse('2026-10-02T12:00:00Z')
  assert.equal(oldestAgeText(null, now), '—')
  assert.equal(oldestAgeText('bozuk', now), '—')
  assert.equal(oldestAgeText('2026-10-02T11:48:00Z', now), '12 dk')
  // Server saati tarayıcıdan biraz ilerideyse negatif süre gösterilmez.
  assert.equal(oldestAgeText('2026-10-02T12:00:03Z', now), '0 sn')
})

test('attemptsText and activeCount', () => {
  assert.equal(attemptsText(0, 10), '—')
  assert.equal(attemptsText(3, 10), '3 / 10')
  assert.equal(activeCount({ pending: 2, retrying: 1 }), 3)
})

test('poolPct is used / max and never above 100', () => {
  assert.equal(poolPct({ acquired: 2, max: 8 }), 25)
  assert.equal(poolPct({ acquired: 9, max: 8 }), 100)
  assert.equal(poolPct({ acquired: 1, max: 0 }), 0)
})

test('labels fall back to the raw value for something the panel does not know', () => {
  assert.equal(queueKindLabel('password_reset'), 'Şifre sıfırlama')
  assert.equal(queueKindLabel('sms_code'), 'sms_code')
  assert.deepEqual(queueStatus('retrying'), { label: 'yeniden denenecek', tone: 'warning' })
})

test('itemTime: finished rows show when; waiting rows show the next attempt or that they are due', () => {
  const now = Date.parse('2026-10-02T12:00:00Z')
  const base = { sent_at: null, failed_at: null, next_attempt_at: null }
  assert.deepEqual(itemTime({ ...base, status: 'sent', sent_at: '2026-10-02T11:00:00Z' }, now), { label: 'gönderildi', at: '2026-10-02T11:00:00Z' })
  assert.deepEqual(itemTime({ ...base, status: 'failed', failed_at: '2026-10-02T11:30:00Z' }, now), { label: 'vazgeçildi', at: '2026-10-02T11:30:00Z' })
  assert.deepEqual(itemTime({ ...base, status: 'retrying', next_attempt_at: '2026-10-02T12:08:00Z' }, now), {
    label: '8 dk sonra denenecek',
    at: '2026-10-02T12:08:00Z',
  })
  assert.deepEqual(itemTime({ ...base, status: 'pending', next_attempt_at: '2026-10-02T11:59:58Z' }, now), { label: 'sırada', at: null })
  assert.deepEqual(itemTime({ ...base, status: 'pending' }, now), { label: 'sırada', at: null })
})
