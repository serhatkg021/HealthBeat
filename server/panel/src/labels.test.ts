import assert from 'node:assert/strict'
import { test } from 'node:test'
import { alertLevelLabel, alertMetricLabel, alertStatusLabel, hostStatusLabel, containerStatusLabel } from './labels.ts'

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
