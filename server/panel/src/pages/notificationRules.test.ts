import assert from 'node:assert/strict'
import { test } from 'node:test'
import type { ChannelOption, NotificationRoute, RecipientCandidate } from '../types/api.ts'
import { candidateKey, candidateLabel, candidatesForNewRoute, channelChoices, describeCoverage } from './notificationRules.ts'

const user = (id: string, name: string, source: RecipientCandidate['source'] = 'org_admin'): RecipientCandidate => ({ user_id: id, name, email: `${name}@x.test`, source })
const contact = (id: string, name: string): RecipientCandidate => ({ contact_id: id, name, email: `${name}@musteri.test`, source: 'contact' })
const route = (over: Partial<NotificationRoute>): NotificationRoute => ({ id: 'r', channel: 'email', min_level: 'warning', created_at: '', channel_enabled: true, ...over })

test('users and contacts with the same id never collide', () => {
  assert.notEqual(candidateKey({ user_id: '1' }), candidateKey({ contact_id: '1' }))
})

test('a recipient already covered on the same channel is not offered again', () => {
  const cands = [user('u1', 'ali'), contact('c1', 'ayse'), contact('c2', 'veli')]
  const routes = [route({ user_id: 'u1' }), route({ contact_id: 'c1', channel: 'sms' })]
  assert.deepEqual(candidatesForNewRoute(cands, routes, 'email').map((c) => c.name), ['ayse', 'veli'])
  assert.deepEqual(candidatesForNewRoute(cands, routes, 'sms').map((c) => c.name), ['ali', 'veli']) // kanal başına
})

test('labels show name, address and where the recipient comes from', () => {
  assert.equal(candidateLabel(user('u1', 'ali', 'super_admin')), 'ali — ali@x.test (Süper Admin)')
  assert.equal(candidateLabel({ contact_id: 'c', name: 'Ayşe', phone: '+90 555', source: 'contact' }), 'Ayşe — +90 555 (İletişim kişisi)')
  assert.equal(candidateLabel({ user_id: 'u', name: 'x', source: 'operator' }), 'x (Operatör)')
  assert.equal(candidateLabel({ contact_id: 'c', name: 'NOC', email: 'noc@x.test', source: 'system_contact' }), 'NOC — noc@x.test (Sistem kişisi)')
})

test('the coverage text says system rules also apply and rules add up', () => {
  for (const text of [describeCoverage('organization', true), describeCoverage('host', false)]) {
    assert.match(text, /sistem kurallarındaki alıcılara da gider/)
    assert.match(text, /onlara eklenir/)
    assert.doesNotMatch(text, /YALNIZCA|varsayılan alıcı|sistem sahib/)
  }
  assert.match(describeCoverage('host', true), /birlikte uygulanır/)
  assert.match(describeCoverage('organization', true), /→ Sistem\)/)
  assert.doesNotMatch(describeCoverage('organization', false), /→/)
  // Sistem kapsamı: bütün sunucular; alıcılar yalnızca süper adminler ve sistem kişileri.
  assert.match(describeCoverage('system', true), /bütün sunucuların/)
  assert.match(describeCoverage('system', true), /süper adminler ve sistem kişileri/)
})

test('only personal channels the server can send are offered; closed ones say why', () => {
  const opt = (channel: ChannelOption['channel'], over: Partial<ChannelOption> = {}): ChannelOption => ({
    channel, enabled: true, ready: true, personal: true, implemented: true, ...over,
  })
  assert.deepEqual(
    channelChoices([
      opt('email'),
      opt('sms', { enabled: false, ready: false }),
      opt('telegram', { enabled: false }),
      opt('slack', { personal: false }), // ortak hedefe gider: kuralın alıcısı bir kişidir
      opt('discord', { implemented: false }),
    ]),
    [
      { id: 'email', label: 'E-posta', unavailable: undefined },
      { id: 'sms', label: 'SMS', unavailable: 'ayar gerekli' },
      { id: 'telegram', label: 'Telegram', unavailable: 'kapalı' },
    ],
  )
})
