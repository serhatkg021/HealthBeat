import assert from 'node:assert/strict'
import { test } from 'node:test'
import type { HostThresholdView, ThresholdConfig } from '../types/api.ts'
import {
  customOnly,
  defaultDrafts,
  defaultsFromServer,
  describeDraft,
  draftsFromServer,
  defaultSource,
  effectiveDefaults,
  isDirty,
  orgChain,
  toOverrides,
  validateDraft,
  validateDrafts,
  withMode,
  type Drafts,
} from './thresholds.ts'

const custom = (warning: string, critical: string) => ({ mode: 'custom' as const, warning, critical })

function row(metric: ThresholdConfig['metric_type'], w: number, c: number, scope: { org?: string } = {}): ThresholdConfig {
  return { id: `${metric}${scope.org ?? ''}`, metric_type: metric, warning_level: w, critical_level: c, organization_id: scope.org, created_at: '', updated_at: '' }
}

test('a new server starts with every metric on default and sends nothing custom', () => {
  const d = defaultDrafts()
  assert.deepEqual(customOnly(d), {})
  assert.deepEqual(toOverrides(d), { cpu: null, ram: null, disk: null, docker_restart: null })
  assert.deepEqual(validateDrafts(d), [])
})

test('custom values are sent as numbers, default metrics as null', () => {
  const d: Drafts = { ...defaultDrafts(), cpu: custom('70', '85.5'), docker_restart: custom('2', '5') }
  assert.deepEqual(toOverrides(d), {
    cpu: { warning_level: 70, critical_level: 85.5 },
    ram: null,
    disk: null,
    docker_restart: { warning_level: 2, critical_level: 5 },
  })
  assert.deepEqual(customOnly(d), { cpu: { warning_level: 70, critical_level: 85.5 }, docker_restart: { warning_level: 2, critical_level: 5 } })
})

test('a value typed under "custom" and then switched back to default is not sent', () => {
  const d: Drafts = { ...defaultDrafts(), cpu: { mode: 'default', warning: '10', critical: '20' } }
  assert.deepEqual(customOnly(d), {})
  assert.equal(toOverrides(d).cpu, null, 'null is what removes a stored custom value')
})

test('validation follows the server rules', () => {
  const bad: [string, ReturnType<typeof custom>][] = [
    ['empty', custom('', '')],
    ['half a pair', custom('50', '')],
    ['not a number', custom('abc', '90')],
    ['warning above critical', custom('90', '80')],
    ['negative', custom('-1', '50')],
    ['percent above 100', custom('50', '101')],
  ]
  for (const [name, d] of bad) assert.notEqual(validateDraft('cpu', d), null, name)
  assert.equal(validateDraft('cpu', custom('80', '80')), null, 'equal levels are allowed')
  assert.equal(validateDraft('cpu', custom('0', '100')), null)
  assert.equal(validateDraft('docker_restart', custom('3', '1000000')), null)
  assert.notEqual(validateDraft('docker_restart', custom('3', '1000001')), null)
  assert.equal(validateDraft('docker_restart', custom('3', '500')), null, 'restart counts are not limited to 100')
  assert.equal(validateDraft('ram', { mode: 'default', warning: 'garbage', critical: '' }), null, 'a metric on default is never invalid')
})

test('validateDrafts names the metric that has the problem', () => {
  const errors = validateDrafts({ ...defaultDrafts(), ram: custom('90', '80') })
  assert.equal(errors.length, 1)
  assert.match(errors[0], /^RAM:/)
})

test('draftsFromServer marks exactly the metrics that have custom values', () => {
  const views: HostThresholdView[] = [
    { metric_type: 'cpu', default: { warning_level: 80, critical_level: 90 }, custom: { warning_level: 20, critical_level: 30 } },
    { metric_type: 'ram', default: { warning_level: 80, critical_level: 90 }, custom: null },
    { metric_type: 'disk', default: null, custom: null },
    { metric_type: 'docker_restart', default: null, custom: { warning_level: 1, critical_level: 2 } },
  ]
  const d = draftsFromServer(views)
  assert.deepEqual(d.cpu, custom('20', '30'))
  assert.equal(d.ram.mode, 'default')
  assert.equal(d.disk.mode, 'default')
  assert.deepEqual(d.docker_restart, custom('1', '2'))
  assert.deepEqual(defaultsFromServer(views), { cpu: { warning_level: 80, critical_level: 90 }, ram: { warning_level: 80, critical_level: 90 } })
})

