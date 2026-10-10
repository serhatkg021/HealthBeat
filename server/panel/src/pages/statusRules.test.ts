import assert from 'node:assert/strict'
import { test } from 'node:test'
import type { HostStatusRuleView, StatusRuleConfig } from '../types/api.ts'
import { emptyDuration } from './duration.ts'
import { statusEffectiveRules } from './effectiveRules.ts'
import {
  STATUS_RULES,
  hostDrafts,
  inheritedForOrg,
  ruleChanges,
  scopeDrafts,
  settingOf,
  settingText,
  validateRuleDrafts,
} from './statusRules.ts'

const row = (rule: StatusRuleConfig['rule'], level: StatusRuleConfig['level'], org?: string, duration?: number): StatusRuleConfig => ({
  rule,
  level,
  organization_id: org,
  updated_at: '',
  ...(duration ? { duration_seconds: duration } : {}),
})

test('the rule list mirrors the server: eleven rules, three of them instant', () => {
  assert.deepEqual(
    STATUS_RULES.map((r) => r.rule),
    [
      'service_failed',
      'container_unhealthy',
      'container_oom',
      'oom_kill',
      'fs_readonly',
      'raid_degraded',
      'raid_rebuilding',
      'time_unsynced',
      'time_source',
      'reboot_required',
      'security_updates',
    ],
  )
  assert.deepEqual(
    STATUS_RULES.filter((r) => !r.duration).map((r) => r.rule),
    ['container_oom', 'fs_readonly', 'reboot_required'],
  )
})

test('each scope reads only its own rows', () => {
  const list = [row('service_failed', 'critical', undefined, 60), row('service_failed', 'off', 'o1'), row('oom_kill', 'warning', 'o2')]
  const global = scopeDrafts(list)
  assert.deepEqual(global.service_failed, { level: 'critical', duration: { value: '1', unit: 'dk' } })
  assert.equal(global.oom_kill.level, '')
  const o1 = scopeDrafts(list, 'o1')
  assert.equal(o1.service_failed.level, 'off')
  assert.equal(o1.oom_kill.level, '')
})

test('only changed rules are sent; clearing a rule sends null', () => {
  const saved = scopeDrafts([row('service_failed', 'critical', undefined, 60), row('reboot_required', 'info')])
  assert.deepEqual(ruleChanges(saved, saved), {})

  const draft = {
    ...saved,
    service_failed: { level: 'critical' as const, duration: { value: '60', unit: 'sn' as const } },
    reboot_required: { level: '' as const, duration: emptyDuration() },
    raid_degraded: { level: 'warning' as const, duration: { value: '5', unit: 'dk' as const } },
  }
  assert.deepEqual(ruleChanges(saved, { ...saved, service_failed: draft.service_failed }), {}, '60 sn = 1 dk')
  assert.deepEqual(ruleChanges(saved, draft), {
    reboot_required: null,
    raid_degraded: { level: 'warning', duration_seconds: 300 },
  })
})

test('a duration is sent only where it means something', () => {
  const d = { value: '5', unit: 'dk' as const }
  assert.deepEqual(settingOf('service_failed', { level: 'critical', duration: d }), { level: 'critical', duration_seconds: 300 })
  assert.deepEqual(settingOf('service_failed', { level: 'critical', duration: emptyDuration() }), { level: 'critical' }, 'empty = immediately')
  assert.deepEqual(settingOf('fs_readonly', { level: 'critical', duration: d }), { level: 'critical' }, 'instant rule')
  assert.deepEqual(settingOf('service_failed', { level: 'off', duration: d }), { level: 'off' }, 'off has no duration')
  assert.equal(settingOf('service_failed', { level: '', duration: d }), null)
})

test('a duration change on an inherited or disabled rule is not a change', () => {
  const saved = scopeDrafts([])
  const draft = { ...saved, service_failed: { level: '' as const, duration: { value: '5', unit: 'dk' as const } } }
  assert.deepEqual(ruleChanges(saved, draft), {})
})

test('invalid durations are reported per rule, only for enabled rules', () => {
  const drafts = scopeDrafts([])
  drafts.service_failed = { level: 'warning', duration: { value: '0', unit: 'dk' } }
  drafts.oom_kill = { level: 'off', duration: { value: 'x', unit: 'dk' } }
  assert.deepEqual(validateRuleDrafts(drafts), ['Servis çalışmıyor: Süre pozitif bir sayı olmalı (hemen için boş bırakın).'])
})

test('an organization inherits from the nearest parent with a row, then from the global rule', () => {
  const parents = new Map<string, string | undefined>([
    ['child', 'parent'],
    ['parent', 'root'],
    ['root', undefined],
  ])
  const list = [row('raid_degraded', 'critical'), row('raid_degraded', 'warning', 'root', 600), row('raid_degraded', 'off', 'child')]
  assert.deepEqual(inheritedForOrg(list, 'child', parents, 'raid_degraded'), {
    setting: { level: 'warning', duration_seconds: 600 },
    fromOrganizationId: 'root',
  }, 'its own row is not what it inherits')
  assert.deepEqual(inheritedForOrg(list, 'other', parents, 'raid_degraded'), { setting: { level: 'critical' } })
  assert.equal(inheritedForOrg(list, 'child', parents, 'oom_kill'), undefined)
})

test('settingText', () => {
  assert.equal(settingText('service_failed', { level: 'critical', duration_seconds: 120 }), 'Kritik · 2 dk boyunca')
  assert.equal(settingText('service_failed', { level: 'off', duration_seconds: 120 }), 'Kapalı')
  assert.equal(settingText('reboot_required', { level: 'info' }), 'Bilgi')
})

test('host drafts come from the custom settings; effective rows say where the value comes from', () => {
  const views: HostStatusRuleView[] = [
    { rule: 'service_failed', takes_duration: true, default: { level: 'warning' }, custom: { level: 'critical', duration_seconds: 60 } },
    { rule: 'raid_degraded', takes_duration: true, default: { level: 'critical', duration_seconds: 300 }, custom: null },
    { rule: 'reboot_required', takes_duration: false, default: null, custom: null },
  ]
  const d = hostDrafts(views)
  assert.equal(d.service_failed.level, 'critical')
  assert.equal(d.raid_degraded.level, '')
  assert.deepEqual(
    statusEffectiveRules(views).map((r) => [r.label, r.value, r.source]),
    [
      ['Servis çalışmıyor', 'Kritik · 1 dk boyunca', 'custom'],
      ['RAID bozuk', 'Kritik · 5 dk boyunca', 'inherited'],
      ['Yeniden başlatma gerekli', null, 'none'],
    ],
  )
})
