import assert from 'node:assert/strict'
import { test } from 'node:test'
import type { Permission } from './auth/permissions.ts'
import { LEGACY_SETTINGS_ROUTES, alertRulesPath, groupHasActive, inSettingsArea, inToolsArea, navigation, settingsTabPath } from './navigation.ts'

const canOnly = (...allowed: Permission[]) => (p: Permission) => allowed.includes(p)
const ids = (items: { id: string }[]) => items.map((i) => i.id)
const group = (nav: ReturnType<typeof navigation>, id: string) => nav.groups.find((g) => g.id === id)

test('a super admin sees the monitoring, alert management and management groups and all three settings tabs', () => {
  const nav = navigation(() => true)
  assert.deepEqual(ids(nav.groups), ['izleme', 'alert-yonetimi', 'yonetim'])
  assert.deepEqual(ids(group(nav, 'izleme')!.items), ['ozet', 'sunucular', 'alertler'])
  assert.deepEqual(ids(group(nav, 'alert-yonetimi')!.items), ['kurallar', 'bakim', 'bildirim'])
  assert.deepEqual(ids(group(nav, 'yonetim')!.items), ['organizasyonlar', 'kullanicilar'])
  assert.deepEqual(ids(nav.settings), ['denetim', 'sistem'])
  assert.deepEqual(ids(nav.tools), ['kuyruk', 'cache', 'log'])
})

test('system tools show only the tabs the user has a permission for; with none the menu entry disappears', () => {
  assert.deepEqual(ids(navigation(canOnly('system.queue.view')).tools), ['kuyruk'])
  assert.deepEqual(ids(navigation(canOnly('system.cache.view')).tools), ['cache'])
  assert.deepEqual(ids(navigation(canOnly('system.logs.view')).tools), ['log'])
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

test('an operator gets "my hosts" instead of organizations and read-only alert management; an empty group disappears', () => {
  const nav = navigation(canOnly('host.view', 'alert.view', 'threshold.view', 'notification.view'))
  assert.deepEqual(ids(nav.groups), ['izleme', 'alert-yonetimi'])
  assert.deepEqual(ids(group(nav, 'izleme')!.items), ['ozet', 'sunucularim', 'alertler'])
  assert.deepEqual(ids(group(nav, 'alert-yonetimi')!.items), ['kurallar', 'bakim', 'bildirim'])
  // Eşikler artık Ayarlar'da değil, Alert kuralları sayfasında: operatörün Ayarlar girişi yok.
  assert.deepEqual(nav.settings, [])
})

test('an organization admin sees the host list and notifications but no users or settings', () => {
  const nav = navigation(canOnly('organization.view', 'host.view', 'threshold.view', 'alert.view', 'notification.view', 'contact.view'))
  assert.deepEqual(ids(group(nav, 'izleme')!.items), ['ozet', 'sunucular', 'alertler'])
  assert.deepEqual(ids(group(nav, 'yonetim')!.items), ['organizasyonlar'])
  assert.deepEqual(nav.settings, [])
})

test('notifications are reachable with either notification or settings permission', () => {
  assert.ok(ids(group(navigation(canOnly('settings.view')), 'alert-yonetimi')!.items).includes('bildirim'))
  assert.ok(!ids(group(navigation(() => false), 'alert-yonetimi')!.items).includes('bildirim'))
})

test('with no permissions only the summary, "my hosts" and maintenance remain and there is no settings entry', () => {
  const nav = navigation(() => false)
  assert.deepEqual(ids(nav.groups), ['izleme', 'alert-yonetimi'])
  assert.deepEqual(ids(group(nav, 'izleme')!.items), ['ozet', 'sunucularim'])
  assert.deepEqual(ids(group(nav, 'alert-yonetimi')!.items), ['bakim'])
  assert.deepEqual(nav.settings, [])
})

test('a group keeps only the items the user may see', () => {
  const nav = navigation(canOnly('organization.view'))
  assert.deepEqual(ids(group(nav, 'yonetim')!.items), ['organizasyonlar'])
})

test('the settings area is the settings page', () => {
  assert.equal(inSettingsArea('/settings'), true)
  for (const p of ['/', '/alerts', '/users', '/settingsx', '/auditor']) assert.equal(inSettingsArea(p), false, p)
})

test('the old separate pages map to settings tabs that exist', () => {
  const tabs = ids(navigation(() => true).settings)
  for (const [path, tab] of Object.entries(LEGACY_SETTINGS_ROUTES)) assert.ok(tabs.includes(tab), `${path} -> ${tab}`)
  assert.equal(settingsTabPath('sistem'), '/settings?sekme=sistem')
})

test('a group is active on its pages and their detail pages', () => {
  const nav = navigation(() => true)
  const yonetim = group(nav, 'yonetim')!
  assert.equal(groupHasActive(yonetim, '/organizations'), true)
  assert.equal(groupHasActive(yonetim, '/organizations/22d546c3'), true)
  assert.equal(groupHasActive(yonetim, '/users'), true)
  assert.equal(groupHasActive(yonetim, '/alerts'), false)
  assert.equal(groupHasActive(yonetim, '/'), false)
  // Özet yalnızca tam eşleşmede etkin: başka her sayfa "/" ile başlar.
  const izleme = group(nav, 'izleme')!
  assert.equal(groupHasActive(izleme, '/'), true)
  assert.equal(groupHasActive(izleme, '/hosts'), true)
  assert.equal(groupHasActive(izleme, '/hosts/4c047b76'), true)
  assert.equal(groupHasActive(izleme, '/organizations'), false)
})

test('the alert rules address carries the scope; the system scope is the bare page', () => {
  assert.equal(alertRulesPath(), '/alert-rules')
  assert.equal(alertRulesPath({ kind: 'sistem' }), '/alert-rules')
  assert.equal(alertRulesPath({ kind: 'org', id: 'o1' }), '/alert-rules?kapsam=org&id=o1')
  assert.equal(alertRulesPath({ kind: 'sunucu', id: 'h1' }), '/alert-rules?kapsam=sunucu&id=h1')
  assert.equal(alertRulesPath({ kind: 'sunucu' }), '/alert-rules?kapsam=sunucu')
})
