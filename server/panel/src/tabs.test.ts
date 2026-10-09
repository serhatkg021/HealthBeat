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

test('an old tab name maps to the tab that replaced it; an alias to a tab the user cannot see falls back', () => {
  const ids = ['genel', 'envanter', 'servisler']
  const aliases = { sistem: 'envanter', docker: 'servisler', eski: 'yok' }
  assert.equal(resolveTab('sistem', ids, 'genel', aliases), 'envanter')
  assert.equal(resolveTab('docker', ids, 'genel', aliases), 'servisler')
  assert.equal(resolveTab('envanter', ids, 'genel', aliases), 'envanter')
  assert.equal(resolveTab('eski', ids, 'genel', aliases), 'genel')
  // Nesnenin kendi alanı olmayan adlar (ör. "constructor") eşleme sayılmaz.
  assert.equal(resolveTab('constructor', ids, 'genel', aliases), 'genel')
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

test('a vertical menu moves with the up and down arrows; left and right do nothing', () => {
  const ids = ['cpu', 'disk', 'saat']
  assert.equal(nextTab(ids, 'cpu', 'ArrowDown', 'vertical'), 'disk')
  assert.equal(nextTab(ids, 'cpu', 'ArrowUp', 'vertical'), 'saat')
  assert.equal(nextTab(ids, 'disk', 'ArrowRight', 'vertical'), null)
  assert.equal(nextTab(ids, 'disk', 'End', 'vertical'), 'saat')
  assert.equal(nextTab(ids, 'disk', 'ArrowDown'), null)
})
