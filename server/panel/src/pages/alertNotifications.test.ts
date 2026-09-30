import assert from 'node:assert/strict'
import { test } from 'node:test'
import { attemptsText, deliveryTime, groupNotifications, groupSummary, recipientsText } from './alertNotifications.ts'

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

test('rows of the same alert event are grouped; each recipient keeps its own status', () => {
  const row = (id: string, event: 'opened' | 'resolved', at: string, status: 'sent' | 'pending' | 'failed', to: string) =>
    ({ id, event, level: 'critical', channel: 'email', status, attempts: 1, recipient_count: 1, recipients: [to], subject: 's', body: 'b', created_at: at }) as const
  const groups = groupNotifications([
    row('1', 'opened', 't1', 'sent', 'a@x'),
    row('2', 'opened', 't1', 'pending', 'b@x'),
    row('3', 'resolved', 't2', 'sent', 'a@x'),
    row('4', 'opened', 't1', 'failed', 'c@x'),
  ])
  assert.deepEqual(groups.map((g) => [g.event, g.items.map((i) => i.id).join(',')]), [['opened', '1,2,4'], ['resolved', '3']])
  assert.equal(groupSummary(groups[0].items), '3 alıcı: 1 gönderildi, 1 bekliyor, 1 vazgeçildi')
  assert.equal(groupSummary(groups[1].items), '1 alıcı: 1 gönderildi')
})
