import assert from 'node:assert/strict'
import { test } from 'node:test'
import type { HostService } from '../types/api.ts'
import { enabledLabel, filterServices, isProblem, notReported, parseServiceName, serviceCounts, serviceState, toggleWatched } from './services.ts'

const svc = (name: string, active: string, sub: string, extra: Partial<HostService> = {}): HostService => ({
  name,
  active,
  sub,
  updated_at: '',
  watched: false,
  ...extra,
})

const LIST = [
  svc('cron.service', 'active', 'running', { description: 'Regular background program processing daemon' }),
  svc('postgresql.service', 'failed', 'failed', { watched: true, restarts: 3 }),
  svc('redis-server.service', 'activating', 'auto-restart'),
  svc('apt-daily.service', 'inactive', 'dead'),
  svc('nginx.service', 'inactive', 'dead', { watched: true }),
  svc('setup.service', 'active', 'exited'),
]

test('serviceState reads active and sub', () => {
  assert.deepEqual(serviceState(LIST[0]), { label: 'çalışıyor', tone: 'good' })
  assert.deepEqual(serviceState(LIST[1]), { label: 'hata', tone: 'critical' })
  assert.deepEqual(serviceState(LIST[2]), { label: 'yeniden başlatılıyor', tone: 'warning' })
  assert.deepEqual(serviceState(LIST[3]), { label: 'durmuş', tone: 'neutral' })
  assert.deepEqual(serviceState(LIST[5]), { label: 'tamamlandı', tone: 'neutral' })
  assert.deepEqual(serviceState({ active: 'weird', sub: '' }), { label: 'weird', tone: 'neutral' })
})

test('a problem is a failure, a restart loop or a watched service that is not running', () => {
  assert.deepEqual(
    LIST.filter(isProblem).map((s) => s.name),
    ['postgresql.service', 'redis-server.service', 'nginx.service'],
  )
})

test('filterServices: problems first, then by name; search covers the description', () => {
  assert.deepEqual(
    filterServices(LIST, { q: '', problemsOnly: false, watchedOnly: false }).map((s) => s.name),
    ['nginx.service', 'postgresql.service', 'redis-server.service', 'apt-daily.service', 'cron.service', 'setup.service'],
  )
  assert.deepEqual(filterServices(LIST, { q: 'BACKGROUND', problemsOnly: false, watchedOnly: false }).map((s) => s.name), ['cron.service'])
  assert.deepEqual(filterServices(LIST, { q: '', problemsOnly: false, watchedOnly: true }).map((s) => s.name), ['nginx.service', 'postgresql.service'])
  assert.deepEqual(filterServices(LIST, { q: 'redis', problemsOnly: true, watchedOnly: false }).map((s) => s.name), ['redis-server.service'])
})

test('watched services that are not in the list are reported separately', () => {
  assert.deepEqual(notReported(['nginx.service', 'old.service', 'a.service'], LIST), ['a.service', 'old.service'])
})

test('toggleWatched keeps the selection sorted and unique', () => {
  assert.deepEqual(toggleWatched(['b.service'], 'a.service', true), ['a.service', 'b.service'])
  assert.deepEqual(toggleWatched(['a.service', 'b.service'], 'a.service', true), ['a.service', 'b.service'])
  assert.deepEqual(toggleWatched(['a.service', 'b.service'], 'a.service', false), ['b.service'])
})

test('parseServiceName completes a bare name and checks it like the server', () => {
  assert.deepEqual(parseServiceName(' nginx ', []), { name: 'nginx.service' })
  assert.deepEqual(parseServiceName('backup.timer', []), { name: 'backup.timer' })
  assert.ok('error' in parseServiceName('', []))
  assert.ok('error' in parseServiceName('nginx', ['nginx.service']))
  assert.ok('error' in parseServiceName('a\u0001b', []))
  assert.ok('error' in parseServiceName('x'.repeat(300), []))
  assert.ok('error' in parseServiceName('new', Array.from({ length: 256 }, (_, i) => `s${i}.service`)))
})

test('counts and labels', () => {
  assert.deepEqual(serviceCounts(LIST, ['nginx.service', 'postgresql.service', 'gone.service']), { total: 6, running: 1, problems: 3, watched: 3 })
  assert.equal(enabledLabel('enabled'), 'evet')
  assert.equal(enabledLabel('static'), 'bağımlılıkla')
  assert.equal(enabledLabel(undefined), '—')
  assert.equal(enabledLabel('something'), 'something')
})
