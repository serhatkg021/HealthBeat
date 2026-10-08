import assert from 'node:assert/strict'
import { test } from 'node:test'
import type { HostThresholdsResponse, MetricPoint } from '../types/api.ts'
import {
  capacityRows,
  daemonLabel,
  ioSummary,
  raidState,
  reachText,
  stateWarnings,
  temperatureLevels,
  temperatureTone,
  timeSourceState,
  timeSyncIssues,
} from './systemState.ts'

const L = (w: number, c: number) => ({ warning_level: w, critical_level: c })
const NOW = new Date('2026-10-08T12:00:00Z').getTime()

test('a sensor uses its own threshold, then the server threshold; none means neutral', () => {
  const res: HostThresholdsResponse = {
    thresholds: [{ metric_type: 'temperature', default: L(80, 90), custom: null }],
    mount_thresholds: [],
    container_thresholds: [],
    subject_thresholds: [{ metric_type: 'temperature', subject: 'nvme', custom: L(60, 70) }],
  }
  assert.deepEqual(temperatureLevels(res, 'nvme'), L(60, 70))
  assert.deepEqual(temperatureLevels(res, 'cpu'), L(80, 90))
  assert.equal(temperatureLevels(null, 'cpu'), null)
  assert.equal(temperatureTone({ celsius: 65 }, L(60, 70)), 'warning')
  assert.equal(temperatureTone({ celsius: 70 }, L(60, 70)), 'critical')
  assert.equal(temperatureTone({ celsius: 50 }, L(60, 70)), 'good')
  assert.equal(temperatureTone({ celsius: 99 }, null), 'neutral', 'hardware limits do not colour; only alert thresholds do')
})

test('capacity rows skip what is unknown', () => {
  assert.deepEqual(capacityRows(undefined), [])
  const rows = capacityRows({ file_handles: 1000, file_handles_max: 4000, conntrack: 10, tasks: 300, pid_max: 4194304 })
  assert.deepEqual(rows.map((r) => [r.label, r.pct, r.unlimited]), [
    ['Dosya tanıtıcısı', 25, false],
    ['Süreç ve iş parçacığı', (300 / 4194304) * 100, false],
  ])
})

test('a limit the kernel reports as "max integer" is shown as no limit', () => {
  // /proc/sys/fs/file-nr'deki 9223372036854775807 (2^63-1) JSON'dan JavaScript'e 2^63 olarak yuvarlanıp gelir.
  const [row] = capacityRows({ file_handles: 12977, file_handles_max: 2 ** 63 })
  assert.equal(row.unlimited, true)
  assert.equal(row.pct, 0)
  assert.equal(capacityRows({ conntrack: 73, conntrack_max: 262144 })[0].unlimited, false)
})

test('RAID and time source states', () => {
  assert.deepEqual(raidState({ state: 'clean' }), { label: 'sağlıklı', tone: 'good' })
  assert.deepEqual(raidState({ state: 'degraded' }), { label: 'bozuk', tone: 'critical' })
  assert.deepEqual(raidState({ state: 'resyncing' }), { label: 'eşitleniyor', tone: 'warning' })
  assert.deepEqual(raidState({ state: 'odd' }), { label: 'odd', tone: 'neutral' })
  assert.equal(timeSourceState('falseticker').tone, 'critical')
  assert.equal(reachText(255), '8/8')
  assert.equal(reachText(0b1011), '3/8')
  assert.equal(reachText(undefined), '—')
  assert.equal(daemonLabel('timesyncd'), 'systemd-timesyncd')
  assert.equal(daemonLabel(undefined), '—')
})

test('time sync issues follow the server rules', () => {
  assert.deepEqual(timeSyncIssues({ enabled: true, synchronized: true, daemon: 'chrony', stratum: 2, leap: 'normal' }, NOW), [])
  assert.deepEqual(
    timeSyncIssues(
      {
        enabled: false,
        synchronized: false,
        ignored: true,
        leap: 'alarm',
        stratum: 16,
        sources: [{ name: 'a', state: 'unreachable', reach: 0 }],
        last_sync: '2026-10-08T10:00:00Z',
        poll_s: 1024,
        local_rtc: true,
      },
      NOW,
    ),
    [
      'NTP kapalı',
      'saat senkron değil',
      'saat sunucusunun son yanıtı geçersiz sayıldı',
      'saat sunucusu senkron olmadığını bildiriyor (leap alarm)',
      'saat sunucusu senkron değil (stratum 16)',
      'hiçbir saat kaynağına ulaşılamıyor',
      'son senkron beklenenden eski',
      'donanım saati yerel saatte (UTC önerilir)',
    ],
  )
})

const point = (extra: Partial<MetricPoint>): MetricPoint => ({ timestamp: '2026-10-08T12:00:00Z', cpu_usage_pct: 1, ram_usage_pct: 1, disk: [], ...extra })

test('the warning strip lists broken RAID, read-only mounts and a recent OOM kill', () => {
  assert.deepEqual(stateWarnings(undefined, null, NOW), [])
  const w = stateWarnings(
    {
      raid: [
        { name: 'md0', state: 'degraded', devices: 2, active: 1 },
        { name: 'md1', state: 'recovering', devices: 2, active: 2, sync_pct: 41.25 },
        { name: 'md2', state: 'clean', devices: 2, active: 2 },
      ],
      oom_last_increase_at: '2026-10-08T03:00:00Z',
    },
    point({ disk: [{ mount: '/', used_pct: 1, total: 1, free: 1, read_only: true }, { mount: '/data', used_pct: 1, total: 1, free: 1, read_only: false }] }),
    NOW,
  )
  assert.deepEqual(w, [
    { tone: 'critical', text: 'RAID md0 bozuk (1/2 disk etkin)' },
    { tone: 'warning', text: 'RAID md1 yeniden kuruluyor (%41,3)' },
    { tone: 'critical', text: 'Salt okunur bağlı: /' },
    { tone: 'warning', text: 'Son 24 saatte bellek yetmediği için süreç öldürüldü (OOM)' },
  ])
  assert.deepEqual(stateWarnings({ oom_last_increase_at: '2026-10-06T03:00:00Z' }, null, NOW), [], 'an old OOM kill is not repeated')
})

test('ioSummary: one line per physical disk', () => {
  const io = { read_iops: 1, write_iops: 1, read_bps: 1536, write_bps: 0, util_pct: 2.5, await_ms: 0.374, queue_depth: 0 }
  assert.deepEqual(ioSummary(point({ disk_io: [{ name: 'sdb', ...io }, { name: 'sda', ...io }] })), [
    { name: 'sda', text: 'gecikme 0,37 ms · okuma 1,5 KB/sn · yazma 0 B/sn · meşgul %2,5' },
    { name: 'sdb', text: 'gecikme 0,37 ms · okuma 1,5 KB/sn · yazma 0 B/sn · meşgul %2,5' },
  ])
  assert.deepEqual(ioSummary(null), [])
})
