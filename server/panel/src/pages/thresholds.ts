// Bir sunucunun eşiklerini düzenlemek için saf mantık: metrik başına ya "varsayılan" (Eşikler
// sayfasının tanımladığı her şey) ya da "özel" değerler. React yok; bu yüzden Node'un çalıştırıcısıyla
// birim test edilir. Yalnızca-tip import'ları silinir; bu da dosyayı bundler olmadan yüklenebilir kılar.

import type {
  HostThresholdView,
  MetricPoint,
  Temperature,
  ContainerThreshold,
  MetricType,
  MountOverrides,
  MountThreshold,
  SubjectMetricType,
  SubjectThreshold,
  SubjectThresholdOverrides,
  ThresholdConfig,
  ThresholdLevels,
  ThresholdOverrides,
} from '../types/api.ts'
import { isValidMount } from './diskSelection.ts'
import { decimal } from './units.ts'
import { durationDraft, durationSeconds, emptyDuration, formatDuration, parseDuration, sameDuration, type DurationDraft } from './duration.ts'

export interface MetricInfo {
  type: MetricType
  label: string
  unit: string
  percent: boolean
  // Seviyelerin üst sınırı (server'ın model.ValidateThresholdLevels'ı).
  max: number
  // Süre koşulu verilebilir (protokol 4 türleri; CPU/RAM/disk/docker_restart anlık değerlendirilir).
  duration?: boolean
  hint?: string
}

const MAX_RESTART_LEVEL = 1_000_000

// Gösterim sırası; server'ın model.ThresholdMetricTypes'ını yansıtır.
export const METRICS: MetricInfo[] = [
  { type: 'cpu', label: 'CPU', unit: '%', percent: true, max: 100 },
  { type: 'ram', label: 'RAM', unit: '%', percent: true, max: 100 },
  { type: 'disk', label: 'Disk', unit: '%', percent: true, max: 100, hint: 'Doluluk oranı; seçilen her disk için ayrı değerlendirilir.' },
  {
    type: 'docker_restart',
    label: 'Docker restart',
    unit: 'restart',
    percent: false,
    max: MAX_RESTART_LEVEL,
    hint: 'Container başına kümülatif restart sayısı; container yeniden oluşturulunca sıfırlanır.',
  },
  {
    type: 'disk_latency',
    label: 'Disk gecikmesi',
    unit: 'ms',
    percent: false,
    max: 600_000,
    duration: true,
    hint: 'Bir disk işleminin ortalama süresi; her fiziksel disk için ayrı değerlendirilir.',
  },
  {
    type: 'temperature',
    label: 'Sıcaklık',
    unit: '°C',
    percent: false,
    max: 500,
    duration: true,
    hint: 'Her sensör için ayrı değerlendirilir. Donanımın bildirdiği sınırlar sunucu kapsamında sensörün yanında görünür.',
  },
  {
    type: 'service_restart',
    label: 'Servis yeniden başlatma',
    unit: 'kez',
    percent: false,
    max: MAX_RESTART_LEVEL,
    duration: true,
    hint: 'İzlenen bir servisin son 10 dakikadaki yeniden başlatma sayısı. İzlenecek servisler sunucu sayfasında seçilir.',
  },
  {
    type: 'time_offset',
    label: 'Saat farkı',
    unit: 'ms',
    percent: false,
    max: 86_400_000,
    duration: true,
    hint: 'Sunucu saatinin NTP kaynağına göre farkı (mutlak değer).',
  },
]

export const metricInfo = (type: MetricType): MetricInfo => METRICS.find((m) => m.type === type)!

// Server'ın bildiği bütün eşik türleri (model.ThresholdMetricTypes sırasıyla).
export const METRIC_TYPES: readonly MetricType[] = METRICS.map((m) => m.type)

