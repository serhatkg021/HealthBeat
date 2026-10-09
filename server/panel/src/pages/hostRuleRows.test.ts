import assert from 'node:assert/strict'
import { test } from 'node:test'
import type { HostServices, HostStatusRuleView, HostThresholdsResponse } from '../types/api.ts'
import { changedKeys, customizeItem, hostProblems, hostRow, hostRuleState, hostSavePlan, hostSummary, inheritItem, keepFailed, revertItem } from './hostRuleRows.ts'
import { addMount } from './thresholds.ts'
import { STATUS_RULES } from './statusRules.ts'
import { METRIC_TYPES } from './thresholds.ts'

const thresholds = (patch: Partial<HostThresholdsResponse> = {}): HostThresholdsResponse => ({
  thresholds: METRIC_TYPES.map((m) => ({ metric_type: m, default: null, custom: null })),
  mount_thresholds: [],
  container_thresholds: [],
  subject_thresholds: [],
  ...patch,
})

const status = (patch: Partial<Record<string, Partial<HostStatusRuleView>>> = {}): HostStatusRuleView[] =>
  STATUS_RULES.map((r) => ({ rule: r.rule, takes_duration: !!r.duration, default: null, custom: null, ...patch[r.rule] }))

const withMetric = (metric: string, view: object) =>
  thresholds({ thresholds: METRIC_TYPES.map((m) => (m === metric ? { metric_type: m, default: null, custom: null, ...view } : { metric_type: m, default: null, custom: null })) })

test('an inherited threshold shows its levels; a percent unit goes in front', () => {
  const state = hostRuleState(withMetric('cpu', { default: { warning_level: 80, critical_level: 90.5 } }), status(), null, null)
  const row = hostRow({ kind: 'threshold', metric: 'cpu' }, state)
  assert.deepEqual(
    row.pills.map((p) => p.text),
    ['uyarı %80', 'kritik %90,5'],
  )
  assert.equal(row.source, 'inherited')
  assert.equal(row.duration, 'anlık')
  assert.equal(row.on, true)
})

test('a host value wins over the inherited one and carries its duration', () => {
  const state = hostRuleState(
    withMetric('disk_latency', { default: { warning_level: 30, critical_level: 50 }, custom: { warning_level: 40, critical_level: 80, duration_seconds: 600 } }),
    status(),
    null,
    null,
  )
  const row = hostRow({ kind: 'threshold', metric: 'disk_latency' }, state)
  assert.deepEqual(
    row.pills.map((p) => p.text),
    ['uyarı 40 ms', 'kritik 80 ms'],
  )
  assert.equal(row.source, 'own')
  assert.equal(row.duration, '10 dk boyunca')
})

test('an undefined threshold is off, unless a subject has its own value', () => {
  const off = hostRow({ kind: 'threshold', metric: 'temperature' }, hostRuleState(thresholds(), status(), null, null))
  assert.equal(off.on, false)
  assert.equal(off.source, 'none')
  assert.equal(off.duration, null)
  assert.deepEqual(off.subjects, { label: 'Sensöre özel:', items: [] })

  const withSensor = hostRuleState(
    thresholds({ subject_thresholds: [{ metric_type: 'temperature', subject: 'nvme/Composite', custom: { warning_level: 70, critical_level: 80, duration_seconds: 120 } }] }),
    status(),
    null,
    null,
  )
  const row = hostRow({ kind: 'threshold', metric: 'temperature' }, withSensor)
  assert.equal(row.on, true)
  assert.equal(row.source, 'own')
  assert.deepEqual(
    row.pills.map((p) => p.text),
    ['yalnızca konuya özel değerler'],
  )
  assert.deepEqual(row.subjects?.items, [{ name: 'nvme/Composite', value: '70 / 80 °C · 2 dk' }])
})

test('mount and container values are listed under their thresholds, sorted', () => {
  const state = hostRuleState(
    thresholds({
      mount_thresholds: [
        { mount: '/srv', custom: { warning_level: 85, critical_level: 95 } },
        { mount: '/boot', custom: { warning_level: 90, critical_level: 95 } },
      ],
      container_thresholds: [{ container: 'web', custom: { warning_level: 3, critical_level: 10 } }],
    }),
    status(),
    null,
    null,
  )
  assert.deepEqual(
    hostRow({ kind: 'threshold', metric: 'disk' }, state).subjects?.items.map((i) => i.name),
    ['/boot', '/srv'],
  )
  assert.deepEqual(hostRow({ kind: 'threshold', metric: 'docker_restart' }, state).subjects, {
    label: 'Container’a özel:',
    items: [{ name: 'web', value: '3 / 10 restart' }],
  })
})

