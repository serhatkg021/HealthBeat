// Bir sunucunun eşiklerini düzenlemek için saf mantık: metrik başına ya "varsayılan" (Eşikler
// sayfasının tanımladığı her şey) ya da "özel" değerler. React yok; bu yüzden Node'un çalıştırıcısıyla
// birim test edilir. Yalnızca-tip import'ları silinir; bu da dosyayı bundler olmadan yüklenebilir kılar.

import type {
  HostThresholdView,
  ContainerThreshold,
  MetricType,
  MountOverrides,
  MountThreshold,
  ThresholdConfig,
  ThresholdLevels,
  ThresholdOverrides,
} from '../types/api.ts'
import { isValidMount } from './diskSelection.ts'

export interface MetricInfo {
  type: MetricType
  label: string
  unit: string
  percent: boolean
  hint?: string
}

// Gösterim sırası; server'ın model.ThresholdMetricTypes'ını yansıtır.
export const METRICS: MetricInfo[] = [
  { type: 'cpu', label: 'CPU', unit: '%', percent: true },
  { type: 'ram', label: 'RAM', unit: '%', percent: true },
  { type: 'disk', label: 'Disk', unit: '%', percent: true, hint: 'Doluluk oranı; seçilen her disk için ayrı değerlendirilir.' },
  {
    type: 'docker_restart',
    label: 'Docker restart',
    unit: 'restart',
    percent: false,
    hint: 'Container başına kümülatif restart sayısı; container yeniden oluşturulunca sıfırlanır.',
  },
]

export const metricInfo = (type: MetricType): MetricInfo => METRICS.find((m) => m.type === type)!

export type Mode = 'default' | 'custom'

// Sayı girdileri metin olarak tutulur; böylece yarım yazılmış bir değer imlecin altında yeniden yazılmaz.
export interface Draft {
  mode: Mode
  warning: string
  critical: string
}

export type Drafts = Record<MetricType, Draft>
export type Defaults = Partial<Record<MetricType, ThresholdLevels>>

const emptyDraft = (): Draft => ({ mode: 'default', warning: '', critical: '' })

// Her metrik "varsayılan"da — yeni bir sunucunun başladığı yer.
export function defaultDrafts(): Drafts {
  return { cpu: emptyDraft(), ram: emptyDraft(), disk: emptyDraft(), docker_restart: emptyDraft() }
}

export function draftsFromServer(views: HostThresholdView[]): Drafts {
  const drafts = defaultDrafts()
  for (const v of views) {
    if (v.custom) {
      drafts[v.metric_type] = { mode: 'custom', warning: String(v.custom.warning_level), critical: String(v.custom.critical_level) }
    }
  }
  return drafts
}

export function defaultsFromServer(views: HostThresholdView[]): Defaults {
  const out: Defaults = {}
  for (const v of views) if (v.default) out[v.metric_type] = v.default
  return out
}

// `orgId` sunucusunun eşik listesinden varsayılan olarak ne aldığı: en yakın üst organizasyona doğru yürüyen zincirde
// (kendisi, üst şirketi, onun üst şirketi…) ilk bulunan değer, hiçbiri yoksa global olan. Sunucu henüz yokken
// (sihirbaz), server'a sorulamadığında kullanılır; server'ın DefaultsFor'uyla eşleşir. `parents`, organizasyon kimliğinden
// üst şirket kimliğine eşlemedir (bkz. parentMap).
export function effectiveDefaults(list: ThresholdConfig[], orgId: string | undefined, parents: ParentMap = new Map()): Defaults {
  const out: Defaults = {}
  for (const m of METRICS) {
    const found = defaultSource(list, orgId, parents, m.type)
    if (found) out[m.type] = found.levels
  }
  return out
}

export type ParentMap = ReadonlyMap<string, string | undefined>

// Bir organizasyondan köke doğru zincir: [kendisi, üst şirketi, ...]. Döngüye karşı korumalı (server zaten engeller).
export function orgChain(orgId: string | undefined, parents: ParentMap): string[] {
  const chain: string[] = []
  for (let id = orgId; id !== undefined && !chain.includes(id); id = parents.get(id)) chain.push(id)
  return chain
}

