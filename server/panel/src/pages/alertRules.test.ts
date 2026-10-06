import assert from 'node:assert/strict'
import { test } from 'node:test'
import { resolveScope } from './alertRules.ts'

test('the scope comes from the address; anything unknown is the system default', () => {
  assert.deepEqual(resolveScope(null, null, true), { kind: 'sistem' })
  assert.deepEqual(resolveScope('yok', 'x', true), { kind: 'sistem' })
  assert.deepEqual(resolveScope('org', 'o1', true), { kind: 'org', id: 'o1' })
  assert.deepEqual(resolveScope('sunucu', 'h1', false), { kind: 'sunucu', id: 'h1' })
  assert.deepEqual(resolveScope('sunucu', '  ', true), { kind: 'sunucu', id: undefined })
})

test('someone who cannot see organizations cannot open the organization scope', () => {
  assert.deepEqual(resolveScope('org', 'o1', false), { kind: 'sistem' })
})
