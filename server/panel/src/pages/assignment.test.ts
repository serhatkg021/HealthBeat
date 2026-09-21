import assert from 'node:assert/strict'
import { test } from 'node:test'
import {
  groupState,
  sameSelection,
  selectedCount,
  serializeSelection,
  toggleHost,
  toggleGroup,
} from './assignment.ts'

const orgA = { hosts: [{ id: 'a1' }, { id: 'a2' }, { id: 'a3' }] }
const empty = { hosts: [] }

test('groupState reports none / some / all', () => {
  assert.equal(groupState(orgA, new Set()), 'none')
  assert.equal(groupState(orgA, new Set(['a2'])), 'some')
  assert.equal(groupState(orgA, new Set(['a1', 'a2', 'a3'])), 'all')
  assert.equal(groupState(orgA, new Set(['b1'])), 'none') // diğer grupların kimlikleri sayılmaz
  assert.equal(groupState(empty, new Set(['a1'])), 'none') // boş bir organizasyon asla "tümü" değildir
})

test('toggleHost adds and removes one id without mutating its input', () => {
  const before = new Set(['a1'])
  const added = toggleHost(before, 'a2', true)
  assert.deepEqual([...added].sort(), ['a1', 'a2'])
  assert.deepEqual([...before], ['a1']) // girdiye dokunulmadı
  assert.deepEqual([...toggleHost(added, 'a1', false)], ['a2'])
  assert.deepEqual([...toggleHost(before, 'a1', true)], ['a1']) // iki kez eklemek etkisizdir
  assert.deepEqual([...toggleHost(before, 'zzz', false)], ['a1']) // olmayan bir kimliği kaldırmak etkisizdir
})

test('toggleGroup selects or clears exactly one organization', () => {
  const start = new Set(['a2', 'b1'])
  const all = toggleGroup(start, orgA, true)
  assert.deepEqual([...all].sort(), ['a1', 'a2', 'a3', 'b1']) // b1 (başka org) korundu, a2 yinelenmedi
  assert.deepEqual([...start].sort(), ['a2', 'b1']) // girdiye dokunulmadı

  const cleared = toggleGroup(all, orgA, false)
  assert.deepEqual([...cleared], ['b1']) // yalnızca org A'nın host'ları kaldırıldı
})

// Kaydetmede tüm atama değiştirilir: ekranın gösteremediği bir kimlik (diyelim organizasyonunun
// listesi yüklenemedi ya da gizli) düzenlemelerle düşürülmemeli.
test('ids that are not on screen survive edits', () => {
  const assigned = new Set(['hidden-1', 'a1'])
  let s = toggleGroup(assigned, orgA, true)
  s = toggleGroup(s, orgA, false)
  s = toggleHost(s, 'b1', true)
  assert.deepEqual(serializeSelection(s), ['b1', 'hidden-1'])
})

test('selectedCount counts only the group\'s own hosts', () => {
  assert.equal(selectedCount(orgA, new Set(['a1', 'a3', 'b1'])), 2)
  assert.equal(selectedCount(empty, new Set(['a1'])), 0)
})

test('serializeSelection is sorted and stable', () => {
  assert.deepEqual(serializeSelection(new Set(['c', 'a', 'b'])), ['a', 'b', 'c'])
  assert.deepEqual(serializeSelection(new Set(['b', 'c', 'a'])), serializeSelection(new Set(['a', 'b', 'c'])))
  assert.deepEqual(serializeSelection(new Set()), [])
})

test('sameSelection ignores order and detects any difference', () => {
  assert.equal(sameSelection(new Set(['a', 'b']), new Set(['b', 'a'])), true)
  assert.equal(sameSelection(new Set(['a']), new Set(['a', 'b'])), false)
  assert.equal(sameSelection(new Set(['a', 'x']), new Set(['a', 'b'])), false)
  assert.equal(sameSelection(new Set(), new Set()), true)
})