// Süre alanının açıklaması (eşik satırlarında ortak).
export const DURATION_HINT = 'Değer bu kadar süre kesintisiz aşılınca alert açılır; boşsa hemen. Kendi değeri olan kapsam süreyi de kendisi verir.'

// Her eşik türü için bir değer.
export function perMetric<T>(f: (type: MetricType) => T): Record<MetricType, T> {
  return Object.fromEntries(METRIC_TYPES.map((m) => [m, f(m)])) as Record<MetricType, T>
}

export type Mode = 'default' | 'custom'

// Sayı girdileri metin olarak tutulur; böylece yarım yazılmış bir değer imlecin altında yeniden yazılmaz.
export interface Draft {
  mode: Mode
  warning: string
  critical: string
  // Yalnızca süre koşulu alan türlerde kullanılır.
  duration: DurationDraft
}

export type Drafts = Record<MetricType, Draft>
export type Defaults = Partial<Record<MetricType, ThresholdLevels>>

const emptyDraft = (): Draft => ({ mode: 'default', warning: '', critical: '', duration: emptyDuration() })

// Saklanan seviyelerin taslağı.
export const levelsDraft = (l: ThresholdLevels): Pick<Draft, 'warning' | 'critical' | 'duration'> => ({
  warning: String(l.warning_level),
  critical: String(l.critical_level),
  duration: durationDraft(l.duration_seconds),
})

// Geçerli bir taslağın seviyeleri; süre yalnızca süre alan türlerde ve doluysa eklenir.
export function draftLevels(type: MetricType, d: Pick<Draft, 'warning' | 'critical' | 'duration'>): ThresholdLevels {
  const out: ThresholdLevels = { warning_level: Number(d.warning), critical_level: Number(d.critical) }
  const seconds = metricInfo(type).duration ? durationSeconds(d.duration) : undefined
  if (seconds !== undefined) out.duration_seconds = seconds
  return out
}

// Her metrik "varsayılan"da — yeni bir sunucunun başladığı yer.
export function defaultDrafts(): Drafts {
  return perMetric(emptyDraft)
}

export function draftsFromServer(views: HostThresholdView[]): Drafts {
  const drafts = defaultDrafts()
  for (const v of views) {
    if (v.custom) drafts[v.metric_type] = { mode: 'custom', ...levelsDraft(v.custom) }
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
  const levelsOf = (t: ThresholdConfig): ThresholdLevels => ({
    warning_level: t.warning_level,
    critical_level: t.critical_level,
    ...(t.duration_seconds ? { duration_seconds: t.duration_seconds } : {}),
  })
  for (const id of orgChain(orgId, parents)) {
    const t = list.find((x) => x.metric_type === metric && x.organization_id === id)
    if (t) return { levels: levelsOf(t), fromOrganizationId: id }
  }
  const global = list.find((x) => x.metric_type === metric && !x.organization_id)
  return global ? { levels: levelsOf(global) } : undefined
}

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
  if (critical > info.max) {
    if (info.percent) return 'Yüzde değerleri en fazla 100 olabilir.'
    if (type === 'docker_restart') return `Restart sayısı en fazla ${MAX_RESTART_LEVEL} olabilir.`
    return `Seviye en fazla ${info.max.toLocaleString('tr-TR')} ${info.unit} olabilir.`
  }
  if (info.duration) {
    const d = parseDuration(draft.duration)
    if ('error' in d) return d.error
  }
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
    out[m.type] = d.mode === 'custom' ? draftLevels(m.type, d) : null
  }
  return out
}

// Yalnızca özel değer taşıyan metrikler — yeni bir sunucunun göndermesi gereken.
export function customOnly(drafts: Drafts): ThresholdOverrides {
  const out: ThresholdOverrides = {}
  for (const m of METRICS) {
    if (drafts[m.type].mode === 'custom') out[m.type] = draftLevels(m.type, drafts[m.type])
  }
  return out
}

