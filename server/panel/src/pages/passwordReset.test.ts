import assert from 'node:assert/strict'
import { test } from 'node:test'
import { plausibleEmail, tokenFromHash, validateResetPassword } from './passwordReset.ts'

const TOKEN = 'k3Jf9_aB-0zQxLmN2pRsTuVwXyZ1234567890abcdef'.slice(0, 43)

test('tokenFromHash reads the token from the URL fragment', () => {
  assert.equal(tokenFromHash(`#token=${TOKEN}`), TOKEN)
  assert.equal(tokenFromHash(`token=${TOKEN}`), TOKEN, 'a hash without the leading # is tolerated')
  assert.equal(tokenFromHash(`#foo=1&token=${TOKEN}`), TOKEN)
})

test('tokenFromHash returns an empty string for anything that is not a well-formed token', () => {
  assert.equal(tokenFromHash(''), '')
  assert.equal(tokenFromHash('#'), '')
  assert.equal(tokenFromHash('#token='), '')
  assert.equal(tokenFromHash('#token=short'), '')
  assert.equal(tokenFromHash(`#token=${TOKEN}<script>`), '')
  assert.equal(tokenFromHash(`#token=${'a'.repeat(201)}`), '')
  assert.equal(tokenFromHash(`#other=${TOKEN}`), '')
  assert.equal(tokenFromHash(`#token=${'a'.repeat(19)}`), '')
  assert.equal(tokenFromHash(`#token=${'a'.repeat(20)}`), 'a'.repeat(20), '20 characters is the lower bound')
  assert.equal(tokenFromHash(`#token=${'a'.repeat(200)}`), 'a'.repeat(200), '200 characters is the upper bound')
})

test('plausibleEmail mirrors the server: shape only, case and outer whitespace ignored', () => {
  for (const ok of ['u@x.test', ' U@X.Test ', 'a.b+c@sub.example.com']) assert.equal(plausibleEmail(ok), true, ok)
  for (const bad of ['', '   ', 'no-at-sign', '@x.test', 'u@', 'a b@x.test', '<u@x.test>', 'u@x.test\r\nBcc: e@x.test']) {
    assert.equal(plausibleEmail(bad), false, JSON.stringify(bad))
  }
  assert.equal(plausibleEmail(`${'a'.repeat(250)}@x.co`), false, 'longer than 254 characters')
  assert.equal(plausibleEmail(`${'a'.repeat(248)}@x.co`), true, 'exactly 254 characters')
})

test('validateResetPassword: length in characters, byte cap, and confirmation', () => {
  assert.equal(validateResetPassword('a-brand-new-passphrase', 'a-brand-new-passphrase'), null)
  assert.match(validateResetPassword('short', 'short') ?? '', /en az 12 karakter/)
  assert.equal(validateResetPassword('123456789012', '123456789012'), null, '12 characters is enough')
  assert.match(validateResetPassword('12345678901', '12345678901') ?? '', /en az 12/)
  // 12 karakter ama çok baytlı: uzunluk karakterle sayılır.
  assert.equal(validateResetPassword('ğüşiöçĞÜŞİÖÇ', 'ğüşiöçĞÜŞİÖÇ'), null)
  // Emoji: 6 karakter ama 12 UTF-16 birimi; uzunluk karakterle sayılır (Go'nun RuneCountInString'i gibi).
  assert.match(validateResetPassword('😀'.repeat(6), '😀'.repeat(6)) ?? '', /en az 12 karakter/)
  assert.equal(validateResetPassword('😀'.repeat(12), '😀'.repeat(12)), null)
  assert.match(validateResetPassword('a'.repeat(73), 'a'.repeat(73)) ?? '', /en çok 72 bayt/)
  assert.equal(validateResetPassword('a'.repeat(72), 'a'.repeat(72)), null, '72 bytes is allowed')
  assert.match(validateResetPassword('a-brand-new-passphrase', 'a-brand-new-passphrasE') ?? '', /eşleşmiyor/)
})
