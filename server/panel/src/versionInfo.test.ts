import assert from 'node:assert/strict'
import { test } from 'node:test'
import { versionInfo } from './versionInfo.ts'

test('equal versions are shown as they are, without a warning', () => {
  const v = versionInfo('1.4.0', '1.4.0')
  assert.deepEqual([v.panel, v.server, v.mismatch], ['1.4.0', '1.4.0', false])
  assert.equal(v.title, 'Panel 1.4.0 · Server 1.4.0')
})

test('different versions warn and explain how to fix it', () => {
  const v = versionInfo('1.4.0', '1.3.0')
  assert.equal(v.mismatch, true)
  assert.match(v.title, /Panel 1\.4\.0, server 1\.3\.0/)
  assert.match(v.title, /HB_VERSION/)
})

test('a version that is unknown never triggers a mismatch warning', () => {
  for (const [p, s] of [['1.4.0', undefined], [undefined, '1.4.0'], ['1.4.0', ''], ['', ''], [undefined, undefined], ['dev', '1.4.0'], ['1.4.0', 'dev']] as const) {
    assert.equal(versionInfo(p, s).mismatch, false, `${p} / ${s}`)
  }
  assert.equal(versionInfo('1.4.0', undefined).server, '—')
  assert.equal(versionInfo(undefined, '1.4.0').panel, '—')
})

test('whitespace is ignored and the comparison is exact (a prerelease differs from its release)', () => {
  assert.equal(versionInfo(' 1.4.0 ', '1.4.0').mismatch, false)
  assert.equal(versionInfo('1.4.0', ' 1.4.0 ').mismatch, false)
  assert.equal(versionInfo('1.4.0', '  ').server, '—', 'a blank server version is unknown')
  assert.equal(versionInfo('1.4.0-rc.1', '1.4.0').mismatch, true)
})
