import assert from 'node:assert/strict'
import { test } from 'node:test'
import { nextTab, resolveTab } from './tabs.ts'

const ids = ['genel', 'disk', 'esikler', 'ayarlar']

test('an allowed tab is used, anything else falls back to the default', () => {
  assert.equal(resolveTab('disk', ids, 'genel'), 'disk')
  assert.equal(resolveTab(null, ids, 'genel'), 'genel')
  assert.equal(resolveTab('', ids, 'genel'), 'genel')
  assert.equal(resolveTab('yok', ids, 'genel'), 'genel')
  assert.equal(resolveTab('DISK', ids, 'genel'), 'genel', 'the match is exact')
  assert.equal(resolveTab('ayarlar', ids.slice(0, 3), 'genel'), 'genel', 'a tab the user may not see is not reachable through the URL')
})

test('arrow keys move to the neighbour and wrap around; Home/End jump to the ends', () => {
  assert.equal(nextTab(ids, 'genel', 'ArrowRight'), 'disk')
  assert.equal(nextTab(ids, 'ayarlar', 'ArrowRight'), 'genel')
  assert.equal(nextTab(ids, 'genel', 'ArrowLeft'), 'ayarlar')
  assert.equal(nextTab(ids, 'esikler', 'ArrowLeft'), 'disk')
  assert.equal(nextTab(ids, 'disk', 'Home'), 'genel')
  assert.equal(nextTab(ids, 'disk', 'End'), 'ayarlar')
  assert.equal(nextTab(ids, 'disk', 'Enter'), null)
  assert.equal(nextTab([], 'x', 'ArrowRight'), null)
  assert.equal(nextTab(['tek'], 'tek', 'ArrowRight'), 'tek')
})