export interface DefaultSource {
  levels: ThresholdLevels
  // Değerin geldiği organizasyon; undefined = global varsayılan.
  fromOrganizationId?: string
}

// Bir metrik için geçerli varsayılan ve nereden geldiği; hiçbir yerde tanımlı değilse undefined (alert üretilmez).
export function defaultSource(list: ThresholdConfig[], orgId: string | undefined, parents: ParentMap, metric: MetricType): DefaultSource | undefined {
  const levelsOf = (t: ThresholdConfig): ThresholdLevels => ({ warning_level: t.warning_level, critical_level: t.critical_level })
  for (const id of orgChain(orgId, parents)) {
    const t = list.find((x) => x.metric_type === metric && x.organization_id === id)
    if (t) return { levels: levelsOf(t), fromOrganizationId: id }
  }
  const global = list.find((x) => x.metric_type === metric && !x.organization_id)
  return global ? { levels: levelsOf(global) } : undefined
}

const MAX_RESTART_LEVEL = 1_000_000

function parseLevel(text: string): number | null {
  if (text.trim() === '') return null
  const n = Number(text)
  return Number.isFinite(n) ? n : null
}

// Server'ın model.ValidateThresholdLevels'ını yansıtır; yetkili olan server'dır.
export function validateDraft(type: MetricType, draft: Draft): string | null {
  if (draft.mode === 'default') return null
  const info = metricInfo(type)
  const warning = parseLevel(draft.warning)
  const critical = parseLevel(draft.critical)
  if (warning === null || critical === null) return 'Uyarı ve kritik seviyesi sayı olarak girilmeli.'
  if (warning < 0 || critical < 0) return 'Seviyeler negatif olamaz.'
  if (warning > critical) return 'Uyarı seviyesi kritik seviyeden büyük olamaz.'
  if (info.percent && critical > 100) return 'Yüzde değerleri en fazla 100 olabilir.'
  if (!info.percent && critical > MAX_RESTART_LEVEL) return `Restart sayısı en fazla ${MAX_RESTART_LEVEL} olabilir.`
  return null
}

// Sorunu olan metrik başına bir mesaj, metriğin adıyla öneklenmiş.
export function validateDrafts(drafts: Drafts): string[] {
  const errors: string[] = []
  for (const m of METRICS) {
    const err = validateDraft(m.type, drafts[m.type])
    if (err) errors.push(`${m.label}: ${err}`)
  }
  return errors
}

// İstek gövdesi. Her metrik her zaman gönderilir (null = varsayılan); böylece "varsayılan"a
// geri dönmek önceden saklanmış özel bir değeri de kaldırır.
export function toOverrides(drafts: Drafts): ThresholdOverrides {
  const out: ThresholdOverrides = {}
  for (const m of METRICS) {
    const d = drafts[m.type]
    out[m.type] = d.mode === 'custom' ? { warning_level: Number(d.warning), critical_level: Number(d.critical) } : null
  }
  return out
}

// Yalnızca özel değer taşıyan metrikler — yeni bir sunucunun göndermesi gereken.
export function customOnly(drafts: Drafts): ThresholdOverrides {
  const out: ThresholdOverrides = {}
  for (const m of METRICS) {
    if (drafts[m.type].mode === 'custom') {
      out[m.type] = { warning_level: Number(drafts[m.type].warning), critical_level: Number(drafts[m.type].critical) }
    }
  }
  return out
}

export function isDirty(a: Drafts, b: Drafts): boolean {
  return METRICS.some((m) => {
    const x = a[m.type]
    const y = b[m.type]
    if (x.mode !== y.mode) return true
    return x.mode === 'custom' && (x.warning.trim() !== y.warning.trim() || x.critical.trim() !== y.critical.trim())
  })
}

export function formatLevels(type: MetricType, levels: ThresholdLevels): string {
  const unit = metricInfo(type).unit
  return `uyarı ${levels.warning_level} ${unit} / kritik ${levels.critical_level} ${unit}`
}

