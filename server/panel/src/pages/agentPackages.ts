// Ayarlar → Agent sürümleri → Agent paketleri bölümünün saf mantığı (React yok; Node'un çalıştırıcısıyla birim test
// edilir): yüklenecek dosyanın biçimini ilk baytlarından bulma, sürüm ve mimariyi addan tahmin etme, yükleme
// satırlarını doğrulama, sürüme göre gruplama ve indirme adresi. Biçim ve ad kuralları server'dakinin aynısıdır
// (internal/agentpkg).

import type { AgentPackage, AgentPackageArch, AgentPackageFormat } from '../types/api.ts'

// MAX_PACKAGE_BYTES, tek bir paket dosyasının üst sınırıdır (server: maxAgentPackageBytes).
export const MAX_PACKAGE_BYTES = 50 * 1024 * 1024
// HEADER_BYTES, biçimi tanımak için okunan bayt sayısıdır.
export const HEADER_BYTES = 8

const MAGIC: { format: AgentPackageFormat; bytes: number[] }[] = [
  { format: 'deb', bytes: [...'!<arch>\n'].map((c) => c.charCodeAt(0)) },
  { format: 'rpm', bytes: [0xed, 0xab, 0xee, 0xdb] },
  { format: 'tar.gz', bytes: [0x1f, 0x8b] },
]

// detectFormat, dosyanın ilk baytlarından biçimini bulur: .deb bir ar arşividir, .rpm'in kendi imzası vardır, tarball
// gzip'tir. Tanınmazsa null.
export function detectFormat(header: Uint8Array): AgentPackageFormat | null {
  for (const m of MAGIC) {
    if (header.length >= m.bytes.length && m.bytes.every((b, i) => header[i] === b)) return m.format
  }
  return null
}

const VERSION_RE = /^\d+\.\d+\.\d+(?:-[0-9A-Za-z.]+)?$/

// isValidVersion, sürümün kabul edilen biçimde olup olmadığıdır: 1.2.0 ya da 1.2.0-rc.1.
export const isValidVersion = (v: string) => v.length <= 64 && VERSION_RE.test(v)

const ARCH_ALIASES: Record<string, AgentPackageArch> = { amd64: 'amd64', x86_64: 'amd64', 'x86-64': 'amd64', arm64: 'arm64', aarch64: 'arm64' }

export interface NameGuess {
  version: string
  arch: AgentPackageArch | ''
}

// guessFromName, dosya adından sürümü ve mimariyi tahmin eder; bulunamayan alan boş kalır. Release adlarını
// (healthbeat-agent_1.2.0~rc.1_amd64.deb, healthbeat-agent-1.1.0-1.x86_64.rpm) ve "agent-1.1.0-arm64.deb" gibi elle
// verilmiş adları tanır. Önsürüm yalnızca rc/beta/alpha ile başlıyorsa alınır (rpm'deki "-1" paket sürümüdür).
export function guessFromName(name: string): NameGuess {
  const stem = name.replace(/\.(deb|rpm|tar\.gz|tgz)$/i, '')
  const v = /(?:^|[^\d.])(\d+\.\d+\.\d+)(?!\.?\d)(?:[~-]((?:rc|beta|alpha)[0-9A-Za-z.]*))?/i.exec(stem)
  const version = v ? (v[2] ? `${v[1]}-${v[2].replace(/\.+$/, '')}` : v[1]) : ''
  const a = /(amd64|x86_64|x86-64|arm64|aarch64)/i.exec(stem)
  return { version: isValidVersion(version) ? version : '', arch: a ? ARCH_ALIASES[a[1].toLowerCase()] : '' }
}

// releaseFileName, paketin server'da alacağı adıdır (server: agentpkg.FileName). Önsürüm .deb ve .rpm'de '~' ile yazılır.
export function releaseFileName(p: { version: string; arch: AgentPackageArch; format: AgentPackageFormat }): string {
  const pkgVersion = p.version.replace('-', '~')
  switch (p.format) {
    case 'deb':
      return `healthbeat-agent_${pkgVersion}_${p.arch}.deb`
    case 'rpm':
      return `healthbeat-agent-${pkgVersion}-1.${p.arch === 'amd64' ? 'x86_64' : 'aarch64'}.rpm`
    case 'tar.gz':
      return `healthbeat-agent_${p.version}_linux_${p.arch}.tar.gz`
  }
}

export type DraftStatus = 'pending' | 'uploading' | 'done' | 'failed'

// UploadDraft, yükleme listesindeki bir dosyadır. format null ise agent paketi değildir ve yüklenmez.
export interface UploadDraft {
  key: string
  name: string
  size: number
  format: AgentPackageFormat | null
  version: string
  arch: AgentPackageArch | ''
  status: DraftStatus
  // Server'ın verdiği hata (yükleme başarısızsa) ve alan hataları.
  error?: string
  fieldErrors?: Partial<Record<'version' | 'arch', string>>
}

// newDraft, seçilen dosyadan yükleme satırı kurar: biçim ilk baytlardan, sürüm ve mimari addan.
export function newDraft(key: string, name: string, size: number, header: Uint8Array): UploadDraft {
  return { key, name, size, format: detectFormat(header), ...guessFromName(name), status: 'pending' }
}

