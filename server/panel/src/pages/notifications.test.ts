import assert from 'node:assert/strict'
import { test } from 'node:test'
import { filterContacts, resolveRouteScope, type ContactRow } from './notifications.ts'

test('rules scope: organization by default, server on request; an operator only gets the server scope', () => {
  assert.deepEqual(resolveRouteScope(null, null, true), { kind: 'org', id: undefined })
  assert.deepEqual(resolveRouteScope('org', 'o1', true), { kind: 'org', id: 'o1' })
  assert.deepEqual(resolveRouteScope('sunucu', 'h1', true), { kind: 'sunucu', id: 'h1' })
  assert.deepEqual(resolveRouteScope('org', 'o1', false), { kind: 'sunucu', id: undefined })
  assert.deepEqual(resolveRouteScope('sunucu', 'h1', false), { kind: 'sunucu', id: 'h1' })
})

const row = (name: string, org: string, extra: Partial<ContactRow> = {}): ContactRow => ({
  id: name,
  organization_id: org,
  organizationName: org,
  name,
  created_at: '',
  updated_at: '',
  ...extra,
})

test('contacts are sorted by organization then name and searched across every field', () => {
  const rows = [row('Zeynep', 'Üretim', { email: 'zeynep@firma.com' }), row('Ali', 'Test', { department: 'DBA' }), row('Ayşe', 'Üretim', { phone: '+90 555' })]
  assert.deepEqual(filterContacts(rows, '').map((r) => r.name), ['Ali', 'Ayşe', 'Zeynep'])
  assert.deepEqual(filterContacts(rows, 'üretim').map((r) => r.name), ['Ayşe', 'Zeynep'])
  assert.deepEqual(filterContacts(rows, 'dba').map((r) => r.name), ['Ali'])
  assert.deepEqual(filterContacts(rows, 'firma.com').map((r) => r.name), ['Zeynep'])
  assert.deepEqual(filterContacts(rows, 'AYŞE üretim').map((r) => r.name), ['Ayşe'])
})
