import assert from 'node:assert/strict'
import { test } from 'node:test'
import { choiceLabel, nextChoice, parseChoice, resolveTheme } from './theme.ts'

test('an unknown or missing saved value means "follow the system"', () => {
  assert.equal(parseChoice(null), 'system')
  assert.equal(parseChoice('blue'), 'system')
  assert.equal(parseChoice('dark'), 'dark')
  assert.equal(parseChoice('light'), 'light')
})

test('system follows the operating system; light and dark ignore it', () => {
  assert.equal(resolveTheme('system', true), 'dark')
  assert.equal(resolveTheme('system', false), 'light')
  assert.equal(resolveTheme('light', true), 'light')
  assert.equal(resolveTheme('dark', false), 'dark')
})

test('the toolbar icon cycles system → light → dark → system', () => {
  assert.deepEqual([nextChoice('system'), nextChoice('light'), nextChoice('dark')], ['light', 'dark', 'system'])
  assert.equal(choiceLabel('system'), 'Sistem')
})
