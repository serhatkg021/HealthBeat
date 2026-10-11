import assert from 'node:assert/strict'
import { test } from 'node:test'
import type { NotificationPreview } from '../types/api.ts'
import { alsoLabel, nobodyNotified, ownerCounts, skipReasonLabel, sourceLabel } from './notificationPreview.ts'

const preview = (over: Partial<NotificationPreview>): NotificationPreview => ({
  host_id: 'h',
  level: 'critical',
  in_maintenance: false,
  recipients: [],
  skipped: [],
  owners_summary: null,
  ...over,
})

test('sources name the owner, the organization and the host', () => {
  assert.equal(sourceLabel({ kind: 'owner' }), 'Sistem sahibi')
  assert.equal(sourceLabel({ kind: 'organization', name: 'Üretim' }), 'Organizasyon: Üretim')
  assert.equal(sourceLabel({ kind: 'host' }, 'web-01'), 'Sunucu: web-01')
  assert.equal(sourceLabel({ kind: 'host' }), 'Sunucu kuralı')
})

test('other sources of the same address are listed after "ayrıca"', () => {
  assert.equal(alsoLabel(undefined), '')
  assert.equal(alsoLabel([]), '')
  assert.equal(alsoLabel([{ kind: 'organization', name: 'Üretim' }, { kind: 'host' }], 'web-01'), 'ayrıca: Organizasyon: Üretim, Sunucu: web-01')
})

test('skip reasons are readable and unknown ones are shown as is', () => {
  assert.equal(skipReasonLabel('channel_off'), 'kanal kapalı')
  assert.equal(skipReasonLabel('channel_not_supported'), 'kanal henüz desteklenmiyor')
  assert.equal(skipReasonLabel('new_reason'), 'new_reason')
  assert.equal(skipReasonLabel(undefined), '')
})

test('hidden owners are counted per channel; skipped ones carry their reason', () => {
  assert.deepEqual(ownerCounts(null), { sent: '', skipped: [] })
  assert.deepEqual(
    ownerCounts([
      { channel: 'email', count: 2 },
      { channel: 'sms', count: 1 },
      { channel: 'slack', count: 1, reason: 'channel_not_supported' },
    ]),
    { sent: 'E-posta: 2 · SMS: 1', skipped: ['Slack: 1 (kanal henüz desteklenmiyor)'] },
  )
})

test('nobody is notified only when no recipient and no owner receives it', () => {
  assert.equal(nobodyNotified(preview({})), true)
  assert.equal(nobodyNotified(preview({ owners_summary: [] })), true)
  assert.equal(nobodyNotified(preview({ owners_summary: [{ channel: 'slack', count: 1, reason: 'channel_off' }] })), true)
  assert.equal(nobodyNotified(preview({ owners_summary: [{ channel: 'email', count: 1 }] })), false)
  const r = { name: 'A', channel: 'email', address: 'a@x', source: { kind: 'host' as const } }
  assert.equal(nobodyNotified(preview({ recipients: [r] })), false)
  assert.equal(nobodyNotified(preview({ skipped: [{ ...r, reason: 'channel_off' }] })), true)
})