test('status rules: inherited level, own level, own off and undefined', () => {
  const state = hostRuleState(
    thresholds(),
    status({
      service_failed: { default: { level: 'critical', duration_seconds: 60 } },
      oom_kill: { custom: { level: 'warning', duration_seconds: 1800 } },
      raid_degraded: { default: { level: 'critical' }, custom: { level: 'off' } },
    }),
    null,
    null,
  )
  const failed = hostRow({ kind: 'status', rule: 'service_failed' }, state)
  assert.deepEqual([failed.pills[0].text, failed.duration, failed.source, failed.on], ['Kritik', '1 dk boyunca', 'inherited', true])
  // OOM'da süre, kapanma süresidir.
  const oom = hostRow({ kind: 'status', rule: 'oom_kill' }, state)
  assert.deepEqual([oom.pills[0].text, oom.duration, oom.source], ['Uyarı', '30 dk sonra kapanır', 'own'])
  const raid = hostRow({ kind: 'status', rule: 'raid_degraded' }, state)
  assert.deepEqual([raid.pills[0].text, raid.source, raid.on], ['Kapalı', 'own', false])
  const reboot = hostRow({ kind: 'status', rule: 'reboot_required' }, state)
  assert.deepEqual([reboot.pills[0].text, reboot.source, reboot.on, reboot.duration], ['tanımlı değil', 'none', false, null])
})

test('the disk selection: all, none, chosen mounts, or unknown when it could not be read', () => {
  const row = (disks: { all_mounts_alert: boolean; custom_alert_mounts: string[] } | null) =>
    hostRow({ kind: 'selection', key: 'disk_mounts' }, hostRuleState(thresholds(), status(), disks, null)).pills.map((p) => p.text)
  assert.deepEqual(row({ all_mounts_alert: true, custom_alert_mounts: ['/x'] }), ['Raporlanan tüm mount’lar'])
  assert.deepEqual(row({ all_mounts_alert: false, custom_alert_mounts: [] }), ['Hiçbiri: doluluk alert’i üretilmez'])
  assert.deepEqual(row({ all_mounts_alert: false, custom_alert_mounts: ['/srv', '/'] }), ['/', '/srv'])
  assert.deepEqual(row(null), ['bilinmiyor'])
})

test('watched services that are not reported are marked', () => {
  const services: HostServices = { services: [{ name: 'nginx.service', active: 'active', watched: true, updated_at: '' } as HostServices['services'][number]], watched: ['nginx.service', 'yok.service'] }
  const row = hostRow({ kind: 'selection', key: 'watched_services' }, hostRuleState(thresholds(), status(), null, services))
  assert.deepEqual(
    row.pills.map((p) => [p.text, p.tone]),
    [
      ['nginx.service', 'plain'],
      ['yok.service · raporlanmıyor', 'off'],
    ],
  )
  assert.equal(row.source, null)
})

test('automatic alerts are always on and are not counted', () => {
  const row = hostRow({ kind: 'auto', key: 'host_offline' }, hostRuleState(thresholds(), status(), null, null))
  assert.deepEqual([row.source, row.on, row.duration], ['always', false, 'aralığın 3 katı'])
})

test('the summary counts switched-on rules and the ones specific to this host', () => {
  const state = hostRuleState(
    thresholds({
      thresholds: METRIC_TYPES.map((m) =>
        m === 'cpu'
          ? { metric_type: m, default: { warning_level: 80, critical_level: 90 }, custom: null }
          : m === 'ram'
            ? { metric_type: m, default: null, custom: { warning_level: 80, critical_level: 95 } }
            : { metric_type: m, default: null, custom: null },
      ),
      mount_thresholds: [{ mount: '/srv', custom: { warning_level: 85, critical_level: 95 } }],
    }),
    status({ fs_readonly: { default: { level: 'critical' } } }),
    null,
    null,
  )
  const rows = [
    hostRow({ kind: 'threshold', metric: 'cpu' }, state),
    hostRow({ kind: 'threshold', metric: 'ram' }, state),
    hostRow({ kind: 'threshold', metric: 'disk' }, state),
    hostRow({ kind: 'status', rule: 'fs_readonly' }, state),
    hostRow({ kind: 'auto', key: 'host_offline' }, state),
  ]
  // disk: sunucu genelinde değer yok ama /srv'nin kendi değeri var.
  assert.deepEqual(hostSummary(rows), { on: 4, own: 2 })
})

const base = () =>
  hostRuleState(
    thresholds({
      thresholds: METRIC_TYPES.map((m) =>
        m === 'cpu' ? { metric_type: m, default: { warning_level: 80, critical_level: 90 }, custom: null } : { metric_type: m, default: null, custom: null },
      ),
      mount_thresholds: [{ mount: '/srv', custom: { warning_level: 85, critical_level: 95 } }],
    }),
    status({ service_failed: { default: { level: 'critical', duration_seconds: 60 } } }),
    { all_mounts_alert: true, custom_alert_mounts: [] },
    null,
  )

