import assert from 'node:assert/strict'
import { test } from 'node:test'
import type { NotificationRoute, RecipientCandidate } from '../types/api.ts'
import { candidateKey, candidateLabel, candidatesForNewRoute, describeCoverage } from './notificationRules.ts'

const user = (id: string, name: string, source: RecipientCandidate['source'] = 'org_admin'): RecipientCandidate => ({ user_id: id, name, email: `${name}@x.test`, source })
const contact = (id: string, name: string): RecipientCandidate => ({ contact_id: id, name, email: `${name}@musteri.test`, source: 'contact' })
const route = (over: Partial<NotificationRoute>): NotificationRoute => ({ id: 'r', channel: 'email', min_level: 'warning', created_at: '', ...over })

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
})

test('the coverage text warns that rules REPLACE the defaults', () => {
  assert.match(describeCoverage('organization', 0), /varsayılan alıcılar bilgilendirilir/)
  assert.match(describeCoverage('organization', 2), /YALNIZCA/)
  assert.match(describeCoverage('host', 1), /YALNIZCA.*organizasyon kuralları/)
  assert.match(describeCoverage('host', 0), /organizasyonun kuralları/)
})
