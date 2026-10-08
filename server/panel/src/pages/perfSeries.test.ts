import assert from 'node:assert/strict'
import { test } from 'node:test'
import type { MetricPoint } from '../types/api.ts'
import {
  busiestDisk,
  busiestInterface,
  cpuDetailRows,
  diskIORows,
  hasAny,
  hasHealthSeries,
  ioDisks,
  latestTcp,
  netErrorTotals,
  netInterfaces,
  netRows,
  psiMax,
  psiRows,
  swapRows,
  tcpRows,
} from './perfSeries.ts'

const base = (t: string): MetricPoint => ({ timestamp: t, cpu_usage_pct: 10, ram_usage_pct: 20, disk: [] })
const T1 = '2026-10-08T10:00:00Z'
const T2 = '2026-10-08T10:01:00Z'
const ms = (t: string) => new Date(t).getTime()

const io = (name: string, read: number, write: number, await_ms = 1) => ({
  name,
  read_iops: 10,
  write_iops: 20,
  read_bps: read,
  write_bps: write,
  util_pct: 5,
  await_ms,
  queue_depth: 0.1,
})
const net = (iface: string, rx: number, tx: number, errors = 0) => ({ interface: iface, rx_bps: rx, tx_bps: tx, rx_errors: errors, tx_errors: 0, rx_drops: 1, tx_drops: 0 })

const P1: MetricPoint = {
  ...base(T1),
  system: {
    cpu_detail: { iowait_pct: 3.456, steal_pct: 0.1 },
    pressure: { cpu: { some10: 1, some60: 2 }, memory: { some10: 0, some60: 0.5, full10: 0, full60: 0.2 }, io: { some10: 30, some60: 54.2, full10: 20, full60: 36.7 } },
    memory_detail: { swap_in_per_s: 0, swap_out_per_s: 12.5 },
    tcp: { retrans_pct: 0.4, established: 120, time_wait: 30 },
  },
  disk_io: [io('sda', 100, 200, 4.2), io('nvme0n1', 5000, 9000)],
  net_io: [net('eth0', 1e6, 2e6, 2), net('docker0', 10, 10)],
}
const OLD: MetricPoint = base(T2) // eski agent'ın satırı

test('an old agent row has no protocol 4 series and leaves a gap instead of a zero', () => {
  assert.equal(hasHealthSeries([OLD]), false)
  assert.equal(hasHealthSeries([OLD, P1]), true)
  assert.deepEqual(cpuDetailRows([P1, OLD]), [{ ts: ms(T1), iowait: 3.456, steal: 0.1 }, { ts: ms(T2) }])
})

test('values are kept raw (rounding happens when they are shown)', () => {
  assert.equal(cpuDetailRows([P1])[0].iowait, 3.456)
})

test('PSI uses the 60 s averages; CPU has no full line', () => {
  assert.deepEqual(psiRows([P1])[0], { ts: ms(T1), cpu_some: 2, mem_some: 0.5, mem_full: 0.2, io_some: 54.2, io_full: 36.7 })
})

test('the three PSI charts share a rounded upper bound', () => {
  assert.equal(psiMax([]), 5)
  assert.equal(psiMax([{ ts: 1, a: 0.3 }]), 5)
  assert.equal(psiMax([{ ts: 1, a: 7.1 }]), 10)
  assert.equal(psiMax(psiRows([P1])), 60)
  assert.equal(psiMax([{ ts: 1, a: 98 }]), 100)
})

test('disk I/O: disks in the range, the busiest as default, one disk per chart', () => {
  assert.deepEqual(ioDisks([P1, OLD]), ['nvme0n1', 'sda'])
  assert.equal(busiestDisk([P1]), 'nvme0n1')
  assert.equal(busiestDisk([OLD]), undefined)
  assert.deepEqual(diskIORows([P1, OLD], 'sda'), [
    { ts: ms(T1), await: 4.2, read_bps: 100, write_bps: 200, read_iops: 10, write_iops: 20, util: 5 },
    { ts: ms(T2) },
  ])
})

test('network: interfaces, the busiest as default, traffic and error totals', () => {
  assert.deepEqual(netInterfaces([P1]), ['docker0', 'eth0'])
  assert.equal(busiestInterface([P1]), 'eth0')
  assert.deepEqual(netRows([P1], 'eth0'), [{ ts: ms(T1), rx: 1e6, tx: 2e6 }])
  assert.deepEqual(netErrorTotals([P1, P1, OLD], 'eth0'), { rx_errors: 4, tx_errors: 0, rx_drops: 2, tx_drops: 0 })
})

test('swap, TCP and the latest connection counts', () => {
  assert.deepEqual(swapRows([P1])[0], { ts: ms(T1), swap_in: 0, swap_out: 12.5 })
  assert.deepEqual(tcpRows([P1, OLD]), [{ ts: ms(T1), retrans: 0.4 }, { ts: ms(T2) }])
  assert.deepEqual(latestTcp([P1, OLD]), { established: 120, time_wait: 30 })
  assert.equal(latestTcp([OLD]), null)
  assert.equal(hasAny(tcpRows([OLD]), ['retrans']), false)
  assert.equal(hasAny(tcpRows([P1]), ['retrans']), true)
})
