import assert from 'node:assert/strict'
import { test } from 'node:test'
import { agoText, certExpiry, certNames, expiresInText, hiddenKeys, keyKindLabel, limiterInfo, limiterRate, usedOf } from './cache.ts'

test('usedOf: spent tokens over the capacity, rounded up so one request is never "0"', () => {
  assert.deepEqual(usedOf({ remaining: 4 }, 5), { used: 1, pct: 20, text: '1 / 5' })
  assert.deepEqual(usedOf({ remaining: 4.6 }, 5), { used: 1, pct: 20, text: '1 / 5' })
  assert.deepEqual(usedOf({ remaining: 0.2 }, 5), { used: 5, pct: 100, text: '5 / 5' })
  assert.deepEqual(usedOf({ remaining: -1 }, 5), { used: 5, pct: 100, text: '5 / 5' })
  assert.deepEqual(usedOf({ remaining: 0 }, 0), { used: 0, pct: 0, text: '0 / 0' })
})

test('limiterRate', () => {
  assert.equal(limiterRate({ enabled: true, per_minute: 10, burst: 20 }), 'dakikada 10, kapasite 20')
  assert.equal(limiterRate({ enabled: true, per_minute: 0.1, burst: 3 }), 'dakikada 0.1, kapasite 3')
  assert.equal(limiterRate({ enabled: false, per_minute: 0, burst: 20 }), 'kapalı')
})

test('unknown limiters and key kinds are shown as they come', () => {
  assert.equal(limiterInfo('login_failures').title, 'Başarısız girişler')
  assert.deepEqual(limiterInfo('yeni_sinir'), { title: 'yeni_sinir', description: '' })
  assert.equal(keyKindLabel('host'), 'Sunucu')
  assert.equal(keyKindLabel('token'), 'Anahtar')
})

test('hiddenKeys counts what the server did not list', () => {
  assert.equal(hiddenKeys({ keys: 250, entries: new Array(200).fill({ key: 'k', label: '', remaining: 0, last_used_at: '' }) }), 50)
  assert.equal(hiddenKeys({ keys: 2, entries: [] }), 2)
  assert.equal(hiddenKeys({ keys: 0, entries: [] }), 0)
})

test('expiresInText and agoText', () => {
  const now = Date.parse('2026-10-02T12:00:00Z')
  assert.equal(expiresInText('2026-10-02T12:00:42Z', now), '42 sn')
  assert.equal(expiresInText('2026-10-02T11:59:59Z', now), 'süresi doldu')
  assert.equal(expiresInText('bozuk', now), 'süresi doldu')
  assert.equal(agoText('2026-10-02T11:48:00Z', now), '12 dk önce')
  assert.equal(agoText('bozuk', now), '—')
})

test('certExpiry warns as the certificate gets close to its end', () => {
  const now = Date.parse('2026-10-02T12:00:00Z')
  assert.deepEqual(certExpiry({ not_after: '2027-10-02T12:00:00Z' }, now), { text: '365 gün kaldı', tone: 'good' })
  assert.deepEqual(certExpiry({ not_after: '2026-10-27T12:00:00Z' }, now), { text: '25 gün kaldı', tone: 'warning' })
  assert.deepEqual(certExpiry({ not_after: '2026-10-10T12:00:00Z' }, now), { text: '8 gün kaldı', tone: 'critical' })
  assert.deepEqual(certExpiry({ not_after: '2026-10-02T15:00:00Z' }, now), { text: '3 sa kaldı', tone: 'critical' })
  assert.deepEqual(certExpiry({ not_after: '2026-10-01T12:00:00Z' }, now), { text: 'süresi doldu', tone: 'critical' })
})

test('certNames joins DNS names and IPs', () => {
  assert.deepEqual(certNames({ dns_names: ['localhost', 'server'], ips: ['127.0.0.1'] }), ['localhost', 'server', '127.0.0.1'])
  assert.deepEqual(certNames({ dns_names: [], ips: [] }), [])
})
