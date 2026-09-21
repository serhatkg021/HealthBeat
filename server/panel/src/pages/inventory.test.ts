import assert from 'node:assert/strict'
import { test } from 'node:test'
import type { HostInfo } from '../types/api.ts'
import {
  formatUptime,
  ipMismatch,
  kernelLabel,
  loadText,
  normalizeIP,
  osLabel,
  shortMachineId,
  supportsInventory,
  swapText,
  virtualizationLabel,
} from './inventory.ts'

test('supportsInventory: protocol 3 and above', () => {
  assert.equal(supportsInventory({}), false)
  assert.equal(supportsInventory({ agent_protocol: 1 }), false)
  assert.equal(supportsInventory({ agent_protocol: 2 }), false)
  assert.equal(supportsInventory({ agent_protocol: 3 }), true)
  assert.equal(supportsInventory({ agent_protocol: 4 }), true)
})

test('osLabel prefers the pretty name and degrades gracefully', () => {
  assert.equal(osLabel({ os: { pretty_name: 'Ubuntu 24.04.5 LTS', name: 'Ubuntu' } }), 'Ubuntu 24.04.5 LTS')
  assert.equal(osLabel({ os: { name: 'Alpine Linux', version_id: '3.19.1' } }), 'Alpine Linux 3.19.1')
  assert.equal(osLabel({ os: { id: 'arch' } }), 'arch')
  assert.equal(osLabel({ os: {} }), '—')
  assert.equal(osLabel(undefined), '—')
})

test('kernelLabel', () => {
  assert.equal(kernelLabel({ kernel: { release: '6.8.0-45-generic', arch: 'x86_64' } }), '6.8.0-45-generic · x86_64')
  assert.equal(kernelLabel({ kernel: { arch: 'aarch64' } }), 'aarch64')
  assert.equal(kernelLabel({ kernel: {} }), '—')
  assert.equal(kernelLabel(undefined), '—')
})

test('virtualizationLabel: physical, vm, container, unknown', () => {
  assert.equal(virtualizationLabel({ virtualization: { kind: 'physical' }, machine: { vendor: 'Dell Inc.', model: 'PowerEdge R740' } }), 'Fiziksel · Dell Inc. PowerEdge R740')
  assert.equal(virtualizationLabel({ virtualization: { kind: 'physical' } }), 'Fiziksel')
  assert.equal(virtualizationLabel({ virtualization: { kind: 'vm', vendor: 'KVM' }, machine: { vendor: 'QEMU', model: 'Standard PC' } }), 'Sanal makine (KVM) · QEMU Standard PC')
  assert.equal(virtualizationLabel({ virtualization: { kind: 'vm' } }), 'Sanal makine')
  assert.equal(virtualizationLabel({ virtualization: { kind: 'container', vendor: 'docker' } }), 'Konteyner (docker)')
  assert.equal(virtualizationLabel({ virtualization: { kind: 'unknown' } }), '—')
  assert.equal(virtualizationLabel({ virtualization: { kind: 'unknown' }, machine: { vendor: 'Acme' } }), 'Acme')
  assert.equal(virtualizationLabel(undefined), '—')
})

test('formatUptime uses at most two units', () => {
  assert.equal(formatUptime(0), '0 sn')
  assert.equal(formatUptime(45), '45 sn')
  assert.equal(formatUptime(90), '1 dk')
  assert.equal(formatUptime(3600), '1 sa')
  assert.equal(formatUptime(3660), '1 sa 1 dk')
  assert.equal(formatUptime(26877), '7 sa 27 dk')
  assert.equal(formatUptime(86400), '1 gün')
  assert.equal(formatUptime(3 * 86400 + 4 * 3600 + 59 * 60), '3 gün 4 sa')
  assert.equal(formatUptime(undefined), '—')
  assert.equal(formatUptime(-1), '—')
  assert.equal(formatUptime(Number.NaN), '—')
})

test('loadText and swapText', () => {
  assert.equal(loadText({ load_avg: [0.84, 1.09, 1.18] }), '0.84 · 1.09 · 1.18 (1 / 5 / 15 dk)')
  assert.equal(loadText({ load_avg: [0, 0, 0] }), '0.00 · 0.00 · 0.00 (1 / 5 / 15 dk)')
  assert.equal(loadText({ load_avg: [1, 2] }), '—')
  assert.equal(loadText({}), '—')
  assert.equal(swapText({ swap: { total_mb: 4095, used_mb: 12 } }), '12 / 4095 MB')
  assert.equal(swapText({}), 'Yok')
})

test('normalizeIP strips the prefix and canonicalizes IPv6', () => {
  assert.equal(normalizeIP('192.168.1.106/24'), '192.168.1.106')
  assert.equal(normalizeIP(' 10.0.0.5 '), '10.0.0.5')
  assert.equal(normalizeIP('2001:0DB8:0:0:0:0:0:1/64'), '2001:db8::1')
  assert.equal(normalizeIP('2001:db8::1'), '2001:db8::1')
})

test('ipMismatch: matches any reported address, ignores prefixes, silent when unknown', () => {
  const h: HostInfo = { addresses: [{ interface: 'eth0', address: '192.168.1.106/24' }, { interface: 'eth0', address: '2001:db8::1/64' }] }
  assert.equal(ipMismatch('192.168.1.106', h), '')
  assert.equal(ipMismatch('2001:0db8:0:0:0:0:0:1', h), '')
  assert.match(ipMismatch('10.0.0.1', h), /10\.0\.0\.1.*yok/)
  assert.equal(ipMismatch('192.168.1.106', { addresses: [] }), '') // bilinmiyor
  assert.equal(ipMismatch('192.168.1.106', {}), '')
  assert.equal(ipMismatch('192.168.1.106', undefined), '')
  assert.equal(ipMismatch('192.168.1.10', { addresses: [{ address: '192.168.1.106/24' }] }) !== '', true) // ön ek eşleşmesi değil, tam adres
})

test('shortMachineId shows a short prefix only', () => {
  assert.equal(shortMachineId({ machine_id_hash: 'f243e30198fd4fa5b6d1d7b78e86d093' }), 'f243e301…')
  assert.equal(shortMachineId({}), '—')
})
