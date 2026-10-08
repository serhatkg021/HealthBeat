import assert from 'node:assert/strict'
import { test } from 'node:test'
import { alertLevelLabel, alertMetricLabel, alertStatusLabel, alertSubjectText, hostStatusLabel, containerStatusLabel } from './labels.ts'

test('known API values are shown in Turkish', () => {
  assert.equal(hostStatusLabel('online'), 'çevrimiçi')
  assert.equal(hostStatusLabel('offline'), 'çevrimdışı')
  assert.equal(alertLevelLabel('critical'), 'kritik')
  assert.equal(alertLevelLabel('warning'), 'uyarı')
  assert.deepEqual(['open', 'acknowledged', 'resolved'].map(alertStatusLabel), ['açık', 'onaylanmış', 'çözülmüş'])
  assert.deepEqual(['running', 'restarting', 'exited', 'paused', 'created', 'dead', 'removing'].map(containerStatusLabel), [
    'çalışıyor',
    'yeniden başlıyor',
    'durdu',
    'duraklatıldı',
    'oluşturuldu',
    'ölü',
    'kaldırılıyor',
  ])
})

test('alert metrics are labelled, including the ones that are not a plain metric name', () => {
  assert.deepEqual(['cpu', 'ram', 'disk', 'docker_restart', 'host_offline', 'disk_missing'].map(alertMetricLabel), [
    'CPU',
    'RAM',
    'Disk',
    'Docker restart',
    'Sunucu çevrimdışı',
    'Disk kayboldu',
  ])
  assert.equal(alertMetricLabel('something_new'), 'something_new')
})

test('an unknown value is shown as it is instead of an empty label', () => {
  assert.equal(hostStatusLabel('maintenance'), 'maintenance')
  assert.equal(containerStatusLabel('weird'), 'weird')
})

test('every protocol 4 alert type has a Turkish label', () => {
  const types = [
    'disk_latency',
    'temperature',
    'service_failed',
    'service_restart_loop',
    'container_unhealthy',
    'container_oom',
    'oom_kill',
    'fs_readonly',
    'raid_degraded',
    'time_sync',
    'reboot_required',
    'security_updates',
  ]
  const labels = types.map(alertMetricLabel)
  types.forEach((t, i) => assert.notEqual(labels[i], t, t))
  assert.equal(new Set(labels).size, types.length)
})

test('time_sync subjects are shown as the reason; other subjects as they are', () => {
  assert.equal(alertSubjectText('time_sync', 'unsynced'), 'saat senkron değil')
  assert.equal(alertSubjectText('time_sync', 'source'), 'saat kaynağı sorunlu')
  assert.equal(alertSubjectText('time_sync', 'offset'), 'saat farkı eşiği aştı')
  assert.equal(alertSubjectText('time_sync', 'yeni'), 'yeni')
  assert.equal(alertSubjectText('disk', 'unsynced'), 'unsynced')
  assert.equal(alertSubjectText('service_failed', 'nginx.service'), 'nginx.service')
})
