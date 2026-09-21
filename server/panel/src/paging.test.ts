import assert from 'node:assert/strict'
import { test } from 'node:test'
import { PAGE_SIZE_OPTIONS, pageSizeOptions } from './paging.ts'

test('every page default used in the panel (10, 20, 50) is a standard option', () => {
  for (const size of [10, 20, 50]) assert.ok(PAGE_SIZE_OPTIONS.includes(size), `${size} must be selectable`)
})

test('a size in the list leaves the list untouched', () => {
  assert.deepEqual(pageSizeOptions(20), PAGE_SIZE_OPTIONS)
})

test('a size outside the list is added in order, so the select never shows a wrong value', () => {
  assert.deepEqual(pageSizeOptions(25), [10, 20, 25, 50, 100])
  assert.deepEqual(pageSizeOptions(5), [5, 10, 20, 50, 100])
  assert.deepEqual(pageSizeOptions(500), [10, 20, 50, 100, 500])
})
