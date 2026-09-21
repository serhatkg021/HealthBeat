import assert from 'node:assert/strict'
import { test } from 'node:test'
import { fullTitle, pageTitle } from './pageTitle.ts'

test('static routes have a title; trailing slashes are ignored', () => {
  assert.equal(pageTitle('/'), 'Özet')
  assert.equal(pageTitle('/alerts'), 'Alert’ler')
  assert.equal(pageTitle('/audit/'), 'Denetim Kaydı')
  assert.equal(pageTitle('/login'), 'Giriş')
  assert.equal(pageTitle('/forgot-password'), 'Şifremi unuttum')
  assert.equal(pageTitle('/reset-password'), 'Yeni şifre')
})

test('detail routes get their title from the page (data), so the route table returns null', () => {
  assert.equal(pageTitle('/hosts/4c047b76-dcfa-40e4-8985-b02cfc7aae5d'), null)
  assert.equal(pageTitle('/organizations/22d546c3'), null)
  assert.equal(pageTitle('/olmayan'), null)
})

test('fullTitle appends the app name; no title means just the app name', () => {
  assert.equal(fullTitle('Eşikler'), 'Eşikler · HealthBeat')
  assert.equal(fullTitle('  web-1  '), 'web-1 · HealthBeat')
  assert.equal(fullTitle(''), 'HealthBeat')
  assert.equal(fullTitle(undefined), 'HealthBeat')
  assert.equal(fullTitle(null), 'HealthBeat')
})
