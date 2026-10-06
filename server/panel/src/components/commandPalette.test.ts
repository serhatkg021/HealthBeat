import assert from 'node:assert/strict'
import { test } from 'node:test'
import { navigation } from '../navigation.ts'
import { pageItems, searchPalette, type PaletteItem } from './commandPalette.ts'

const hosts: PaletteItem[] = Array.from({ length: 12 }, (_, i) => ({
  key: `host:${i}`,
  group: 'Sunucular' as const,
  label: `web-${String(i).padStart(2, '0')}`,
  hint: 'Üretim',
  to: `/hosts/${i}`,
  keywords: `10.0.0.${i}`,
}))
const orgs: PaletteItem[] = [{ key: 'org:1', group: 'Organizasyonlar', label: 'Üretim', to: '/organizations/1' }]

test('pages follow permissions: an operator gets no users, settings or system tools', () => {
  const operator = pageItems(navigation((p) => ['host.view', 'alert.view', 'threshold.view', 'notification.view'].includes(p)), false)
  const labels = operator.map((p) => p.label)
  assert.ok(labels.includes('Sunucular'))
  assert.ok(labels.includes('Bildirim kuralları'))
  assert.ok(!labels.includes('Kullanıcılar'))
  assert.ok(!labels.includes('Ayarlar'))
  assert.ok(!labels.includes('Denetim Kaydı'))
  assert.ok(!labels.includes('Sunucu ekle'))
  const admin = pageItems(navigation(() => true), true).map((p) => p.label)
  assert.ok(admin.includes('Denetim Kaydı') && admin.includes('Ayarlar') && admin.includes('Sunucu ekle'))
})

test('an empty query lists only pages; typing searches every group in order, capped per group', () => {
  const pages = pageItems(navigation(() => true), true)
  const all = [...hosts, ...orgs, ...pages]
  assert.ok(searchPalette(all, '').every((i) => i.group === 'Sayfalar'))
  const web = searchPalette(all, 'web')
  assert.equal(web.filter((i) => i.group === 'Sunucular').length, 8)
  assert.deepEqual(searchPalette(all, '10.0.0.11').map((i) => i.key), ['host:11'])
  const uretim = searchPalette(all, 'üretim').map((i) => i.group)
  assert.equal(uretim[0], 'Sunucular')
  assert.ok(uretim.includes('Organizasyonlar'))
  assert.deepEqual(searchPalette(all, 'denetim').map((i) => i.label), ['Denetim Kaydı'])
})