test('customizing a threshold starts from the inherited levels; inheriting drops the host value', () => {
  const saved = base()
  const custom = customizeItem({ kind: 'threshold', metric: 'cpu' }, saved)
  assert.deepEqual([custom.thresholds.cpu.mode, custom.thresholds.cpu.warning, custom.thresholds.cpu.critical], ['custom', '80', '90'])
  // Değer değişmeden özelleştirmek de bir değişikliktir (sunucuya kendi değeri yazılır).
  assert.deepEqual([...changedKeys(saved, custom)], ['threshold:cpu'])
  const back = inheritItem({ kind: 'threshold', metric: 'cpu' }, custom)
  assert.deepEqual([...changedKeys(saved, back)], [])
})

test('customizing a status rule starts from the inherited level, or warning when nothing is inherited', () => {
  const saved = base()
  const failed = customizeItem({ kind: 'status', rule: 'service_failed' }, saved).status.service_failed
  assert.deepEqual(failed, { level: 'critical', duration: { value: '1', unit: 'dk' } })
  assert.equal(customizeItem({ kind: 'status', rule: 'oom_kill' }, saved).status.oom_kill.level, 'warning')
  assert.equal(inheritItem({ kind: 'status', rule: 'oom_kill' }, customizeItem({ kind: 'status', rule: 'oom_kill' }, saved)).status.oom_kill.level, '')
})

test('changes are tracked per item, subject values count for their threshold, and revert restores one item', () => {
  const saved = base()
  let draft = customizeItem({ kind: 'status', rule: 'oom_kill' }, saved)
  draft = { ...draft, mounts: addMount(draft.mounts, '/boot', { warning_level: 90, critical_level: 95 }) }
  draft = { ...draft, disks: { mode: 'selected', selected: new Set(['/']) } }
  assert.deepEqual([...changedKeys(saved, draft)].sort(), ['selection:disk_mounts', 'status:oom_kill', 'threshold:disk'])
  const reverted = revertItem({ kind: 'threshold', metric: 'disk' }, saved, draft)
  assert.deepEqual([...changedKeys(saved, reverted)].sort(), ['selection:disk_mounts', 'status:oom_kill'])
})

test('the save plan sends only the parts that changed', () => {
  const saved = base()
  assert.deepEqual(hostSavePlan(saved, saved), {})

  const statusOnly = customizeItem({ kind: 'status', rule: 'oom_kill' }, saved)
  assert.deepEqual(hostSavePlan(saved, statusOnly), { status: { oom_kill: { level: 'warning' } } })

  const withMount = { ...saved, mounts: addMount(saved.mounts, '/boot', { warning_level: 90, critical_level: 95 }) }
  const plan = hostSavePlan(saved, withMount)
  assert.deepEqual(Object.keys(plan), ['thresholds'])
  assert.deepEqual(plan.thresholds?.parts, { mounts: { '/boot': { warning_level: 90, critical_level: 95 }, '/srv': { warning_level: 85, critical_level: 95 } } })
  assert.equal(plan.thresholds?.overrides.cpu, null)

  const removed = { ...saved, mounts: {} }
  assert.deepEqual(hostSavePlan(saved, removed).thresholds?.parts, { mounts: { '/srv': null } })

  const disks = { ...saved, disks: { mode: 'selected' as const, selected: new Set(['/srv', '/']) } }
  assert.deepEqual(hostSavePlan(saved, disks), { disks: { all_mounts_alert: false, custom_alert_mounts: ['/', '/srv'] } })
})

test('invalid values block saving with a prefixed message', () => {
  const saved = base()
  const custom = customizeItem({ kind: 'threshold', metric: 'ram' }, saved)
  assert.deepEqual(hostProblems(custom), ['RAM: Uyarı ve kritik seviyesi sayı olarak girilmeli.'])
  const bad = { ...saved, thresholds: { ...saved.thresholds, cpu: { ...saved.thresholds.cpu, mode: 'custom' as const, warning: '95', critical: '90' } } }
  assert.deepEqual(hostProblems(bad), ['CPU: Uyarı seviyesi kritik seviyeden büyük olamaz.'])
})

test('after a partial failure the failed part stays in the draft, the rest comes from the server', () => {
  const saved = base()
  const draft = { ...customizeItem({ kind: 'status', rule: 'oom_kill' }, saved), disks: { mode: 'selected' as const, selected: new Set(['/']) } }
  // Disk seçimi kaydedildi (server'dan yeni hâli geldi), durum kuralı kaydedilemedi.
  const fresh = { ...saved, disks: draft.disks }
  const next = keepFailed(fresh, draft, new Set(['status']))
  assert.deepEqual([...changedKeys(fresh, next)], ['status:oom_kill'])
})
