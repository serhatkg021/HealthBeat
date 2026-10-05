import assert from 'node:assert/strict'
import { test } from 'node:test'
import { MAX_DISK_SERIES, cpuRamRows, diskMounts, diskRows, formatPoint, isLongSpan } from './metricHistory.ts'
import type { MetricPoint } from '../types/api.ts'

const point = (iso: string, cpu: number, ram: number, disks: [string, number][]): MetricPoint => ({
  timestamp: iso,
  cpu_usage_pct: cpu,
  ram_usage_pct: ram,
  disk: disks.map(([mount, used_pct]) => ({ mount, used_pct, total: 100, free: 100 - used_pct })),
})

test('cpu/ram rows carry the timestamp and one decimal', () => {
  const rows = cpuRamRows([point('2026-10-05T10:00:00Z', 12.345, 60.06, [])])
  assert.deepEqual(rows, [{ ts: Date.parse('2026-10-05T10:00:00Z'), cpu: 12.3, ram: 60.1 }])
})

test('disk mounts are sorted, unique and capped; a mount missing from a report is absent in that row', () => {
  const points = [point('2026-10-05T10:00:00Z', 0, 0, [['/var', 40], ['/', 10]]), point('2026-10-05T10:01:00Z', 0, 0, [['/', 11.26]])]
  const mounts = diskMounts(points)
  assert.deepEqual(mounts, ['/', '/var'])
  assert.deepEqual(diskRows(points, mounts), [
    { ts: Date.parse('2026-10-05T10:00:00Z'), '/': 10, '/var': 40 },
    { ts: Date.parse('2026-10-05T10:01:00Z'), '/': 11.3 },
  ])
  const many = [point('2026-10-05T10:00:00Z', 0, 0, Array.from({ length: 12 }, (_, i) => [`/m${String(i).padStart(2, '0')}`, 1] as [string, number]))]
  assert.equal(diskMounts(many).length, MAX_DISK_SERIES)
})

test('labels add the date only when the span is longer than a day', () => {
  const ms = new Date(2026, 9, 5, 7, 8, 9).getTime()
  assert.equal(formatPoint(ms, 60 * 60 * 1000), '07:08:09')
  assert.equal(formatPoint(ms, 2 * 24 * 60 * 60 * 1000), '05-10-2026 07:08:09')
  assert.equal(isLongSpan(24 * 60 * 60 * 1000), false)
  assert.equal(isLongSpan(24 * 60 * 60 * 1000 + 1), true)
})
