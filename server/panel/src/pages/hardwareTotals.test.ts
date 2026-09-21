import assert from 'node:assert/strict'
import { test } from 'node:test'
import { diskKindLabel, formatBytes, formatCores, formatRamUsage, joinParts } from './hardwareTotals.ts'

test('formatCores: unknown totals render nothing', () => {
  assert.equal(formatCores(undefined), null)
  assert.equal(formatCores(null), null)
  assert.equal(formatCores(0), null)
  assert.equal(formatCores(8), '8 çekirdek')
})

test('formatRamUsage: unknown total renders nothing', () => {
  assert.equal(formatRamUsage(50, undefined), null)
  assert.equal(formatRamUsage(50, 0), null)
})

test('formatRamUsage: used is derived from the percentage', () => {
  assert.equal(formatRamUsage(60, 16384), '9.6 / 16 GB')
  assert.equal(formatRamUsage(50, 512), '256 / 512 MB')
  assert.equal(formatRamUsage(0, 2048), '0 / 2 GB')
})

test('joinParts', () => {
  assert.equal(joinParts('%42.5', null), '%42.5')
  assert.equal(joinParts('%42.5', '8 çekirdek'), '%42.5 · 8 çekirdek')
})

test('formatBytes', () => {
  assert.equal(formatBytes(undefined), '—')
  assert.equal(formatBytes(0), '—')
  assert.equal(formatBytes(512), '512 B')
  assert.equal(formatBytes(1023), '1023 B') // 1000 değil 1024 tabanı
  assert.equal(formatBytes(1024), '1 KB')
  assert.equal(formatBytes(500107862016), '465.8 GB') // lsblk: 465,8G
  assert.equal(formatBytes(2000398934016), '1.8 TB')
  assert.equal(formatBytes(1024 * 1024 * 1024), '1 GB')
  assert.equal(formatBytes(120034123776), '111.8 GB')
})

test('diskKindLabel', () => {
  assert.equal(diskKindLabel('nvme'), 'NVMe SSD')
  assert.equal(diskKindLabel('ssd'), 'SSD')
  assert.equal(diskKindLabel('hdd'), 'HDD')
  assert.equal(diskKindLabel(''), '—')
  assert.equal(diskKindLabel(undefined), '—')
  assert.equal(diskKindLabel('floppy'), '—')
})
