import assert from 'node:assert/strict'
import { test } from 'node:test'
import {
  agentHint,
  agentKind,
  agentBadgeInfo,
  compareVersions,
  needsUpdate,
  noPolicy,
  supportsHardwareSummary,
  unsupportedFieldsNotice,
  type AgentPolicy,
} from './agentStatus.ts'

const policy: AgentPolicy = { latest: '1.5.0', min: '1.2.0' }

test('compareVersions: numeric, not lexicographic', () => {
  assert.equal(compareVersions('1.10.0', '1.9.0'), 1)
  assert.equal(compareVersions('1.9.0', '1.10.0'), -1)
  assert.equal(compareVersions('2.0.0', '1.99.99'), 1)
  assert.equal(compareVersions('1.2.3', '1.2.3'), 0)
  assert.equal(compareVersions('1.2.4', '1.2.3'), 1)
})

test('compareVersions: prerelease is lower than its release; build metadata is ignored', () => {
  assert.equal(compareVersions('1.2.0-rc.1', '1.2.0'), -1)
  assert.equal(compareVersions('1.2.0', '1.2.0-rc.1'), 1)
  assert.equal(compareVersions('1.2.0-rc.1', '1.2.0-rc.1'), 0)
  assert.equal(compareVersions('1.2.0-alpha', '1.2.0-beta'), -1)
  assert.equal(compareVersions('1.2.0+build.5', '1.2.0'), 0)
})

test('compareVersions: an unparsable version is never judged older or newer', () => {
  assert.equal(compareVersions('dev', '1.0.0'), 0)
  assert.equal(compareVersions('1.0.0', ''), 0)
  assert.equal(compareVersions('1.0', '1.0.0'), 0)
})

test('agentKind: legacy, unknown, current, outdated, unsupported', () => {
  assert.equal(agentKind({}, policy), 'unknown') // henüz hiç rapor yok
  assert.equal(agentKind({ agent_protocol: 1 }, policy), 'legacy')
  assert.equal(agentKind({ agent_version: null, agent_protocol: 1 }, policy), 'legacy')
  assert.equal(agentKind({ agent_protocol: 2 }, policy), 'unknown') // protokol var ama sürüm okunamadı
  assert.equal(agentKind({ agent_version: '1.5.0', agent_protocol: 2 }, policy), 'current')
  assert.equal(agentKind({ agent_version: '1.6.0', agent_protocol: 2 }, policy), 'current') // güncelden yeni
  assert.equal(agentKind({ agent_version: '1.4.9', agent_protocol: 2 }, policy), 'outdated')
  assert.equal(agentKind({ agent_version: '1.2.0', agent_protocol: 2 }, policy), 'outdated') // min'e eşit: hâlâ desteklenir
  assert.equal(agentKind({ agent_version: '1.1.9', agent_protocol: 2 }, policy), 'unsupported')
})

test('agentKind: unsupported takes precedence over outdated', () => {
  assert.equal(agentKind({ agent_version: '0.9.0', agent_protocol: 2 }, policy), 'unsupported')
})

test('agentKind: without a policy nothing versioned is ever outdated or unsupported', () => {
  assert.equal(agentKind({ agent_version: '0.0.1', agent_protocol: 2 }, noPolicy), 'current')
  assert.equal(agentKind({ agent_version: '0.0.1', agent_protocol: 2 }, { latest: '9.0.0', min: '' }), 'outdated')
  assert.equal(agentKind({ agent_version: '0.0.1', agent_protocol: 2 }, { latest: '', min: '1.0.0' }), 'unsupported')
})

test('needsUpdate', () => {
  for (const k of ['legacy', 'outdated', 'unsupported'] as const) assert.equal(needsUpdate(k), true, k)
  for (const k of ['current', 'unknown'] as const) assert.equal(needsUpdate(k), false, k)
})

test('agentBadgeInfo: text is only the version; the status is carried by tone and title', () => {
  const policy = { latest: '1.3.0', min: '1.0.0' }
  const current = agentBadgeInfo({ agent_version: '1.3.0', agent_protocol: 3 }, policy)
  assert.deepEqual(current, { text: 'v1.3.0', tone: 'good', title: 'Güncel' })

  const outdated = agentBadgeInfo({ agent_version: '1.2.0', agent_protocol: 2 }, policy)
  assert.equal(outdated.text, 'v1.2.0')
  assert.equal(outdated.tone, 'warning')
  assert.match(outdated.title, /^Güncelleme var\. .*1\.3\.0/)

  const unsupported = agentBadgeInfo({ agent_version: '0.9.0', agent_protocol: 2 }, policy)
  assert.equal(unsupported.tone, 'critical')
  assert.match(unsupported.title, /^Desteklenmiyor\. .*1\.0\.0/)
})

test('agentBadgeInfo: without a version a legacy agent says so, an unreported one shows a dash', () => {
  const legacy = agentBadgeInfo({ agent_protocol: 1 }, noPolicy)
  assert.equal(legacy.text, 'eski agent')
  assert.equal(legacy.tone, 'warning')
  assert.match(legacy.title, /^Eski agent\. /)

  const none = agentBadgeInfo({}, noPolicy)
  assert.equal(none.text, '—')
  assert.equal(none.tone, 'neutral')
  assert.match(none.title, /Sürüm bilgisi yok/)
})

test('agentBadgeInfo: with no policy a versioned agent is current (never judged old)', () => {
  const info = agentBadgeInfo({ agent_version: '0.0.1', agent_protocol: 2 }, noPolicy)
  assert.equal(info.tone, 'good')
})

test('agentHint mentions the version to move to; nothing for current/unknown', () => {
  assert.match(agentHint('outdated', policy), /1\.5\.0/)
  assert.match(agentHint('unsupported', policy), /1\.2\.0/)
  assert.match(agentHint('legacy', policy), /1\.5\.0/)
  assert.match(agentHint('legacy', noPolicy), /eski sürüm/)
  assert.equal(agentHint('current', policy), '')
  assert.equal(agentHint('unknown', policy), '')
})

test('supportsHardwareSummary: protocol 2 and above', () => {
  assert.equal(supportsHardwareSummary({}), false)
  assert.equal(supportsHardwareSummary({ agent_protocol: 1 }), false)
  assert.equal(supportsHardwareSummary({ agent_protocol: 2 }), true)
  assert.equal(supportsHardwareSummary({ agent_protocol: 3 }), true)
})

test('unsupportedFieldsNotice lists the field names, and is empty when there are none', () => {
  assert.equal(unsupportedFieldsNotice(undefined), '')
  assert.equal(unsupportedFieldsNotice(null), '')
  assert.equal(unsupportedFieldsNotice([]), '')
  const msg = unsupportedFieldsNotice(['gpu', 'load_average'])
  assert.match(msg, /gpu, load_average/)
  assert.match(msg, /server'ı güncelleyin/)
})
