import assert from 'node:assert/strict'
import { test } from 'node:test'
import type { AgentPackage } from '../types/api.ts'
import { downloadBase, downloadUrl, groupByVersion, packageKind, parsePackageName, splitUploadable, uploadSummary } from './agentPackages.ts'

test('parsePackageName recognizes the release packages', () => {
  assert.deepEqual(parsePackageName('healthbeat-agent_1.1.0_amd64.deb'), { version: '1.1.0', arch: 'amd64', format: 'deb' })
  assert.deepEqual(parsePackageName('healthbeat-agent-1.1.0-1.aarch64.rpm'), { version: '1.1.0', arch: 'arm64', format: 'rpm' })
  assert.deepEqual(parsePackageName('healthbeat-agent-1.1.0-1.x86_64.rpm'), { version: '1.1.0', arch: 'amd64', format: 'rpm' })
  assert.deepEqual(parsePackageName('healthbeat-agent_1.1.0_linux_arm64.tar.gz'), { version: '1.1.0', arch: 'arm64', format: 'tar.gz' })
  assert.deepEqual(parsePackageName('healthbeat-agent_1.2.0~rc.1_amd64.deb'), { version: '1.2.0-rc.1', arch: 'amd64', format: 'deb' })
  assert.deepEqual(parsePackageName('healthbeat-agent_1.2.0-rc.1_linux_amd64.tar.gz'), { version: '1.2.0-rc.1', arch: 'amd64', format: 'tar.gz' })
})

test('parsePackageName rejects everything else', () => {
  for (const name of [
    'SHA256SUMS',
    'SHA256SUMS.asc',
    'healthbeat-agent_1.1.0_linux_amd64',
    'healthbeat-agent_1.1.0_i386.deb',
    'healthbeat-agent_1.1.0-rc.1_amd64.deb',
    'healthbeat-server_2.2.0.tar.gz',
    'healthbeat-agent_1.1.0_amd64.deb (1)',
  ]) {
    assert.equal(parsePackageName(name), null, name)
  }
})

test('splitUploadable separates non-packages before uploading', () => {
  const { ok, rejected } = splitUploadable([{ name: 'healthbeat-agent_1.1.0_amd64.deb' }, { name: 'SHA256SUMS' }])
  assert.deepEqual(
    ok.map((f) => f.name),
    ['healthbeat-agent_1.1.0_amd64.deb'],
  )
  assert.deepEqual(
    rejected.map((f) => f.name),
    ['SHA256SUMS'],
  )
})

const pkg = (version: string, filename: string, downloads = 0): AgentPackage => ({
  id: filename,
  version,
  filename,
  arch: 'amd64',
  format: 'deb',
  size_bytes: 1,
  sha256: 'a'.repeat(64),
  uploaded_at: '2026-11-02T10:00:00Z',
  download_count: downloads,
})

test('groupByVersion keeps the server order and sums downloads', () => {
  const groups = groupByVersion([pkg('1.2.0', 'a', 2), pkg('1.2.0', 'b', 3), pkg('1.1.0', 'c', 1)])
  assert.deepEqual(
    groups.map((g) => [g.version, g.packages.length, g.downloads]),
    [
      ['1.2.0', 2, 5],
      ['1.1.0', 1, 1],
    ],
  )
  assert.deepEqual(groupByVersion([]), [])
})

test('downloadBase prefers the configured API address, else the API port on the panel host', () => {
  assert.equal(downloadBase('https://api.example.com:9443/', 'panel.example.com'), 'https://api.example.com:9443')
  assert.equal(downloadBase('', 'panel.example.com'), 'https://panel.example.com:8443')
  assert.equal(downloadBase(undefined, '10.0.0.5'), 'https://10.0.0.5:8443')
  assert.equal(downloadBase('', '::1'), 'https://[::1]:8443')
  assert.equal(downloadBase('', 'localhost', 18443), 'https://localhost:18443')
  assert.equal(downloadBase('', 'hb.example.com', 443), 'https://hb.example.com')
})

test('downloadUrl', () => {
  assert.equal(
    downloadUrl('https://hb:8443', '/agent/download/', { version: '1.2.0-rc.1', filename: 'healthbeat-agent_1.2.0~rc.1_amd64.deb' }),
    'https://hb:8443/agent/download/1.2.0-rc.1/healthbeat-agent_1.2.0~rc.1_amd64.deb',
  )
})

test('labels and summaries', () => {
  assert.equal(packageKind({ format: 'tar.gz', arch: 'arm64' }), '.tar.gz · arm64')
  assert.equal(uploadSummary([{ filename: 'a', package: pkg('1', 'a') }]), '1 dosya yüklendi.')
  assert.equal(uploadSummary([{ filename: 'a', error: 'x' }]), '1 dosya yüklenemedi.')
  assert.equal(uploadSummary([{ filename: 'a', package: pkg('1', 'a') }, { filename: 'b', error: 'x' }]), '1 dosya yüklendi, 1 dosya yüklenemedi.')
})
