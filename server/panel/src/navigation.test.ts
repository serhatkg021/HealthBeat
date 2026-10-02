import assert from 'node:assert/strict'
import { test } from 'node:test'
import type { Permission } from './auth/permissions.ts'
import { groupHasActive, inSettingsArea, inToolsArea, navigation } from './navigation.ts'

const canOnly = (...allowed: Permission[]) => (p: Permission) => allowed.includes(p)
const ids = (items: { id: string }[]) => items.map((i) => i.id)

test('a super admin sees the summary, alerts, the management group and all three settings cards', () => {
  const nav = navigation(() => true)
  assert.deepEqual(ids(nav.top), ['ozet', 'alertler'])
  assert.deepEqual(ids(nav.groups), ['yonetim'])
  assert.deepEqual(ids(nav.groups[0].items), ['organizasyonlar', 'kullanicilar'])
  assert.deepEqual(ids(nav.settings), ['esikler', 'denetim', 'sistem'])
  assert.deepEqual(ids(nav.tools), ['kuyruk', 'cache'])
})

test('system tools show only the tabs the user has a permission for; with none the menu entry disappears', () => {
  assert.deepEqual(ids(navigation(canOnly('system.queue.view')).tools), ['kuyruk'])
  assert.deepEqual(ids(navigation(canOnly('system.cache.view')).tools), ['cache'])
  // Ayarları görebilmek Sistem Araçları'nı açmaz; her aracın kendi izni vardır.
  assert.deepEqual(navigation(canOnly('settings.view', 'settings.manage', 'audit.view')).tools, [])
  assert.deepEqual(navigation(() => false).tools, [])
})

test('the system tools area is its own page, not part of settings', () => {
  assert.equal(inToolsArea('/system'), true)
  assert.equal(inToolsArea('/systems'), false)
  assert.equal(inSettingsArea('/system'), false)
  assert.equal(inToolsArea('/settings/system'), false)
})

test('an operator gets "my hosts" instead of organizations; an empty group disappears', () => {
  const nav = navigation(canOnly('alert.view', 'threshold.view'))
  assert.deepEqual(ids(nav.top), ['ozet', 'sunucularim', 'alertler'])
  assert.deepEqual(nav.groups, [])
  assert.deepEqual(ids(nav.settings), ['esikler'])
})

test('with no permissions only the summary and "my hosts" remain and there is no settings entry', () => {
  const nav = navigation(() => false)
  assert.deepEqual(ids(nav.top), ['ozet', 'sunucularim'])
  assert.deepEqual(nav.settings, [])
})

test('a group keeps only the items the user may see', () => {
  const nav = navigation(canOnly('organization.view'))
  assert.deepEqual(ids(nav.groups[0].items), ['organizasyonlar'])
})

test('the settings area covers the hub and the pages opened from it', () => {
  for (const p of ['/settings', '/settings/system', '/thresholds', '/audit']) assert.equal(inSettingsArea(p), true, p)
  for (const p of ['/', '/alerts', '/users', '/settingsx', '/auditor']) assert.equal(inSettingsArea(p), false, p)
})

test('a group is active on its pages and their detail pages', () => {
  const group = navigation(() => true).groups[0]
  assert.equal(groupHasActive(group, '/organizations'), true)
  assert.equal(groupHasActive(group, '/organizations/22d546c3'), true)
  assert.equal(groupHasActive(group, '/users'), true)
  assert.equal(groupHasActive(group, '/alerts'), false)
  assert.equal(groupHasActive(group, '/'), false)
})
