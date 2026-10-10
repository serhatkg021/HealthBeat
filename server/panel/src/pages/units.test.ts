import assert from 'node:assert/strict'
import { test } from 'node:test'
import {
  decimal,
  formatBitRate,
  formatByteRate,
  formatCelsius,
  formatCount,
  formatIOPS,
  formatMs,
  formatOffsetMs,
  formatPct,
  formatPerSecond,
  formatTick,
  niceAxis,
} from './units.ts'

test('unknown values render a dash, never NaN or "undefined"', () => {
  for (const f of [formatPct, formatMs, formatOffsetMs, formatCelsius, formatByteRate, formatBitRate, formatIOPS, formatPerSecond, formatCount]) {
    assert.equal(f(undefined), '—')
    assert.equal(f(null), '—')
    assert.equal(f(Number.NaN), '—')
    assert.equal(f(Number.POSITIVE_INFINITY), '—')
  }
})

test('decimal uses a comma and drops the sign of a rounded zero', () => {
  assert.equal(decimal(12.345, 1), '12,3')
  assert.equal(decimal(-0.0004, 2), '0,00')
  assert.equal(decimal(3, 0), '3')
})

test('percentages keep one decimal', () => {
  assert.equal(formatPct(0.16), '%0,2')
  assert.equal(formatPct(54.234), '%54,2')
  assert.equal(formatPct(100), '%100,0')
  assert.equal(formatPct(7.25, 0), '%7')
})

test('latency loses decimals as it grows', () => {
  assert.equal(formatMs(0), '0 ms')
  assert.equal(formatMs(0.374), '0,37 ms')
  assert.equal(formatMs(9.996), '10,00 ms')
  assert.equal(formatMs(12.36), '12,4 ms')
  assert.equal(formatMs(152.6), '153 ms')
  assert.equal(formatMs(4321.2), '4321 ms')
})

test('clock offset keeps sub-millisecond precision and its sign', () => {
  assert.equal(formatOffsetMs(1.8414), '1,841 ms')
  assert.equal(formatOffsetMs(-12.345), '-12,35 ms')
  assert.equal(formatOffsetMs(250.04), '250,0 ms')
})

test('temperature has one decimal', () => {
  assert.equal(formatCelsius(64.25), '64,3 °C')
})

test('disk throughput is 1024-based bytes per second', () => {
  assert.equal(formatByteRate(0), '0 B/sn')
  assert.equal(formatByteRate(512), '512 B/sn')
  assert.equal(formatByteRate(1536), '1,5 KB/sn')
  assert.equal(formatByteRate(1.5 * 1024 * 1024), '1,5 MB/sn')
  assert.equal(formatByteRate(250 * 1024 * 1024), '250 MB/sn')
  assert.equal(formatByteRate(2 * 1024 ** 3), '2,0 GB/sn')
})

test('network throughput is 1000-based bits per second', () => {
  assert.equal(formatBitRate(800), '800 bit/sn')
  assert.equal(formatBitRate(94_200_000), '94,2 Mbit/sn')
  assert.equal(formatBitRate(940_000_000), '940 Mbit/sn')
  assert.equal(formatBitRate(10e9), '10,0 Gbit/sn')
})

test('IOPS and per-second rates', () => {
  assert.equal(formatIOPS(0.4), '0,4 IOPS')
  assert.equal(formatIOPS(1234.6), '1235 IOPS')
  assert.equal(formatPerSecond(3.46), '3,5/sn')
})

test('counts use a thousands separator', () => {
  assert.equal(formatCount(123456), '123.456')
  assert.equal(formatCount(4194304), '4.194.304')
  assert.equal(formatCount(0), '0')
})

test('axis ticks: integers grouped, fractions with a comma and no trailing zeros', () => {
  assert.equal(formatTick(0), '0')
  assert.equal(formatTick(12000), '12.000')
  assert.equal(formatTick(0.5), '0,5')
  assert.equal(formatTick(0.25), '0,25')
  assert.equal(formatTick(2.5), '2,5')
  assert.equal(formatTick(Number.NaN), '')
})

test('niceAxis ends on a round value with evenly spaced ticks', () => {
  assert.deepEqual(niceAxis(0.38), { top: 0.4, ticks: [0, 0.1, 0.2, 0.3, 0.4] })
  assert.deepEqual(niceAxis(1.6), { top: 2, ticks: [0, 0.5, 1, 1.5, 2] })
  assert.deepEqual(niceAxis(5), { top: 5, ticks: [0, 1, 2, 3, 4, 5] })
  assert.deepEqual(niceAxis(54.2), { top: 60, ticks: [0, 20, 40, 60] })
  assert.deepEqual(niceAxis(100), { top: 100, ticks: [0, 20, 40, 60, 80, 100] })
  assert.deepEqual(niceAxis(6e6), { top: 6e6, ticks: [0, 2e6, 4e6, 6e6] })
  assert.deepEqual(niceAxis(0), { top: 1, ticks: [0, 0.2, 0.4, 0.6, 0.8, 1] })
  assert.deepEqual(niceAxis(Number.NaN).top, 1)
})