// Seviyeler ya da (süre alan türlerde) süre değişti mi.
function levelsChanged(x: Pick<Draft, 'warning' | 'critical' | 'duration'>, y: Pick<Draft, 'warning' | 'critical' | 'duration'>, duration: boolean): boolean {
  if (x.warning.trim() !== y.warning.trim() || x.critical.trim() !== y.critical.trim()) return true
  return duration && !sameDuration(x.duration, y.duration)
}

export function isDirty(a: Drafts, b: Drafts): boolean {
  return METRICS.some((m) => {
    const x = a[m.type]
    const y = b[m.type]
    if (x.mode !== y.mode) return true
    return x.mode === 'custom' && levelsChanged(x, y, m.duration === true)
  })
}

export function formatLevels(type: MetricType, levels: ThresholdLevels): string {
  const unit = metricInfo(type).unit
  const text = `uyarı ${levels.warning_level} ${unit} / kritik ${levels.critical_level} ${unit}`
  return levels.duration_seconds ? `${text} · ${formatDuration(levels.duration_seconds)} boyunca` : text
}

// ---- sistem ve organizasyon tablolarının satırı -----------------------------------------------------------------
// Bu kapsamlarda "varsayılan" seçimi yok: satır ya tanımlıdır ya da değildir (kaldırınca üst kapsamdan devralınır).

export type RowDraft = Pick<Draft, 'warning' | 'critical' | 'duration'>

export const rowDraft = (saved?: ThresholdLevels): RowDraft => (saved ? levelsDraft(saved) : { warning: '', critical: '', duration: emptyDuration() })

export function rowChanged(type: MetricType, draft: RowDraft, saved?: ThresholdLevels): boolean {
  if (!saved) return draft.warning.trim() !== '' || draft.critical.trim() !== ''
  return levelsChanged(draft, levelsDraft(saved), metricInfo(type).duration === true)
}

// Kaydetme gövdesi. Süre alan türlerde süre her zaman gönderilir: boşsa null (süreyi kaldır, hemen).
export function rowPayload(type: MetricType, draft: RowDraft): { warning_level: number; critical_level: number; duration_seconds?: number | null } {
  const levels = { warning_level: Number(draft.warning), critical_level: Number(draft.critical) }
  return metricInfo(type).duration ? { ...levels, duration_seconds: durationSeconds(draft.duration) ?? null } : levels
}

// Bir özet için tek satır: hangi değerin geçerli olduğunu ve — "varsayılan" için — şu an ne olduğunu söyler.
export function describeDraft(type: MetricType, draft: Draft, defaults: Defaults): string {
  if (draft.mode === 'custom') return `Özel: ${formatLevels(type, draftLevels(type, draft))}`
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
    if (d) Object.assign(next, levelsDraft(d))
  }
  return { ...drafts, [type]: next }
}

// ---- mount başına disk eşikleri -------------------------------------------------------------
// MountDrafts içindeki bir mount'un kendi değerleri vardır; orada olmayan bir mount sunucunun disk
// eşiğini izler. Yani "kaldır", bir mount'u sunucunun değerine geri koyan şeydir.

export interface MountDraft {
  warning: string
  critical: string
  // Yalnızca süre alan türlerin konularında (disk gecikmesi, sıcaklık, servis yeniden başlatma).
  duration?: DurationDraft
}

export type MountDrafts = Record<string, MountDraft>

const MAX_MOUNTS = 64

export function mountDraftsFromServer(list: MountThreshold[]): MountDrafts {
  const out: MountDrafts = {}
  for (const m of list) out[m.mount] = { warning: String(m.custom.warning_level), critical: String(m.custom.critical_level) }
  return out
}

// Bir konunun (mount, container, disk, sensör, servis) taslağını kendi türünün kurallarıyla denetler.
export function validateSubjectDraft(type: MetricType, draft: MountDraft): string | null {
  return validateDraft(type, { mode: 'custom', ...draft, duration: draft.duration ?? emptyDuration() })
}

