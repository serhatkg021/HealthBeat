import assert from 'node:assert/strict'
import { test } from 'node:test'
import { filterOptions, moveActive, type SelectOption } from './searchSelect.ts'

const options: SelectOption[] = [
  { value: '1', label: 'web-01', hint: 'Üretim', keywords: '10.0.4.21' },
  { value: '2', label: 'db-02', hint: 'Üretim', keywords: '10.0.4.32' },
  { value: '3', label: 'İstanbul DC', depth: 1, keywords: 'Şirket › Üretim' },
]

const values = (list: SelectOption[]) => list.map((o) => o.value)

test('an empty query keeps every option in its order', () => {
  assert.deepEqual(values(filterOptions(options, '')), ['1', '2', '3'])
  assert.deepEqual(values(filterOptions(options, '   ')), ['1', '2', '3'])
})

test('every word must appear in the label, hint or keywords; matching ignores Turkish case', () => {
  assert.deepEqual(values(filterOptions(options, 'web')), ['1'])
  assert.deepEqual(values(filterOptions(options, '10.0.4.32')), ['2'])
  assert.deepEqual(values(filterOptions(options, 'üretim db')), ['2'])
  assert.deepEqual(values(filterOptions(options, 'istanbul')), ['3'])
  assert.deepEqual(values(filterOptions(options, 'ŞİRKET')), ['3'])
  assert.deepEqual(values(filterOptions(options, 'yok')), [])
})

test('arrow keys wrap around; with nothing highlighted they start from the matching end', () => {
  assert.equal(moveActive(-1, 3, 'ArrowDown'), 0)
  assert.equal(moveActive(-1, 3, 'ArrowUp'), 2)
  assert.equal(moveActive(2, 3, 'ArrowDown'), 0)
  assert.equal(moveActive(0, 3, 'ArrowUp'), 2)
  assert.equal(moveActive(0, 0, 'ArrowDown'), -1)
})