// Bir özet için tek satır: hangi değerin geçerli olduğunu ve — "varsayılan" için — şu an ne olduğunu söyler.
export function describeDraft(type: MetricType, draft: Draft, defaults: Defaults): string {
  if (draft.mode === 'custom') return `Özel: ${formatLevels(type, { warning_level: Number(draft.warning), critical_level: Number(draft.critical) })}`
  const d = defaults[type]
  return d ? `Varsayılan: ${formatLevels(type, d)}` : 'Varsayılan (tanımlı değil — bu metrik için alert üretilmez)'
}

// Bir metriği "özel"e geçirmek varsayılan değerlerden başlar; böylece kişi sıfırdan yazmak yerine
// düzenler. Geri dönmek, kazara olduysa diye yazılanı korur.
export function withMode(drafts: Drafts, type: MetricType, mode: Mode, defaults: Defaults): Drafts {
  const current = drafts[type]
  if (mode === current.mode) return drafts
  const next: Draft = { ...current, mode }
  if (mode === 'custom' && current.warning === '' && current.critical === '') {
    const d = defaults[type]
    if (d) {
      next.warning = String(d.warning_level)
      next.critical = String(d.critical_level)
    }
  }
  return { ...drafts, [type]: next }
}

// ---- mount başına disk eşikleri -------------------------------------------------------------
// MountDrafts içindeki bir mount'un kendi değerleri vardır; orada olmayan bir mount sunucunun disk
// eşiğini izler. Yani "kaldır", bir mount'u sunucunun değerine geri koyan şeydir.

export interface MountDraft {
  warning: string
  critical: string
}

export type MountDrafts = Record<string, MountDraft>

const MAX_MOUNTS = 64

export function mountDraftsFromServer(list: MountThreshold[]): MountDrafts {
  const out: MountDrafts = {}
  for (const m of list) out[m.mount] = { warning: String(m.custom.warning_level), critical: String(m.custom.critical_level) }
  return out
}

export function validateMountDraft(draft: MountDraft): string | null {
  return validateDraft('disk', { mode: 'custom', ...draft })
}

export function validateContainerDraft(draft: MountDraft): string | null {
  return validateDraft('docker_restart', { mode: 'custom', ...draft })
}

// Sorunu olan mount başına bir mesaj, mount ile öneklenmiş.
export function validateMountDrafts(drafts: MountDrafts): string[] {
  const errors: string[] = []
  const mounts = Object.keys(drafts)
  if (mounts.length > MAX_MOUNTS) errors.push(`En fazla ${MAX_MOUNTS} mount için özel eşik girilebilir.`)
  for (const mount of mounts.sort()) {
    const err = validateMountDraft(drafts[mount])
    if (err) errors.push(`Disk ${mount}: ${err}`)
  }
  return errors
}

// Yalnızca kendi değerleri olan mount'lar — yeni bir sunucunun gönderdiği.
export function customMounts(drafts: MountDrafts): MountOverrides {
  const out: MountOverrides = {}
  for (const [mount, d] of Object.entries(drafts)) out[mount] = { warning_level: Number(d.warning), critical_level: Number(d.critical) }
  return out
}

// Mevcut bir sunucu için güncelleme: değeri olan ve kaldırılan mount'lar null olur (sunucunun disk
// eşiğine geri döner), gerisi değerlerini taşır.
export function toMountOverrides(saved: MountDrafts, draft: MountDrafts): MountOverrides {
  const out: MountOverrides = {}
  for (const mount of Object.keys(saved)) if (!(mount in draft)) out[mount] = null
  return { ...out, ...customMounts(draft) }
}

export function isMountsDirty(a: MountDrafts, b: MountDrafts): boolean {
  const keys = new Set([...Object.keys(a), ...Object.keys(b)])
  for (const k of keys) {
    const x = a[k]
    const y = b[k]
    if (!x || !y) return true
    if (x.warning.trim() !== y.warning.trim() || x.critical.trim() !== y.critical.trim()) return true
  }
  return false
}