const subjectLevels = (type: MetricType, d: MountDraft): ThresholdLevels => draftLevels(type, { ...d, duration: d.duration ?? emptyDuration() })

export function validateMountDraft(draft: MountDraft): string | null {
  return validateSubjectDraft('disk', draft)
}

export function validateContainerDraft(draft: MountDraft): string | null {
  return validateSubjectDraft('docker_restart', draft)
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
export function customMounts(drafts: MountDrafts, type: MetricType = 'disk'): MountOverrides {
  const out: MountOverrides = {}
  for (const [mount, d] of Object.entries(drafts)) out[mount] = subjectLevels(type, d)
  return out
}

// Mevcut bir sunucu için güncelleme: değeri olan ve kaldırılan mount'lar null olur (sunucunun disk
// eşiğine geri döner), gerisi değerlerini taşır. Container ve protokol 4 konuları da aynı biçimdedir.
export function toMountOverrides(saved: MountDrafts, draft: MountDrafts, type: MetricType = 'disk'): MountOverrides {
  const out: MountOverrides = {}
  for (const mount of Object.keys(saved)) if (!(mount in draft)) out[mount] = null
  return { ...out, ...customMounts(draft, type) }
}

export function isMountsDirty(a: MountDrafts, b: MountDrafts): boolean {
  const keys = new Set([...Object.keys(a), ...Object.keys(b)])
  for (const k of keys) {
    const x = a[k]
    const y = b[k]
    if (!x || !y) return true
    if (levelsChanged({ ...x, duration: x.duration ?? emptyDuration() }, { ...y, duration: y.duration ?? emptyDuration() }, true)) return true
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
export function addMount(drafts: MountDrafts, mount: string, from?: ThresholdLevels, withDuration = false): MountDrafts {
  const start: MountDraft = from ? levelsDraft(from) : { warning: '', critical: '', duration: emptyDuration() }
  if (!withDuration) delete start.duration
  return { ...drafts, [mount]: start }
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
      return `Disk ${mount} — Özel: ${formatLevels('disk', subjectLevels('disk', d))}`
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
    return draftLevels(type, draft)
  }
  return defaults[type]
}

export function describeContainers(drafts: MountDrafts): string[] {
  return Object.keys(drafts)
    .sort()
    .map((name) => {
      const d = drafts[name]
      return `Docker restart ${name} — Özel: ${formatLevels('docker_restart', subjectLevels('docker_restart', d))}`
    })
}

// ---- protokol 4 konu eşikleri (disk gecikmesi, sıcaklık, servis yeniden başlatma) --------------------------------
// Mount eşikleriyle aynı mantık; konu bir fiziksel disk, sensör ya da servistir ve satırın kendi süresi vardır (server
// konu eşiğini süresiyle saklar: konunun süresi boşsa o konuda hemen açılır).

export const SUBJECT_METRICS: readonly SubjectMetricType[] = ['disk_latency', 'temperature', 'service_restart']

// Konunun adı ve bayt sınırı (server: disk adı 64, sensör ve servis 256 bayt).
const SUBJECT_NAME: Record<SubjectMetricType, { what: string; maxBytes: number }> = {
  disk_latency: { what: 'Disk adı', maxBytes: 64 },
  temperature: { what: 'Sensör adı', maxBytes: 256 },
  service_restart: { what: 'Servis adı', maxBytes: 256 },
}

export type SubjectDrafts = Record<SubjectMetricType, MountDrafts>

export const emptySubjectDrafts = (): SubjectDrafts => ({ disk_latency: {}, temperature: {}, service_restart: {} })

export function subjectDraftsFromServer(list: SubjectThreshold[] | undefined): SubjectDrafts {
  const out = emptySubjectDrafts()
  for (const t of list ?? []) if (t.metric_type in out) out[t.metric_type][t.subject] = levelsDraft(t.custom)
  return out
}

export function validateSubjectDrafts(drafts: SubjectDrafts): string[] {
  const errors: string[] = []
  for (const type of SUBJECT_METRICS) {
    const names = Object.keys(drafts[type])
    const label = metricInfo(type).label
    if (names.length > MAX_MOUNTS) errors.push(`${label}: en fazla ${MAX_MOUNTS} konu için özel eşik girilebilir.`)
    for (const name of names.sort()) {
      const err = validateSubjectDraft(type, drafts[type][name])
      if (err) errors.push(`${label} ${name}: ${err}`)
    }
  }
  return errors
}

// Mevcut bir sunucu için güncelleme: kaldırılan konular null, gerisi değerleriyle; değişmeyen türler gönderilmez.
export function toSubjectOverrides(saved: SubjectDrafts, draft: SubjectDrafts): SubjectThresholdOverrides {
  const out: SubjectThresholdOverrides = {}
  for (const type of SUBJECT_METRICS) {
    if (isMountsDirty(saved[type], draft[type])) out[type] = toMountOverrides(saved[type], draft[type], type)
  }
  return out
}

export const isSubjectsDirty = (a: SubjectDrafts, b: SubjectDrafts): boolean => SUBJECT_METRICS.some((t) => isMountsDirty(a[t], b[t]))

// "Ekle"ye yazılan konu adını denetler: kullanılabilir bir ad ya da bir mesaj döner.
export function parseSubjectToAdd(type: SubjectMetricType, text: string, existing: MountDrafts): { mount: string } | { error: string } {
  const name = text.trim()
  const { what, maxBytes } = SUBJECT_NAME[type]
  if (name === '') return { error: `${what} girin.` }
  // eslint-disable-next-line no-control-regex
  if (/[\u0000-\u001f\u007f-\u009f]/.test(name)) return { error: `${what} kontrol karakteri içeremez.` }
  if (new TextEncoder().encode(name).length > maxBytes) return { error: `${what} ${maxBytes} bayttan uzun olamaz.` }
  if (name in existing) return { error: `${name} için zaten bir değer var.` }
  if (Object.keys(existing).length >= MAX_MOUNTS) return { error: `En fazla ${MAX_MOUNTS} konu eklenebilir.` }
  return { mount: name }
}

export function describeSubjects(type: SubjectMetricType, drafts: MountDrafts): string[] {
  const label = metricInfo(type).label
  return Object.keys(drafts)
    .sort()
    .map((name) => `${label} ${name} — Özel: ${formatLevels(type, subjectLevels(type, drafts[name]))}`)
}

// Disk gecikmesi için önerilecek diskler: son metrik satırındaki G/Ç'si ölçülen diskler.
export function diskIONames(point: Pick<MetricPoint, 'disk_io'> | null | undefined): string[] {
  return [...new Set((point?.disk_io ?? []).map((d) => d.name))].sort()
}

// Sensörün donanım sınırı notu. İkisi de biliniyorsa (ve sıralıysa) öneri olarak uyarı = üst sınır, kritik = kritik
// sınır sunulur; hiçbir şey kendiliğinden kurulmaz.
export function temperatureNotes(temps: Temperature[] | undefined): Record<string, { text: string; levels?: ThresholdLevels }> {
  const out: Record<string, { text: string; levels?: ThresholdLevels }> = {}
  for (const t of temps ?? []) {
    const c = (v: number) => `${Number.isInteger(v) ? v : decimal(v, 1)} °C`
    const parts = [t.max !== undefined ? `üst sınır ${c(t.max)}` : '', t.crit !== undefined ? `kritik ${c(t.crit)}` : ''].filter(Boolean)
    if (parts.length === 0) continue
    const levels = t.max !== undefined && t.crit !== undefined && t.max <= t.crit ? { warning_level: t.max, critical_level: t.crit } : undefined
    out[t.sensor] = { text: `Donanım: ${parts.join(' · ')}`, ...(levels ? { levels } : {}) }
  }
  return out
}