test('effectiveDefaults: the organization value beats the global one, other organizations are ignored', () => {
  const list = [row('cpu', 80, 90), row('ram', 70, 85), row('cpu', 50, 60, { org: 'A' }), row('ram', 1, 2, { org: 'B' })]
  assert.deepEqual(effectiveDefaults(list, 'A'), { cpu: { warning_level: 50, critical_level: 60 }, ram: { warning_level: 70, critical_level: 85 } })
  assert.deepEqual(effectiveDefaults(list, 'B'), { cpu: { warning_level: 80, critical_level: 90 }, ram: { warning_level: 1, critical_level: 2 } })
  assert.deepEqual(effectiveDefaults(list, undefined), { cpu: { warning_level: 80, critical_level: 90 }, ram: { warning_level: 70, critical_level: 85 } })
  assert.deepEqual(effectiveDefaults([list[2], list[0]], 'A').cpu, { warning_level: 50, critical_level: 60 }, 'row order must not matter')
})

test('effectiveDefaults inherits from the nearest ancestor, never from a child or a sibling', () => {
  // holding ─┬─ acme ── acme-ist ; holding ── beta
  const parents = new Map<string, string | undefined>([['holding', undefined], ['acme', 'holding'], ['ist', 'acme'], ['beta', 'holding']])
  const list = [
    row('cpu', 90, 99), // global
    row('cpu', 85, 95, { org: 'holding' }),
    row('ram', 60, 80, { org: 'holding' }),
    row('cpu', 75, 90, { org: 'acme' }),
    row('disk', 1, 2, { org: 'beta' }), // kardeş dal: sızmamalı
    row('docker_restart', 3, 6, { org: 'ist' }), // alt dal: üst şirkete sızmamalı
  ]
  assert.deepEqual(effectiveDefaults(list, 'ist', parents), {
    cpu: { warning_level: 75, critical_level: 90 }, // acme'nin değeri holding'inkinden yakın
    ram: { warning_level: 60, critical_level: 80 }, // yalnızca holding tanımlamış
    docker_restart: { warning_level: 3, critical_level: 6 },
  })
  assert.deepEqual(effectiveDefaults(list, 'holding', parents), { cpu: { warning_level: 85, critical_level: 95 }, ram: { warning_level: 60, critical_level: 80 } })
  assert.deepEqual(effectiveDefaults(list, 'beta', parents).cpu, { warning_level: 85, critical_level: 95 })
  // Zincir bilinmiyorsa (parents boş) yalnızca kendisi ve global bakılır.
  assert.deepEqual(effectiveDefaults(list, 'ist').cpu, { warning_level: 90, critical_level: 99 })
})

test('defaultSource says where the value comes from', () => {
  const parents = new Map<string, string | undefined>([['holding', undefined], ['acme', 'holding']])
  const list = [row('cpu', 90, 99), row('ram', 60, 80, { org: 'holding' })]
  assert.deepEqual(defaultSource(list, 'acme', parents, 'ram'), { levels: { warning_level: 60, critical_level: 80 }, fromOrganizationId: 'holding' })
  assert.deepEqual(defaultSource(list, 'acme', parents, 'cpu'), { levels: { warning_level: 90, critical_level: 99 } })
  assert.equal(defaultSource(list, 'acme', parents, 'disk'), undefined)
})

test('orgChain walks to the root and survives a cycle', () => {
  assert.deepEqual(orgChain('c', new Map([['c', 'b'], ['b', 'a'], ['a', undefined]])), ['c', 'b', 'a'])
  assert.deepEqual(orgChain('a', new Map([['a', 'b'], ['b', 'a']])), ['a', 'b']) // döngü sonsuz döngüye yol açmaz
  assert.deepEqual(orgChain(undefined, new Map()), [])
})

test('describeDraft says which value applies, and that a missing default means no alerts', () => {
  const defaults = { cpu: { warning_level: 80, critical_level: 90 } }
  assert.equal(describeDraft('cpu', defaultDrafts().cpu, defaults), 'Varsayılan: uyarı 80 % / kritik 90 %')
  assert.equal(describeDraft('cpu', custom('70', '85'), defaults), 'Özel: uyarı 70 % / kritik 85 %')
  assert.match(describeDraft('ram', defaultDrafts().ram, defaults), /alert üretilmez/)
  assert.equal(describeDraft('docker_restart', custom('3', '6'), {}), 'Özel: uyarı 3 restart / kritik 6 restart')
})

test('switching to custom starts from the default values, and only when nothing was typed yet', () => {
  const defaults = { cpu: { warning_level: 80, critical_level: 90 } }
  const d = withMode(defaultDrafts(), 'cpu', 'custom', defaults)
  assert.deepEqual(d.cpu, custom('80', '90'))
  assert.equal(withMode(d, 'cpu', 'custom', defaults), d, 'no change when already custom')

  const typed = withMode({ ...defaultDrafts(), cpu: { mode: 'default', warning: '1', critical: '2' } }, 'cpu', 'custom', defaults)
  assert.deepEqual(typed.cpu, custom('1', '2'), 'earlier input is kept, not overwritten by the default')

  assert.deepEqual(withMode(defaultDrafts(), 'ram', 'custom', defaults).ram, custom('', ''), 'no default to start from: empty fields')
  assert.equal(withMode(d, 'cpu', 'default', defaults).cpu.mode, 'default')
})

