// Ayarlar → Agent sürümleri → Paketler bölümünün saf mantığı (React yok; Node'un çalıştırıcısıyla birim test edilir):
// dosya adını tanıma (server'daki kalıpların aynısı: internal/agentpkg), sürüme göre gruplama ve indirme adresi.

import type { AgentPackage, AgentPackageFormat, AgentPackageUploadResult } from '../types/api.ts'

const PATTERNS: { re: RegExp; format: AgentPackageFormat; arch: (m: string) => 'amd64' | 'arm64' }[] = [
  { re: /^healthbeat-agent_(\d+\.\d+\.\d+(?:~[0-9A-Za-z.]+)?)_(amd64|arm64)\.deb$/, format: 'deb', arch: (a) => a as 'amd64' | 'arm64' },
  {
    re: /^healthbeat-agent-(\d+\.\d+\.\d+(?:~[0-9A-Za-z.]+)?)-1\.(x86_64|aarch64)\.rpm$/,
    format: 'rpm',
    arch: (a) => (a === 'x86_64' ? 'amd64' : 'arm64'),
  },
  { re: /^healthbeat-agent_(\d+\.\d+\.\d+(?:-[0-9A-Za-z.]+)?)_linux_(amd64|arm64)\.tar\.gz$/, format: 'tar.gz', arch: (a) => a as 'amd64' | 'arm64' },
]

export interface PackageNameInfo {
  version: string
  arch: 'amd64' | 'arm64'
  format: AgentPackageFormat
}

// parsePackageName, dosya adını tanır; agent paketi değilse null. Önsürüm .deb/.rpm'de '~' ile yazılır (1.2.0~rc.1).
export function parsePackageName(name: string): PackageNameInfo | null {
  for (const p of PATTERNS) {
    const m = p.re.exec(name)
    if (m) return { version: m[1].replace('~', '-'), arch: p.arch(m[2]), format: p.format }
  }
  return null
}

// splitUploadable, seçilen dosyaları yüklenebilir olanlar ve agent paketi olmayanlar diye ayırır.
export function splitUploadable<T extends { name: string }>(files: T[]): { ok: T[]; rejected: T[] } {
  const ok: T[] = []
  const rejected: T[] = []
  for (const f of files) (parsePackageName(f.name) ? ok : rejected).push(f)
  return { ok, rejected }
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
// (HB_API_PORT, varsayılan 8443). Agent'lar server'a farklı bir adresle bağlanıyorsa adresin başı ona göre değiştirilir
// (panelde açıklanır).
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

// packageKind, "deb · amd64" gibi kısa tür metnidir.
export function packageKind(p: Pick<AgentPackage, 'format' | 'arch'>): string {
  return `${FORMAT_LABEL[p.format] ?? p.format} · ${p.arch}`
}

// uploadSummary, yükleme sonucunun tek satırlık özetidir.
export function uploadSummary(results: AgentPackageUploadResult[]): string {
  const ok = results.filter((r) => r.package).length
  const failed = results.length - ok
  if (failed === 0) return `${ok} dosya yüklendi.`
  if (ok === 0) return `${failed} dosya yüklenemedi.`
  return `${ok} dosya yüklendi, ${failed} dosya yüklenemedi.`
}
