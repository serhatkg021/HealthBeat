import assert from 'node:assert/strict'
import { test } from 'node:test'
import type { Permission } from './auth/permissions.ts'
import { groupHasActive, inSettingsArea, navigation } from './navigation.ts'

const canOnly = (...allowed: Permission[]) => (p: Permission) => allowed.includes(p)
const ids = (items: { id: string }[]) => items.map((i) => i.id)

test('a super admin sees the summary, alerts, the management group and all three settings cards', () => {
  const nav = navigation(() => true)
  assert.deepEqual(ids(nav.top), ['ozet', 'alertler'])
  assert.deepEqual(ids(nav.groups), ['yonetim'])
  assert.deepEqual(ids(nav.groups[0].items), ['organizasyonlar', 'kullanicilar'])
  assert.deepEqual(ids(nav.settings), ['esikler', 'denetim', 'sistem'])
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