test('isDirty compares mode and, for custom metrics only, the values', () => {
  const a: Drafts = { ...defaultDrafts(), cpu: custom('70', '85') }
  assert.equal(isDirty(a, { ...a }), false)
  assert.equal(isDirty(a, { ...a, cpu: custom('70', '86') }), true)
  assert.equal(isDirty(a, { ...a, cpu: { mode: 'default', warning: '70', critical: '85' } }), true)
  assert.equal(
    isDirty({ ...defaultDrafts(), ram: { mode: 'default', warning: '1', critical: '2' } }, defaultDrafts()),
    false,
    'leftover text under "default" is not a change',
  )
})

// ---- mount başına disk eşikleri ------------------------------------------------------------

import {
  addMount,
  customMounts,
  describeMounts,
  isMountsDirty,
  mountDraftsFromServer,
  parseMountToAdd,
  removeMount,
  serverLevels,
  toMountOverrides,
  validateMountDrafts,
} from './thresholds.ts'

test('mount drafts come from the server list and go back as numbers', () => {
  const d = mountDraftsFromServer([
    { mount: '/', custom: { warning_level: 70, critical_level: 90 } },
    { mount: '/storage', custom: { warning_level: 90, critical_level: 95 } },
  ])
  assert.deepEqual(d, { '/': { warning: '70', critical: '90' }, '/storage': { warning: '90', critical: '95' } })
  assert.deepEqual(customMounts(d), { '/': { warning_level: 70, critical_level: 90 }, '/storage': { warning_level: 90, critical_level: 95 } })
})

test('a removed mount is sent as null so it goes back to the server value; kept ones carry their values', () => {
  const saved = { '/': { warning: '70', critical: '90' }, '/storage': { warning: '90', critical: '95' } }
  const draft = { '/': { warning: '75', critical: '90' }, '/data': { warning: '1', critical: '2' } }
  assert.deepEqual(toMountOverrides(saved, draft), {
    '/storage': null,
    '/': { warning_level: 75, critical_level: 90 },
    '/data': { warning_level: 1, critical_level: 2 },
  })
  assert.deepEqual(toMountOverrides({}, {}), {})
})

test('mount validation reuses the disk rules and names the mount', () => {
  assert.deepEqual(validateMountDrafts({ '/': { warning: '70', critical: '90' } }), [])
  const errors = validateMountDrafts({
    '/a': { warning: '90', critical: '80' },
    '/b': { warning: '', critical: '' },
    '/c': { warning: '50', critical: '101' },
    '/ok': { warning: '1', critical: '2' },
  })
  assert.equal(errors.length, 3)
  assert.ok(errors.every((e) => e.startsWith('Disk /')))
  assert.ok(errors.some((e) => e.startsWith('Disk /a:')) && errors.some((e) => e.startsWith('Disk /b:')) && errors.some((e) => e.startsWith('Disk /c:')))
  const many: Record<string, { warning: string; critical: string }> = {}
  for (let i = 0; i < 65; i++) many[`/m${i}`] = { warning: '1', critical: '2' }
  assert.ok(validateMountDrafts(many).length > 0, 'more than 64 mounts is rejected like the server does')
})

test('isMountsDirty notices added, removed and edited mounts', () => {
  const a = { '/': { warning: '70', critical: '90' } }
  assert.equal(isMountsDirty(a, { '/': { warning: '70', critical: '90' } }), false)
  assert.equal(isMountsDirty(a, { '/': { warning: '71', critical: '90' } }), true)
  assert.equal(isMountsDirty(a, {}), true)
  assert.equal(isMountsDirty({}, a), true)
  assert.equal(isMountsDirty({}, {}), false)
})

test('adding a mount starts from the server disk values, removing it leaves the others alone', () => {
  const start = addMount({}, '/storage', { warning_level: 85, critical_level: 95 })
  assert.deepEqual(start, { '/storage': { warning: '85', critical: '95' } })
  assert.deepEqual(addMount({}, '/x'), { '/x': { warning: '', critical: '' } })
  const two = addMount(start, '/', { warning_level: 1, critical_level: 2 })
  assert.deepEqual(Object.keys(removeMount(two, '/storage')), ['/'])
  assert.deepEqual(Object.keys(two).sort(), ['/', '/storage'], 'removeMount must not change its input')
})

