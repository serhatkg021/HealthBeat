import assert from 'node:assert/strict'
import { test } from 'node:test'
import { attemptsText, deliveryTime, recipientsText } from './alertNotifications.ts'

test('recipients are listed when visible, otherwise only counted', () => {
  assert.equal(recipientsText({ recipients: ['ops@x.test', 'cto@x.test'], recipient_count: 2 }), 'ops@x.test, cto@x.test')
  assert.equal(recipientsText({ recipient_count: 2 }), '2 alıcı')
})

test('attempts are mentioned only when they say something', () => {
  assert.equal(attemptsText({ status: 'sent', attempts: 1 }), '')
  assert.equal(attemptsText({ status: 'sent', attempts: 3 }), '3 deneme')
  assert.equal(attemptsText({ status: 'failed', attempts: 10 }), '10 deneme')
  assert.equal(attemptsText({ status: 'pending', attempts: 0 }), 'henüz denenmedi')
})

test('the delivery time follows the status', () => {
  assert.deepEqual(deliveryTime({ status: 'sent', sent_at: 's' }), { label: 'gönderildi', at: 's' })
  assert.deepEqual(deliveryTime({ status: 'failed', failed_at: 'f' }), { label: 'vazgeçildi', at: 'f' })
  assert.deepEqual(deliveryTime({ status: 'pending', next_attempt_at: 'n' }), { label: 'sonraki deneme', at: 'n' })
})
