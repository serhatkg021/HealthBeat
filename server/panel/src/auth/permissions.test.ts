import { test } from 'node:test'
import assert from 'node:assert/strict'
import { hasPermission } from './permissions.ts'

test('izin listesinde olan izin var sayılır', () => {
  assert.equal(hasPermission({ permissions: ['host.view', 'host.update'] }, 'host.update'), true)
})

test('listede olmayan izin yok sayılır', () => {
  assert.equal(hasPermission({ permissions: ['host.view'] }, 'host.delete'), false)
})

test('izinler henüz yüklenmediyse hiçbir izin yoktur', () => {
  assert.equal(hasPermission({}, 'host.view'), false)
  assert.equal(hasPermission(null, 'host.view'), false)
  assert.equal(hasPermission(undefined, 'host.view'), false)
})
