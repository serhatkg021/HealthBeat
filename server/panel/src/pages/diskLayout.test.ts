import assert from 'node:assert/strict'
import { test } from 'node:test'
import { diskLayout, shareOfDisk, usedBytesOf } from './diskLayout.ts'
import type { DiskUsage, PhysicalDisk } from '../types/api.ts'

const GB = 1024 ** 3
const use = (mount: string, totalGB: number, usedGB: number): DiskUsage => ({
  mount,
  total: totalGB * GB,
  free: (totalGB - usedGB) * GB,
  used_pct: (usedGB / totalGB) * 100,
})

test('a 2 TB disk split into /storage and / shows each mount total and usage', () => {
  const disks: PhysicalDisk[] = [{ name: 'sda', size_bytes: 2048 * GB, mounts: ['/storage', '/'] }]
  const layout = diskLayout(disks, [use('/storage', 1500, 1200), use('/', 500, 100)])
  assert.equal(layout.groups.length, 1)
  const g = layout.groups[0]
  assert.deepEqual(g.mounts.map((m) => m.mount), ['/storage', '/'])
  assert.equal(g.mounts[0].usage?.total, 1500 * GB)
  assert.equal(g.mounts[0].usedBytes, 1200 * GB)
  assert.equal(g.allocatedBytes, 2000 * GB)
  assert.equal(g.otherBytes, 48 * GB)
  assert.deepEqual(layout.unassigned, [])
})

test('shareOfDisk is the mount total relative to the disk size', () => {
  const disk: PhysicalDisk = { name: 'sda', size_bytes: 2000 * GB, mounts: ['/storage'] }
  const g = diskLayout([disk], [use('/storage', 1500, 10)]).groups[0]
  assert.equal(shareOfDisk(disk, g.mounts[0]), 75)
})

test('a mount spanning several disks (LVM) is shared: it is not split across disks nor counted as allocation', () => {
  const disks: PhysicalDisk[] = [
    { name: 'sda', size_bytes: 1000 * GB, mounts: ['/data', '/boot'] },
    { name: 'sdb', size_bytes: 1000 * GB, mounts: ['/data'] },
  ]
  const layout = diskLayout(disks, [use('/data', 1900, 500), use('/boot', 1, 0.2)])
  const a = layout.groups[0]
  assert.equal(a.mounts.find((m) => m.mount === '/data')?.shared, true)
  assert.equal(a.mounts.find((m) => m.mount === '/boot')?.shared, false)
  assert.equal(a.allocatedBytes, 1 * GB, 'only the disk-exclusive mount counts')
  assert.equal(layout.groups[1].allocatedBytes, 0)
  assert.equal(shareOfDisk(disks[0], a.mounts[0]), null)
})

test('mounts that are on no physical disk are listed separately; a mount not reported has no usage', () => {
  const disks: PhysicalDisk[] = [{ name: 'nvme0n1', size_bytes: 500 * GB, mounts: ['/', '/boot/efi'] }]
  const layout = diskLayout(disks, [use('/', 400, 100), use('/mnt/nas', 4000, 3000)])
  assert.deepEqual(layout.unassigned.map((u) => u.mount), ['/mnt/nas'])
  const efi = layout.groups[0].mounts.find((m) => m.mount === '/boot/efi')
  assert.equal(efi?.usage, undefined)
  assert.equal(efi?.usedBytes, undefined)
  assert.equal(layout.groups[0].allocatedBytes, 400 * GB)
})

test('filesystems bigger than the reported disk, or an unknown disk size, never produce a negative remainder', () => {
  const big = diskLayout([{ name: 'a', size_bytes: 100 * GB, mounts: ['/'] }], [use('/', 120, 1)])
  assert.equal(big.groups[0].otherBytes, 0)
  const unknown = diskLayout([{ name: 'b', mounts: ['/'] }], [use('/', 120, 1)])
  assert.equal(unknown.groups[0].otherBytes, 0)
  assert.equal(shareOfDisk({ name: 'b', mounts: ['/'] }, unknown.groups[0].mounts[0]), null)
  assert.equal(shareOfDisk({ name: 'a', size_bytes: 100 * GB, mounts: ['/'] }, big.groups[0].mounts[0]), 100, 'clamped')
})

test('no disk data at all is an empty layout; duplicated mount names in one disk are collapsed', () => {
  assert.deepEqual(diskLayout(undefined, undefined), { groups: [], unassigned: [] })
  const g = diskLayout([{ name: 'a', size_bytes: 10 * GB, mounts: ['/', '/'] }], [use('/', 5, 1)]).groups[0]
  assert.equal(g.mounts.length, 1)
  assert.equal(g.mounts[0].shared, false, 'the same mount listed twice on ONE disk is not shared')
})

test('usedBytesOf never goes negative', () => {
  assert.equal(usedBytesOf({ mount: '/', used_pct: 0, total: 10, free: 20 }), 0)
  assert.equal(usedBytesOf({ mount: '/', used_pct: 50, total: 10, free: 4 }), 6)
})
