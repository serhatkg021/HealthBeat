import assert from 'node:assert/strict'
import { test } from 'node:test'
import type { Organization } from '../types/api.ts'
import { buildTree, parentChoices, parentMap, selfAndDescendants } from './orgTree.ts'

const org = (id: string, name: string, parent?: string, access?: 'full' | 'context'): Organization => ({
  id,
  name,
  parent_organization_id: parent,
  access,
  created_at: '',
})

const orgs = [org('beta', 'Beta', 'holding'), org('ist', 'Acme İstanbul', 'acme'), org('holding', 'Holding'), org('acme', 'Acme', 'holding'), org('solo', 'Bağımsız')]

test('buildTree orders roots by name with children indented beneath their parent', () => {
  assert.deepEqual(
    buildTree(orgs).map((r) => [r.org.id, r.depth, r.path.join(' › ')]),
    [
      ['solo', 0, ''],
      ['holding', 0, ''],
      ['acme', 1, 'Holding'],
      ['ist', 2, 'Holding › Acme'],
      ['beta', 1, 'Holding'],
    ],
  )
})

test('an organization whose parent is not visible becomes a root instead of vanishing', () => {
  const visible = [org('acme', 'Acme', 'holding'), org('ist', 'İst', 'acme')]
  assert.deepEqual(buildTree(visible).map((r) => [r.org.id, r.depth]), [['acme', 0], ['ist', 1]])
})

test('buildTree survives a cycle in bad data', () => {
  const rows = buildTree([org('a', 'A', 'b'), org('b', 'B', 'a')])
  assert.ok(rows.length <= 2)
})

test('selfAndDescendants and parentChoices prevent moving a branch under itself', () => {
  assert.deepEqual([...selfAndDescendants('acme', orgs)].sort(), ['acme', 'ist'])
  assert.deepEqual(parentChoices(orgs, 'acme').map((o) => o.id).sort(), ['beta', 'holding', 'solo'])
  assert.deepEqual(parentChoices(orgs).length, 5)
  // Bağlam organizasyonları (yalnızca bilgi) üst şirket olarak seçilemez.
  assert.deepEqual(parentChoices([org('h', 'H', undefined, 'context'), org('a', 'A', 'h', 'full')]).map((o) => o.id), ['a'])
})

test('parentMap maps every organization to its parent', () => {
  const m = parentMap(orgs)
  assert.equal(m.get('ist'), 'acme')
  assert.equal(m.get('holding'), undefined)
  assert.equal(m.size, 5)
})