// "Mount ekle"ye yazılanı ayrıştırır; sonuç ya kullanılabilir bir yol ya da bir mesajdır.
export function parseMountToAdd(text: string, existing: MountDrafts): { mount: string } | { error: string } {
  const mount = text.trim()
  if (mount === '') return { error: 'Bir disk yolu girin.' }
  if (!isValidMount(mount)) return { error: 'Disk yolu mutlak olmalı ("/" ile başlamalı).' }
  if (mount in existing) return { error: `${mount} için zaten bir değer var.` }
  if (Object.keys(existing).length >= MAX_MOUNTS) return { error: `En fazla ${MAX_MOUNTS} mount eklenebilir.` }
  return { mount }
}

// Yeni bir mount sunucunun disk değerlerinden (biliniyorsa) başlar; böylece kişi sıfırdan yazmak yerine düzenler.
export function addMount(drafts: MountDrafts, mount: string, from?: ThresholdLevels): MountDrafts {
  return {
    ...drafts,
    [mount]: from ? { warning: String(from.warning_level), critical: String(from.critical_level) } : { warning: '', critical: '' },
  }
}

export function removeMount(drafts: MountDrafts, mount: string): MountDrafts {
  const { [mount]: _removed, ...rest } = drafts
  return rest
}

// Özet satırları, mount başına bir tane, yola göre sıralı.
export function describeMounts(drafts: MountDrafts): string[] {
  return Object.keys(drafts)
    .sort()
    .map((mount) => {
      const d = drafts[mount]
      return `Disk ${mount} — Özel: ${formatLevels('disk', { warning_level: Number(d.warning), critical_level: Number(d.critical) })}`
    })
}

// ---- container başına docker_restart eşikleri ----------------------------------------------
// Mount eşikleriyle aynı mantık: listedeki container kendi değerini kullanır, listede olmayan
// sunucunun docker_restart değerini izler. Taslak biçimi mount taslaklarıyla aynıdır.

const MAX_CONTAINER_NAME_BYTES = 255

export function containerDraftsFromServer(list: ContainerThreshold[]): MountDrafts {
  const out: MountDrafts = {}
  for (const c of list) out[c.container] = { warning: String(c.custom.warning_level), critical: String(c.custom.critical_level) }
  return out
}

export function validateContainerDrafts(drafts: MountDrafts): string[] {
  const errors: string[] = []
  const names = Object.keys(drafts)
  if (names.length > MAX_MOUNTS) errors.push(`En fazla ${MAX_MOUNTS} container için özel eşik girilebilir.`)
  for (const name of names.sort()) {
    const err = validateContainerDraft(drafts[name])
    if (err) errors.push(`Docker restart ${name}: ${err}`)
  }
  return errors
}

// "Container ekle"ye yazılan adı denetler: kullanılabilir bir ad ya da bir mesaj döner.
export function parseContainerToAdd(text: string, existing: MountDrafts): { mount: string } | { error: string } {
  const name = text.trim()
  if (name === '') return { error: 'Bir container adı girin.' }
  // eslint-disable-next-line no-control-regex
  if (/[\u0000-\u001f\u007f-\u009f]/.test(name)) return { error: 'Container adı kontrol karakteri içeremez.' }
  if (new TextEncoder().encode(name).length > MAX_CONTAINER_NAME_BYTES) return { error: `Container adı ${MAX_CONTAINER_NAME_BYTES} bayttan uzun olamaz.` }
  if (name in existing) return { error: `${name} için zaten bir değer var.` }
  if (Object.keys(existing).length >= MAX_MOUNTS) return { error: `En fazla ${MAX_MOUNTS} container eklenebilir.` }
  return { mount: name }
}

// Sunucunun docker_restart için fiilen kullandığı değerler (özel, geçerliyse; yoksa varsayılan).
export function serverLevels(type: MetricType, draft: Draft, defaults: Defaults): ThresholdLevels | undefined {
  if (draft.mode === 'custom') {
    if (validateDraft(type, draft) !== null) return undefined
    return { warning_level: Number(draft.warning), critical_level: Number(draft.critical) }
  }
  return defaults[type]
}

export function describeContainers(drafts: MountDrafts): string[] {
  return Object.keys(drafts)
    .sort()
    .map((name) => {
      const d = drafts[name]
      return `Docker restart ${name} — Özel: ${formatLevels('docker_restart', { warning_level: Number(d.warning), critical_level: Number(d.critical) })}`
    })
}