test('the path typed into "add a mount" is checked', () => {
  const existing = { '/data': { warning: '1', critical: '2' } }
  assert.deepEqual(parseMountToAdd(' /storage ', existing), { mount: '/storage' })
  assert.deepEqual(parseMountToAdd('/mnt/My Disk', existing), { mount: '/mnt/My Disk' })
  const withControlChar = '/a' + String.fromCharCode(1) + 'b'
  for (const bad of ['', '   ', 'storage', './x', '/data', withControlChar]) assert.ok('error' in parseMountToAdd(bad, existing), JSON.stringify(bad))
})

test('a new mount starts from the values the server actually uses: custom if valid, else the default', () => {
  const defaults = { disk: { warning_level: 85, critical_level: 95 } }
  assert.deepEqual(serverLevels('disk', defaultDrafts().disk, defaults), { warning_level: 85, critical_level: 95 })
  assert.deepEqual(serverLevels('disk', custom('60', '80'), defaults), { warning_level: 60, critical_level: 80 })
  assert.equal(serverLevels('disk', custom('90', '80'), defaults), undefined, 'an invalid custom value is not offered as a starting point')
  assert.equal(serverLevels('disk', defaultDrafts().disk, {}), undefined)
})

test('mount summary lines are sorted and say the values', () => {
  assert.deepEqual(describeMounts({ '/storage': { warning: '90', critical: '95' }, '/': { warning: '70', critical: '90' } }), [
    'Disk / — Özel: uyarı 70 % / kritik 90 %',
    'Disk /storage — Özel: uyarı 90 % / kritik 95 %',
  ])
})

// ---- container başına docker_restart eşikleri ---------------------------------------------

import {
  containerDraftsFromServer,
  describeContainers,
  parseContainerToAdd,
  validateContainerDrafts,
} from './thresholds.ts'

test('container drafts come from the server list and are validated with the restart rules', () => {
  const d = containerDraftsFromServer([
    { container: 'web', custom: { warning_level: 3, critical_level: 10 } },
    { container: 'batch', custom: { warning_level: 50, critical_level: 500 } },
  ])
  assert.deepEqual(d, { web: { warning: '3', critical: '10' }, batch: { warning: '50', critical: '500' } })
  assert.deepEqual(validateContainerDrafts(d), [], 'restart counts are not percentages: 500 is fine')
  const errors = validateContainerDrafts({
    a: { warning: '9', critical: '3' },
    b: { warning: '', critical: '' },
    c: { warning: '1', critical: '1000001' },
    ok: { warning: '1', critical: '2' },
  })
  assert.equal(errors.length, 3)
  assert.ok(errors.every((e) => e.startsWith('Docker restart ')))
})

test('the name typed into "add a container" is checked', () => {
  const existing = { web: { warning: '1', critical: '2' } }
  assert.deepEqual(parseContainerToAdd(' my-app_1.worker ', existing), { mount: 'my-app_1.worker' })
  const withControlChar = 'a' + String.fromCharCode(1) + 'b'
  for (const bad of ['', '   ', 'web', withControlChar, 'x'.repeat(256)]) assert.ok('error' in parseContainerToAdd(bad, existing), JSON.stringify(bad))
  assert.ok(!('error' in parseContainerToAdd('x'.repeat(255), existing)), '255 bytes is allowed')
  const many: Record<string, { warning: string; critical: string }> = {}
  for (let i = 0; i < 64; i++) many[`c${i}`] = { warning: '1', critical: '2' }
  assert.ok('error' in parseContainerToAdd('one-more', many), 'the 65th container is refused')
})

test('container summary lines are sorted and use the restart unit', () => {
  assert.deepEqual(describeContainers({ web: { warning: '3', critical: '10' }, batch: { warning: '50', critical: '100' } }), [
    'Docker restart batch — Özel: uyarı 50 restart / kritik 100 restart',
    'Docker restart web — Özel: uyarı 3 restart / kritik 10 restart',
  ])
})

test('a new container starts from the values the server uses for docker_restart', () => {
  const defaults = { docker_restart: { warning_level: 3, critical_level: 10 } }
  assert.deepEqual(serverLevels('docker_restart', defaultDrafts().docker_restart, defaults), { warning_level: 3, critical_level: 10 })
  assert.deepEqual(serverLevels('docker_restart', custom('1', '2'), defaults), { warning_level: 1, critical_level: 2 })
  assert.equal(serverLevels('docker_restart', defaultDrafts().docker_restart, {}), undefined)
})

test('more than 64 containers with their own threshold is rejected like the server does', () => {
  const many: Record<string, { warning: string; critical: string }> = {}
  for (let i = 0; i < 65; i++) many[`c${i}`] = { warning: '1', critical: '2' }
  assert.ok(validateContainerDrafts(many).length > 0)
})