export interface DraftProblems {
  version?: string
  arch?: string
  file?: string
}

const variantKey = (p: { version: string; arch: string; format: string | null }) => `${p.version.trim()}|${p.arch}|${p.format}`

// draftProblems, yüklenecek satırların sorunlarıdır (anahtar → sorunlar). Agent paketi olmayan ve yüklenmiş satırlar
// denetlenmez. Aynı sürüm + mimari + biçim listede iki kez seçilemez ve zaten yüklü olamaz.
export function draftProblems(drafts: UploadDraft[], existing: Pick<AgentPackage, 'version' | 'arch' | 'format'>[]): Record<string, DraftProblems> {
  const out: Record<string, DraftProblems> = {}
  const uploaded = new Set(existing.map(variantKey))
  const seen = new Map<string, number>()
  const active = drafts.filter((d) => d.format && d.status !== 'done')
  for (const d of active) seen.set(variantKey(d), (seen.get(variantKey(d)) ?? 0) + 1)
  for (const d of active) {
    const p: DraftProblems = {}
    const version = d.version.trim()
    if (!version) p.version = 'sürüm gerekli'
    else if (!isValidVersion(version)) p.version = '1.2.0 ya da 1.2.0-rc.1 biçiminde olmalı'
    else if (d.fieldErrors?.version) p.version = d.fieldErrors.version
    if (!d.arch) p.arch = 'mimari seçin'
    else if (d.fieldErrors?.arch) p.arch = d.fieldErrors.arch
    if (d.size > MAX_PACKAGE_BYTES) p.file = `dosya en fazla ${MAX_PACKAGE_BYTES / 1024 / 1024} MB olabilir`
    else if (!p.version && !p.arch) {
      if (uploaded.has(variantKey(d))) p.file = 'bu sürümün bu paketi zaten yüklü'
      else if ((seen.get(variantKey(d)) ?? 0) > 1) p.file = 'aynı sürüm, mimari ve biçim listede birden fazla'
    }
    if (p.version || p.arch || p.file) out[d.key] = p
  }
  return out
}

// uploadable, yüklenmeye hazır satırlardır: agent paketi, henüz yüklenmemiş ve sorunsuz.
export function uploadable(drafts: UploadDraft[], problems: Record<string, DraftProblems>): UploadDraft[] {
  return drafts.filter((d) => d.format && d.status !== 'done' && !problems[d.key])
}

// draftSummary, yükleme sonucunun tek satırlık özetidir; yükleme bitmeden null.
export function draftSummary(drafts: UploadDraft[]): string | null {
  const done = drafts.filter((d) => d.status === 'done').length
  const failed = drafts.filter((d) => d.status === 'failed').length
  if (done + failed === 0) return null
  if (failed === 0) return `${done} dosya yüklendi.`
  if (done === 0) return `${failed} dosya yüklenemedi.`
  return `${done} dosya yüklendi, ${failed} dosya yüklenemedi.`
}

export interface VersionGroup {
  version: string
  packages: AgentPackage[]
  downloads: number
}

// groupByVersion, paketleri sürüme göre gruplar; server'ın sırası (en yeni sürüm önce) korunur.
export function groupByVersion(packages: AgentPackage[]): VersionGroup[] {
  const groups: VersionGroup[] = []
  for (const p of packages) {
    let g = groups.find((x) => x.version === p.version)
    if (!g) {
      g = { version: p.version, packages: [], downloads: 0 }
      groups.push(g)
    }
    g.packages.push(p)
    g.downloads += p.download_count
  }
  return groups
}

// downloadBase, indirme adresinin kökü: panelin yapılandırılmış API adresi, yoksa panelin alan adında API portu
// (HB_API_PORT, varsayılan 8443).
export function downloadBase(apiBaseUrl: string | undefined, hostname: string, apiPort?: number): string {
  const base = (apiBaseUrl ?? '').trim().replace(/\/+$/, '')
  if (base) return base
  const host = hostname.includes(':') && !hostname.startsWith('[') ? `[${hostname}]` : hostname // IPv6
  const port = apiPort && Number.isInteger(apiPort) && apiPort > 0 ? apiPort : 8443
  return port === 443 ? `https://${host}` : `https://${host}:${port}`
}

// downloadUrl, bir paketin tam indirme adresidir.
export function downloadUrl(base: string, path: string, p: Pick<AgentPackage, 'version' | 'filename'>): string {
  return `${base}${path}${encodeURIComponent(p.version)}/${encodeURIComponent(p.filename)}`
}

const FORMAT_LABEL: Record<AgentPackageFormat, string> = { deb: '.deb', rpm: '.rpm', 'tar.gz': '.tar.gz' }

// formatLabel, ".deb" gibi biçim metnidir.
export const formatLabel = (f: AgentPackageFormat) => FORMAT_LABEL[f] ?? f

// packageKind, ".deb · amd64" gibi kısa tür metnidir.
export function packageKind(p: Pick<AgentPackage, 'format' | 'arch'>): string {
  return `${formatLabel(p.format)} · ${p.arch}`
}
