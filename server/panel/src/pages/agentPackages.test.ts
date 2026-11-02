import assert from 'node:assert/strict'
import { test } from 'node:test'
import type { AgentPackage } from '../types/api.ts'
import {
  MAX_PACKAGE_BYTES,
  detectFormat,
  downloadBase,
  downloadUrl,
  draftProblems,
  draftSummary,
  groupByVersion,
  guessFromName,
  isValidVersion,
  newDraft,
  packageKind,
  releaseFileName,
  uploadable,
  type UploadDraft,
} from './agentPackages.ts'

const bytes = (s: string | number[]) => new Uint8Array(typeof s === 'string' ? [...s].map((c) => c.charCodeAt(0)) : s)
const DEB = bytes('!<arch>\ndebian')
const RPM = bytes([0xed, 0xab, 0xee, 0xdb, 3, 0])
const TGZ = bytes([0x1f, 0x8b, 8, 0])

test('detectFormat reads the file header', () => {
  assert.equal(detectFormat(DEB), 'deb')
  assert.equal(detectFormat(RPM), 'rpm')
  assert.equal(detectFormat(TGZ), 'tar.gz')
  assert.equal(detectFormat(bytes('PK\x03\x04')), null) // zip
  assert.equal(detectFormat(bytes('!<arch>')), null) // eksik
  assert.equal(detectFormat(bytes('')), null)
})

test('guessFromName recognizes release and hand-made names', () => {
  const cases: [string, string, string][] = [
    ['healthbeat-agent_1.1.0_amd64.deb', '1.1.0', 'amd64'],
    ['healthbeat-agent-1.1.0-1.aarch64.rpm', '1.1.0', 'arm64'],
    ['healthbeat-agent-1.1.0-1.x86_64.rpm', '1.1.0', 'amd64'],
    ['healthbeat-agent_1.1.0_linux_arm64.tar.gz', '1.1.0', 'arm64'],
    ['healthbeat-agent_1.2.0~rc.1_amd64.deb', '1.2.0-rc.1', 'amd64'],
    ['healthbeat-agent-1.2.0~rc.1-1.x86_64.rpm', '1.2.0-rc.1', 'amd64'],
    ['healthbeat-agent_1.2.0-rc.1_linux_amd64.tar.gz', '1.2.0-rc.1', 'amd64'],
    ['agent-1.3.0-ARM64.deb', '1.3.0', 'arm64'],
    ['hb agent 2.0.0 (x86-64).rpm', '2.0.0', 'amd64'],
    ['agent_1.2.0-rc.2.deb', '1.2.0-rc.2', ''],
    ['agent_1.1.0-linux_amd64.tgz', '1.1.0', 'amd64'], // "linux" önsürüm sayılmaz
    ['agent.deb', '', ''],
    ['agent-1.1.deb', '', ''],
    ['10.1.1.1-agent.deb', '', ''], // IP benzeri: sürüm değil
  ]
  for (const [name, version, arch] of cases) assert.deepEqual(guessFromName(name), { version, arch }, name)
})

test('isValidVersion', () => {
  for (const v of ['1.1.0', '10.20.30', '1.2.0-rc.1', '1.2.0-beta']) assert.ok(isValidVersion(v), v)
  for (const v of ['', '1.1', 'v1.1.0', '1.2.0-rc-1', '1.1.0+build', '1.1.0 ', `1.1.0-${'a'.repeat(60)}`]) assert.ok(!isValidVersion(v), v)
})

test('releaseFileName matches the server', () => {
  assert.equal(releaseFileName({ version: '1.1.0', arch: 'amd64', format: 'deb' }), 'healthbeat-agent_1.1.0_amd64.deb')
  assert.equal(releaseFileName({ version: '1.1.0', arch: 'arm64', format: 'rpm' }), 'healthbeat-agent-1.1.0-1.aarch64.rpm')
  assert.equal(releaseFileName({ version: '1.2.0-rc.1', arch: 'amd64', format: 'deb' }), 'healthbeat-agent_1.2.0~rc.1_amd64.deb')
  assert.equal(releaseFileName({ version: '1.2.0-rc.1', arch: 'amd64', format: 'rpm' }), 'healthbeat-agent-1.2.0~rc.1-1.x86_64.rpm')
  assert.equal(releaseFileName({ version: '1.2.0-rc.1', arch: 'arm64', format: 'tar.gz' }), 'healthbeat-agent_1.2.0-rc.1_linux_arm64.tar.gz')
})

