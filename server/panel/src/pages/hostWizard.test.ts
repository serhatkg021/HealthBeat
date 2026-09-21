import assert from 'node:assert/strict'
import { test } from 'node:test'
import { buildCreateRequest, emptyWizardDraft, looksLikeIP, STEPS, summarize, validateAll, validateStep, type WizardDraft } from './hostWizard.ts'
import { defaultDrafts } from './thresholds.ts'

const valid = (over: Partial<WizardDraft> = {}): WizardDraft => ({ ...emptyWizardDraft(), title: 'web-1', ip: '10.0.0.5', ...over })

test('steps come in the order server, disks, thresholds, summary', () => {
  assert.deepEqual(STEPS.map((s) => s.id), ['server', 'disks', 'thresholds', 'summary'])
})

test('a fresh wizard: defaults everywhere, nothing custom, server step incomplete', () => {
  const d = emptyWizardDraft()
  assert.equal(d.diskMode, 'all')
  assert.deepEqual(d.thresholds, defaultDrafts())
  assert.ok(validateStep('server', d).length > 0)
  assert.deepEqual(validateStep('disks', d), [])
  assert.deepEqual(validateStep('thresholds', d), [])
})

test('IP check accepts IPv4/IPv6 and rejects hostnames and out-of-range octets', () => {
  for (const ok of ['10.0.0.5', '192.168.1.255', '::1', 'fe80::1', '2001:db8::8a2e:370:7334', ' 10.0.0.5 ']) assert.equal(looksLikeIP(ok), true, ok)
  for (const bad of ['', 'web-1', '10.0.0', '10.0.0.256', '10.0.0.5.6', 'a:b', '10.0.0.x', '01.2.3.4']) assert.equal(looksLikeIP(bad), false, bad)
})

test('server step: required fields, interval and pull-only fields', () => {
  assert.deepEqual(validateStep('server', valid()), [])
  assert.equal(validateStep('server', valid({ title: '  ' })).length, 1)
  assert.equal(validateStep('server', valid({ ip: 'nope' })).length, 1)
  for (const interval of ['0', '-5', '1.5', 'abc', '']) assert.equal(validateStep('server', valid({ intervalSeconds: interval })).length, 1, interval)
  // pull alanları yalnızca pull modunda denetlenir
  assert.deepEqual(validateStep('server', valid({ pullPort: 'garbage', pullEndpoint: '' })), [])
  assert.equal(validateStep('server', valid({ mode: 'pull', pullPort: '70000' })).length, 1)
  assert.equal(validateStep('server', valid({ mode: 'pull', pullPort: '0' })).length, 1)
  assert.equal(validateStep('server', valid({ mode: 'pull', pullEndpoint: ' ' })).length, 1)
  assert.deepEqual(validateStep('server', valid({ mode: 'pull' })), [])
})

test('disk step only validates the paths when specific disks are chosen', () => {
  assert.deepEqual(validateStep('disks', valid({ diskMode: 'all', diskMounts: 'not a path' })), [])
  assert.equal(validateStep('disks', valid({ diskMode: 'selected', diskMounts: '/, data' })).length, 1)
  assert.deepEqual(validateStep('disks', valid({ diskMode: 'selected', diskMounts: '/, /data\n/mnt/My Disk' })), [])
  assert.deepEqual(validateStep('disks', valid({ diskMode: 'selected', diskMounts: '' })), [], 'an empty selection is a valid choice: no disk alerts')
})

test('threshold step reports problems and validateAll points at the right step', () => {
  const d = valid({ thresholds: { ...defaultDrafts(), cpu: { mode: 'custom', warning: '90', critical: '80' } } })
  assert.equal(validateStep('thresholds', d).length, 1)
  const all = validateAll({ ...d, ip: '' })
  assert.deepEqual(Object.keys(all).sort(), ['server', 'thresholds'])
  assert.deepEqual(validateAll(valid()), {})
})

test('request: all disks and all defaults send no disk selection and no thresholds', () => {
  assert.deepEqual(buildCreateRequest('org-1', valid()), {
    organization_id: 'org-1',
    title: 'web-1',
    ip: '10.0.0.5',
    mode: 'push',
    interval_seconds: 30,
  })
})

test('request: selected disks are sent, and an empty selection is sent as [] (not omitted)', () => {
  const two = buildCreateRequest('o', valid({ diskMode: 'selected', diskMounts: '/data, /, /data' }))
  assert.equal(two.all_mounts_alert, false)
  assert.deepEqual(two.custom_alert_mounts, ['/data', '/'])
  const none = buildCreateRequest('o', valid({ diskMode: 'selected', diskMounts: '' }))
  assert.equal(none.all_mounts_alert, false, 'omitting it would mean "all disks"')
  assert.deepEqual(none.custom_alert_mounts, [])
  assert.ok('custom_alert_mounts' in none)
})

test('request: pull fields only in pull mode; trimmed text; numbers as numbers', () => {
  const push = buildCreateRequest('o', valid({ pullPort: '1234' }))
  assert.equal('pull_port' in push, false)
  const pull = buildCreateRequest('o', valid({ mode: 'pull', title: ' db ', ip: ' 10.0.0.9 ', pullPort: '9200', pullEndpoint: ' /health ', intervalSeconds: '15' }))
  assert.equal(pull.title, 'db')
  assert.equal(pull.ip, '10.0.0.9')
  assert.equal(pull.pull_port, 9200)
  assert.equal(pull.pull_endpoint, '/health')
  assert.equal(pull.interval_seconds, 15)
})

