import assert from 'node:assert/strict'
import { test } from 'node:test'
import { validateNewPassword } from './password.ts'

const ok = 'a-long-enough-password'

test('accepts a valid change', () => {
  assert.equal(validateNewPassword('old-password-1', ok, ok), null)
})

test('requires the current password', () => {
  assert.match(validateNewPassword('', ok, ok) ?? '', /Mevcut/)
})

test('enforces the 12-character minimum by characters, not bytes', () => {
  assert.match(validateNewPassword('old', 'elevenchars', 'elevenchars') ?? '', /en az 12/)
  assert.equal(validateNewPassword('old', 'twelve-chars', 'twelve-chars'), null)
  assert.equal(validateNewPassword('old', 'ş'.repeat(12), 'ş'.repeat(12)), null) // 12 karakter, 24 bayt
  // BMP dışındaki bir karakter tek karakterdir (iki UTF-16 birimi), server'daki gibi.
  assert.match(validateNewPassword('old', '😀'.repeat(11), '😀'.repeat(11)) ?? '', /en az 12/)
  assert.equal(validateNewPassword('old', '😀'.repeat(12), '😀'.repeat(12)), null)
})

test('enforces the 72-byte maximum in bytes', () => {
  assert.equal(validateNewPassword('old', 'a'.repeat(72), 'a'.repeat(72)), null)
  assert.match(validateNewPassword('old', 'a'.repeat(73), 'a'.repeat(73)) ?? '', /72 bayt/)
  assert.match(validateNewPassword('old', 'ş'.repeat(37), 'ş'.repeat(37)) ?? '', /72 bayt/) // 37 karakter = 74 bayt
})

test('rejects an unchanged password and a mismatched confirmation', () => {
  assert.match(validateNewPassword(ok, ok, ok) ?? '', /farklı/)
  assert.match(validateNewPassword('old', ok, ok + 'x') ?? '', /eşleşmiyor/)
})