test('newDraft fills what it can', () => {
  assert.deepEqual(newDraft('k', 'agent-1.1.0-amd64.bin', 10, DEB), {
    key: 'k',
    name: 'agent-1.1.0-amd64.bin',
    size: 10,
    format: 'deb',
    version: '1.1.0',
    arch: 'amd64',
    status: 'pending',
  })
  assert.equal(newDraft('k', 'SHA256SUMS', 10, bytes('abc')).format, null)
})

const draft = (key: string, over: Partial<UploadDraft> = {}): UploadDraft => ({
  key,
  name: `${key}.deb`,
  size: 10,
  format: 'deb',
  version: '1.1.0',
  arch: 'amd64',
  status: 'pending',
  ...over,
})

test('draftProblems validates rows, duplicates and existing packages', () => {
  const drafts = [
    draft('ok', { arch: 'arm64' }),
    draft('noversion', { version: '' }),
    draft('badversion', { version: '1.1' }),
    draft('noarch', { arch: '' }),
    draft('dup1', { version: '1.2.0', format: 'rpm' }),
    draft('dup2', { version: ' 1.2.0 ', format: 'rpm' }),
    draft('exists'),
    draft('big', { version: '1.3.0', size: MAX_PACKAGE_BYTES + 1 }),
    draft('server', { version: '1.4.0', fieldErrors: { arch: 'server hatası' } }),
    draft('skip', { format: null, version: '' }),
    draft('done', { version: '', status: 'done' }),
  ]
  const problems = draftProblems(drafts, [{ version: '1.1.0', arch: 'amd64', format: 'deb' }])
  assert.deepEqual(problems, {
    noversion: { version: 'sürüm gerekli' },
    badversion: { version: '1.2.0 ya da 1.2.0-rc.1 biçiminde olmalı' },
    noarch: { arch: 'mimari seçin' },
    dup1: { file: 'aynı sürüm, mimari ve biçim listede birden fazla' },
    dup2: { file: 'aynı sürüm, mimari ve biçim listede birden fazla' },
    exists: { file: 'bu sürümün bu paketi zaten yüklü' },
    big: { file: 'dosya en fazla 50 MB olabilir' },
    server: { arch: 'server hatası' },
  })
  assert.deepEqual(
    uploadable(drafts, problems).map((d) => d.key),
    ['ok'],
  )
})

test('draftSummary', () => {
  assert.equal(draftSummary([draft('a')]), null)
  assert.equal(draftSummary([draft('a', { status: 'done' })]), '1 dosya yüklendi.')
  assert.equal(draftSummary([draft('a', { status: 'failed' })]), '1 dosya yüklenemedi.')
  assert.equal(draftSummary([draft('a', { status: 'done' }), draft('b', { status: 'failed' })]), '1 dosya yüklendi, 1 dosya yüklenemedi.')
})

const pkg = (version: string, filename: string, downloads = 0): AgentPackage => ({
  id: filename,
  version,
  filename,
  original_filename: filename,
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

test('downloadUrl and packageKind', () => {
  assert.equal(
    downloadUrl('https://hb:8443', '/agent/download/', { version: '1.2.0-rc.1', filename: 'healthbeat-agent_1.2.0~rc.1_amd64.deb' }),
    'https://hb:8443/agent/download/1.2.0-rc.1/healthbeat-agent_1.2.0~rc.1_amd64.deb',
  )
  assert.equal(packageKind({ format: 'tar.gz', arch: 'arm64' }), '.tar.gz · arm64')
})
