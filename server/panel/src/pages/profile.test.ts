import assert from 'node:assert/strict'
import { test } from 'node:test'
import { profilePatch, validateProfile } from './profile.ts'

test('a name and a phone in the accepted form are valid; both may be empty', () => {
  assert.equal(validateProfile('Ayşe Yılmaz', '+90 (555) 000-00.00'), null)
  assert.equal(validateProfile('', ''), null)
  assert.equal(validateProfile('  ', '  '), null)
})

test('letters in the phone and over-long values are rejected with a message', () => {
  assert.match(validateProfile('Ayşe', '0555 abc') ?? '', /Telefon yalnızca/)
  assert.match(validateProfile('Ayşe', '1'.repeat(33)) ?? '', /Telefon çok uzun/)
  assert.match(validateProfile('a'.repeat(201), '') ?? '', /Ad çok uzun/)
  assert.equal(validateProfile(` ${'a'.repeat(200)} `, ''), null, 'surrounding spaces do not count')
})

test('only changed fields are sent, trimmed; nothing changed means no request', () => {
  const user = { full_name: 'Ayşe', phone: undefined }
  assert.equal(profilePatch(user, 'Ayşe', ''), null)
  assert.equal(profilePatch(user, ' Ayşe ', '  '), null)
  assert.deepEqual(profilePatch(user, 'Ayşe Yılmaz', ''), { full_name: 'Ayşe Yılmaz' })
  assert.deepEqual(profilePatch(user, 'Ayşe', ' 0555 '), { phone: '0555' })
})

test('emptying a field sends an empty string so the server clears it', () => {
  assert.deepEqual(profilePatch({ full_name: 'Ayşe', phone: '0555' }, '', ''), { full_name: '', phone: '' })
})