test('request: only metrics with custom values are sent as thresholds', () => {
  const d = valid({ thresholds: { ...defaultDrafts(), cpu: { mode: 'custom', warning: '70', critical: '85' }, ram: { mode: 'default', warning: '1', critical: '2' } } })
  assert.deepEqual(buildCreateRequest('o', d).thresholds, { cpu: { warning_level: 70, critical_level: 85 } })
})

test('summary has one section per step, in step order, with the entered values', () => {
  const d = valid({
    mode: 'pull',
    diskMode: 'selected',
    diskMounts: '/, /data',
    thresholds: { ...defaultDrafts(), cpu: { mode: 'custom', warning: '70', critical: '85' } },
  })
  const s = summarize(d, { ram: { warning_level: 80, critical_level: 90 } })
  assert.deepEqual(s.map((x) => x.step), ['server', 'disks', 'thresholds'])
  assert.ok(s[0].lines.some((l) => l.includes('web-1')) && s[0].lines.some((l) => l.includes('10.0.0.5')) && s[0].lines.some((l) => l.includes('Pull')))
  assert.ok(s[1].lines[0].includes('/, /data'))
  assert.equal(s[2].lines.length, 4)
  assert.ok(s[2].lines.find((l) => l.startsWith('CPU'))?.includes('Özel'))
  assert.ok(s[2].lines.find((l) => l.startsWith('RAM'))?.includes('Varsayılan: uyarı 80'))
  assert.ok(s[2].lines.find((l) => l.startsWith('Disk'))?.includes('alert üretilmez'), 'a metric without any default says it raises no alerts')
})

test('summary spells out the three disk situations', () => {
  const lines = (over: Partial<WizardDraft>) => summarize(valid(over), {})[1].lines[0]
  assert.match(lines({ diskMode: 'all' }), /tüm diskler/)
  assert.match(lines({ diskMode: 'selected', diskMounts: '/data' }), /\/data/)
  assert.match(lines({ diskMode: 'selected', diskMounts: '' }), /kapalı/)
})

test('mount thresholds: validated in the thresholds step, sent with the request, listed in the summary', () => {
  const d = valid({ mountThresholds: { '/storage': { warning: '90', critical: '95' }, '/': { warning: '70', critical: '90' } } })
  assert.deepEqual(validateStep('thresholds', d), [])
  assert.deepEqual(buildCreateRequest('o', d).mount_thresholds, {
    '/storage': { warning_level: 90, critical_level: 95 },
    '/': { warning_level: 70, critical_level: 90 },
  })
  const lines = summarize(d, {})[2].lines
  assert.equal(lines.length, 6, 'four metrics plus one line per mount')
  assert.ok(lines[2].startsWith('Disk —') && lines[3].startsWith('Disk / —') && lines[4].startsWith('Disk /storage —'))

  const broken = valid({ mountThresholds: { '/storage': { warning: '95', critical: '90' } } })
  assert.equal(validateStep('thresholds', broken).length, 1)
  assert.deepEqual(Object.keys(validateAll(broken)), ['thresholds'])
  assert.equal('mount_thresholds' in buildCreateRequest('o', valid()), false, 'nothing custom: the field is not sent')
})

test('summary warns about a mount threshold for a disk that is not selected for alerts', () => {
  const d = valid({ diskMode: 'selected', diskMounts: '/', mountThresholds: { '/': { warning: '70', critical: '90' }, '/storage': { warning: '90', critical: '95' } } })
  const lines = summarize(d, {})[2].lines.filter((l) => l.startsWith('Disk /'))
  assert.equal(lines.length, 2)
  assert.ok(!lines[0].includes('uyarı: bu disk'), '/ is selected: no warning')
  assert.ok(lines[1].includes('uyarı: bu disk'), '/storage is not selected: it will never alert')
  const all = summarize({ ...d, diskMode: 'all' }, {})[2].lines.filter((l) => l.startsWith('Disk /'))
  assert.ok(all.every((l) => !l.includes('uyarı: bu disk')), 'with "all disks" every mount can alert')
})

test('the disk summary mentions the missing-disk alert only when specific disks are chosen', () => {
  const lines = (over: Partial<WizardDraft>) => summarize(valid(over), {})[1].lines
  assert.ok(lines({ diskMode: 'selected', diskMounts: '/data' }).some((l) => l.includes('disk kayboldu')))
  assert.ok(!lines({ diskMode: 'all' }).some((l) => l.includes('kayboldu')))
  assert.ok(!lines({ diskMode: 'selected', diskMounts: '' }).some((l) => l.includes('kayboldu')))
})

test('container thresholds: validated in the thresholds step, sent with the request, listed in the summary', () => {
  const d = valid({ containerThresholds: { web: { warning: '3', critical: '10' }, batch: { warning: '50', critical: '100' } } })
  assert.deepEqual(validateStep('thresholds', d), [])
  assert.deepEqual(buildCreateRequest('o', d).container_thresholds, {
    web: { warning_level: 3, critical_level: 10 },
    batch: { warning_level: 50, critical_level: 100 },
  })
  const lines = summarize(d, {})[2].lines
  assert.equal(lines.length, 6, 'four metrics plus one line per container')
  assert.ok(lines[3].startsWith('Docker restart —') && lines[4].startsWith('Docker restart batch —') && lines[5].startsWith('Docker restart web —'))

  const broken = valid({ containerThresholds: { web: { warning: '10', critical: '3' } } })
  assert.equal(validateStep('thresholds', broken).length, 1)
  assert.deepEqual(Object.keys(validateAll(broken)), ['thresholds'])
  assert.equal('container_thresholds' in buildCreateRequest('o', valid()), false, 'nothing custom: the field is not sent')
  assert.equal('mount_thresholds' in buildCreateRequest('o', d), false, 'container thresholds must not appear as mount thresholds')
})
