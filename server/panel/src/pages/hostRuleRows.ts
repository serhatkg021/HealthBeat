// Alert kuralları sayfasının sunucu kapsamı: sunucunun eşik, durum kuralı ve seçim taslakları konu satırlarına çevrilir.
// Taslaklardan çalışır; böylece aynı satırlar hem kaydedilmiş durumu hem de düzenlenen taslağı gösterir. React yok;
// Node'un çalıştırıcısıyla birim test edilir.
import type {
  HostServices,
  HostStatusRuleView,
  HostThresholdsResponse,
  MetricType,
  MountOverrides,
  StatusRule,
  StatusRuleChanges,
  StatusRuleLevel,
  StatusRuleSetting,
  SubjectMetricType,
  SubjectThresholdOverrides,
  ThresholdOverrides,
} from '../types/api.ts'
import { fromServer as diskFromServer, isDirty as isDiskDirty, toServer as diskToServer, type State as DiskState } from './diskSelection.ts'
import { durationDraft, durationSeconds, formatDuration } from './duration.ts'
import { AUTO_RULES, SELECTIONS, TOPICS, itemKey, topicItems, type TopicItem } from './ruleTopics.ts'
import { hostDrafts, ruleChanged, ruleChanges, ruleInfo, validateRuleDrafts, type RuleDrafts } from './statusRules.ts'
import {
  isMetricDirty,
  isMountsDirty,
  toMountOverrides,
  toOverrides,
  toSubjectOverrides,
  validateContainerDrafts,
  validateDrafts,
  validateMountDrafts,
  validateSubjectDrafts,
  withMode,
  containerDraftsFromServer,
  defaultsFromServer,
  draftsFromServer,
  metricInfo,
  mountDraftsFromServer,
  subjectDraftsFromServer,
  type Defaults,
  type Drafts,
  type MountDraft,
  type MountDrafts,
  type SubjectDrafts,
} from './thresholds.ts'

export interface HostRuleState {
  thresholds: Drafts
  defaults: Defaults
  mounts: MountDrafts
  containers: MountDrafts
  subjects: SubjectDrafts
  status: RuleDrafts
  // Durum kuralının sunucuya devralınan değeri (kendi değeri yokken geçerli olan).
  statusDefaults: Partial<Record<StatusRule, StatusRuleSetting | null>>
  // Yüklenemediyse null (satır "bilinmiyor" gösterir).
  disks: DiskState | null
  watched: { name: string; reported: boolean }[] | null
}

export function hostRuleState(
  thresholds: HostThresholdsResponse,
  status: HostStatusRuleView[],
  disks: { all_mounts_alert: boolean; custom_alert_mounts: string[] } | null,
  services: HostServices | null,
): HostRuleState {
  const reported = new Set(services?.services.map((s) => s.name) ?? [])
  return {
    thresholds: draftsFromServer(thresholds.thresholds),
    defaults: defaultsFromServer(thresholds.thresholds),
    mounts: mountDraftsFromServer(thresholds.mount_thresholds),
    containers: containerDraftsFromServer(thresholds.container_thresholds),
    subjects: subjectDraftsFromServer(thresholds.subject_thresholds),
    status: hostDrafts(status),
    statusDefaults: Object.fromEntries(status.map((v) => [v.rule, v.default])),
    disks: disks ? diskFromServer(disks.all_mounts_alert, disks.custom_alert_mounts) : null,
    watched: services ? [...services.watched].sort().map((name) => ({ name, reported: reported.has(name) })) : null,
  }
}

export type PillTone = 'warning' | 'critical' | 'info' | 'off' | 'plain'

export interface Pill {
  text: string
  tone: PillTone
}

// own = bu sunucunun kendi değeri; inherited = sistemden ya da organizasyondan; always = ayarı olmayan, hep açık alert.
export type SourceKind = 'own' | 'inherited' | 'always' | 'none'

export type RowKind = 'eşik' | 'durum' | 'seçim' | 'otomatik'

export interface SubjectStrip {
  label: string
  items: { name: string; value: string }[]
}

export interface RuleRowView {
  key: string
  label: string
  hint: string
  kind: RowKind
  pills: Pill[]
  // "anlık", "10 dk boyunca"; kapalı kuralda ve seçimde null.
  duration: string | null
  // Seçimlerde null (kaynak sütunu boş kalır).
  source: SourceKind | null
  // Menüdeki "etkin" sayısına girer (yalnızca eşik ve durum kuralları).
  on: boolean
  subjects?: SubjectStrip
}

const LEVEL_PILL: Record<Exclude<StatusRuleLevel, 'off'>, Pill> = {
  info: { text: 'Bilgi', tone: 'info' },
  warning: { text: 'Uyarı', tone: 'warning' },
  critical: { text: 'Kritik', tone: 'critical' },
}

const OFF: Pill = { text: 'tanımlı değil', tone: 'off' }

// "80" -> "%80", "30" -> "30 ms"; ondalık virgülle.
function levelText(metric: MetricType, value: string | number): string {
  const info = metricInfo(metric)
  const v = String(value).trim().replace('.', ',')
  return info.percent ? `%${v}` : `${v} ${info.unit}`
}

interface Levels {
  warning: string | number
  critical: string | number
  seconds?: number
}

function levelPills(metric: MetricType, l: Levels): Pill[] {
  return [
    { text: `uyarı ${levelText(metric, l.warning)}`, tone: 'warning' },
    { text: `kritik ${levelText(metric, l.critical)}`, tone: 'critical' },
  ]
}

const thresholdDuration = (metric: MetricType, seconds?: number): string =>
  metricInfo(metric).duration && seconds ? `${formatDuration(seconds)} boyunca` : 'anlık'

const SUBJECT_LABEL: Record<SubjectMetricType, string> = {
  disk_latency: 'Diske özel:',
  temperature: 'Sensöre özel:',
  service_restart: 'Servise özel:',
}

function strip(metric: MetricType, label: string, drafts: MountDrafts): SubjectStrip {
  const value = (d: MountDraft) => {
    const unit = metricInfo(metric).unit
    const text = `${d.warning.trim().replace('.', ',')} / ${d.critical.trim().replace('.', ',')} ${unit}`
    const seconds = d.duration ? durationSeconds(d.duration) : undefined
    return seconds ? `${text} · ${formatDuration(seconds)}` : text
  }
  return {
    label,
    items: Object.keys(drafts)
      .sort()
      .map((name) => ({ name, value: value(drafts[name]) })),
  }
}

function thresholdRow(metric: MetricType, state: HostRuleState): RuleRowView {
  const info = metricInfo(metric)
  const draft = state.thresholds[metric]
  const base = { key: `threshold:${metric}`, label: info.label, hint: info.hint ?? '', kind: 'eşik' as const }
  let row: RuleRowView
  if (draft.mode === 'custom') {
    const seconds = durationSeconds(draft.duration)
    row = { ...base, pills: levelPills(metric, { warning: draft.warning, critical: draft.critical }), duration: thresholdDuration(metric, seconds), source: 'own', on: true }
  } else {
    const d = state.defaults[metric]
    row = d
      ? { ...base, pills: levelPills(metric, { warning: d.warning_level, critical: d.critical_level }), duration: thresholdDuration(metric, d.duration_seconds), source: 'inherited', on: true }
      : { ...base, pills: [OFF], duration: null, source: 'none', on: false }
  }
  if (metric === 'disk') row.subjects = strip(metric, 'Mount’a özel:', state.mounts)
  if (metric === 'docker_restart') row.subjects = strip(metric, 'Container’a özel:', state.containers)
  if (metric === 'disk_latency' || metric === 'temperature' || metric === 'service_restart') {
    row.subjects = strip(metric, SUBJECT_LABEL[metric], state.subjects[metric])
  }
  // Sunucu genelinde değer yokken konuya özel değerler yine alert üretir: kural yalnızca o konular için etkindir.
  if (!row.on && row.subjects && row.subjects.items.length > 0) {
    row.on = true
    row.pills = [{ text: 'yalnızca konuya özel değerler', tone: 'plain' }]
    row.source = 'own'
  }
  return row
}

function statusDuration(rule: StatusRule, seconds?: number): string {
  if (!ruleInfo(rule).duration || !seconds) return 'anlık'
  // OOM'da süre, alert'in ne kadar sessizlikten sonra kapanacağıdır.
  return rule === 'oom_kill' ? `${formatDuration(seconds)} sonra kapanır` : `${formatDuration(seconds)} boyunca`
}

function statusRow(rule: StatusRule, state: HostRuleState): RuleRowView {
  const info = ruleInfo(rule)
  const base = { key: `status:${rule}`, label: info.label, hint: info.hint, kind: 'durum' as const }
  const draft = state.status[rule]
  const own = draft.level !== ''
  const setting: StatusRuleSetting | null | undefined = own
    ? { level: draft.level as StatusRuleLevel, duration_seconds: durationSeconds(draft.duration) }
    : state.statusDefaults[rule]
  if (!setting || setting.level === 'off') {
    return { ...base, pills: [own ? { text: 'Kapalı', tone: 'off' } : OFF], duration: null, source: own ? 'own' : 'none', on: false }
  }
  return { ...base, pills: [LEVEL_PILL[setting.level]], duration: statusDuration(rule, setting.duration_seconds), source: own ? 'own' : 'inherited', on: true }
}

function diskSelectionPills(disks: DiskState | null): Pill[] {
  if (!disks) return [{ text: 'bilinmiyor', tone: 'off' }]
  if (disks.mode === 'all') return [{ text: 'Raporlanan tüm mount’lar', tone: 'plain' }]
  if (disks.selected.size === 0) return [{ text: 'Hiçbiri: doluluk alert’i üretilmez', tone: 'off' }]
  return [...disks.selected].sort().map((m) => ({ text: m, tone: 'plain' }))
}

function watchedPills(watched: HostRuleState['watched']): Pill[] {
  if (!watched) return [{ text: 'bilinmiyor', tone: 'off' }]
  if (watched.length === 0) return [{ text: 'Hiçbiri: servis alert’i üretilmez', tone: 'off' }]
  return watched.map((w) => (w.reported ? { text: w.name, tone: 'plain' } : { text: `${w.name} · raporlanmıyor`, tone: 'off' }))
}

const AUTO_DURATION = { disk_missing: '3 rapor', host_offline: 'aralığın 3 katı' } as const

export function hostRow(item: TopicItem, state: HostRuleState): RuleRowView {
  switch (item.kind) {
    case 'threshold':
      return thresholdRow(item.metric, state)
    case 'status':
      return statusRow(item.rule, state)
    case 'selection': {
      const text = SELECTIONS[item.key]
      const pills = item.key === 'disk_mounts' ? diskSelectionPills(state.disks) : watchedPills(state.watched)
      return { key: itemKey(item), label: text.label, hint: text.hint, kind: 'seçim', pills, duration: null, source: null, on: false }
    }
    case 'auto': {
      const text = AUTO_RULES[item.key]
      return {
        key: itemKey(item),
        label: text.label,
        hint: text.hint,
        kind: 'otomatik',
        pills: [{ text: 'Kritik', tone: 'critical' }],
        duration: AUTO_DURATION[item.key],
        source: 'always',
        on: false,
      }
    }
  }
}

// Devralınan satırın kaynak metni: "Devralındı · Ana Şirket"; kaynak bilinmiyorsa genel metin.
export function inheritedText(row: RuleRowView, from: string | undefined): string | undefined {
  return row.source === 'inherited' && from ? `Devralındı · ${from}` : undefined
}

// Özet satırı: bütün konularda etkin kural sayısı ve kaçının bu sunucuya özel olduğu.
export function hostSummary(rows: RuleRowView[]): { on: number; own: number } {
  const rules = rows.filter((r) => r.kind === 'eşik' || r.kind === 'durum')
  return { on: rules.filter((r) => r.on).length, own: rules.filter((r) => r.source === 'own' || (r.subjects?.items.length ?? 0) > 0).length }
}

// ---- düzenleme ------------------------------------------------------------------------------------------------------

const isSubjectMetric = (m: MetricType): m is SubjectMetricType => m === 'disk_latency' || m === 'temperature' || m === 'service_restart'

// Eşiğin konuya özel değerleri (mount, container ya da protokol 4 konuları) değişti mi.
function subjectsChanged(m: MetricType, saved: HostRuleState, draft: HostRuleState): boolean {
  if (m === 'disk') return isMountsDirty(draft.mounts, saved.mounts)
  if (m === 'docker_restart') return isMountsDirty(draft.containers, saved.containers)
  if (isSubjectMetric(m)) return isMountsDirty(draft.subjects[m], saved.subjects[m])
  return false
}

export function itemChanged(item: TopicItem, saved: HostRuleState, draft: HostRuleState): boolean {
  switch (item.kind) {
    case 'threshold':
      return isMetricDirty(item.metric, draft.thresholds[item.metric], saved.thresholds[item.metric]) || subjectsChanged(item.metric, saved, draft)
    case 'status':
      return ruleChanged(item.rule, saved.status[item.rule], draft.status[item.rule])
    case 'selection':
      return item.key === 'disk_mounts' && saved.disks !== null && draft.disks !== null && isDiskDirty(draft.disks, saved.disks)
    default:
      return false
  }
}

// Kaydedilmemiş değişikliği olan öğelerin anahtarları (menü noktaları ve kaydet çubuğundaki sayı).
export function changedKeys(saved: HostRuleState, draft: HostRuleState): Set<string> {
  const out = new Set<string>()
  for (const topic of TOPICS) for (const item of topicItems(topic, 'sunucu')) if (itemChanged(item, saved, draft)) out.add(itemKey(item))
  return out
}

// "Özelleştir": eşik devralınan değerlerden, durum kuralı devralınan seviyeden (yoksa uyarı) başlar.
export function customizeItem(item: TopicItem, state: HostRuleState): HostRuleState {
  if (item.kind === 'threshold') return { ...state, thresholds: withMode(state.thresholds, item.metric, 'custom', state.defaults) }
  if (item.kind === 'status') {
    const inherited = state.statusDefaults[item.rule]
    const level: StatusRuleLevel = inherited && inherited.level !== 'off' ? inherited.level : 'warning'
    return { ...state, status: { ...state.status, [item.rule]: { level, duration: durationDraft(inherited?.duration_seconds) } } }
  }
  return state
}

// "Devral": bu sunucunun kendi değeri bırakılır; konuya özel değerlere dokunulmaz.
export function inheritItem(item: TopicItem, state: HostRuleState): HostRuleState {
  if (item.kind === 'threshold') return { ...state, thresholds: withMode(state.thresholds, item.metric, 'default', state.defaults) }
  if (item.kind === 'status') return { ...state, status: { ...state.status, [item.rule]: { ...state.status[item.rule], level: '' } } }
  return state
}

// "Geri al": öğe (konuya özel değerleriyle) kaydedilmiş hâline döner.
export function revertItem(item: TopicItem, saved: HostRuleState, draft: HostRuleState): HostRuleState {
  switch (item.kind) {
    case 'threshold': {
      const m = item.metric
      const next: HostRuleState = { ...draft, thresholds: { ...draft.thresholds, [m]: saved.thresholds[m] } }
      if (m === 'disk') next.mounts = saved.mounts
      if (m === 'docker_restart') next.containers = saved.containers
      if (isSubjectMetric(m)) next.subjects = { ...draft.subjects, [m]: saved.subjects[m] }
      return next
    }
    case 'status':
      return { ...draft, status: { ...draft.status, [item.rule]: saved.status[item.rule] } }
    case 'selection':
      return item.key === 'disk_mounts' ? { ...draft, disks: saved.disks } : draft
    default:
      return draft
  }
}

// Kaydetmeyi engelleyen sorunlar, türün adıyla öneklenmiş.
export function hostProblems(draft: HostRuleState): string[] {
  return [
    ...validateDrafts(draft.thresholds),
    ...validateMountDrafts(draft.mounts),
    ...validateContainerDrafts(draft.containers),
    ...validateSubjectDrafts(draft.subjects),
    ...validateRuleDrafts(draft.status),
  ]
}

export type SavePart = 'thresholds' | 'status' | 'disks'

export interface HostSavePlan {
  thresholds?: { overrides: ThresholdOverrides; parts: { mounts?: MountOverrides; containers?: MountOverrides; subjects?: SubjectThresholdOverrides } }
  status?: StatusRuleChanges
  disks?: { all_mounts_alert: boolean; custom_alert_mounts: string[] }
}

// Yalnızca değişen kısımlar gönderilir (en fazla üç çağrı). Eşiklerde bütün türler gönderilir (null = devral); konuya özel
// değerlerde yalnızca değişen tür, o türde de kaldırılanlar null olarak.
export function hostSavePlan(saved: HostRuleState, draft: HostRuleState): HostSavePlan {
  const plan: HostSavePlan = {}
  const mountsDirty = isMountsDirty(draft.mounts, saved.mounts)
  const containersDirty = isMountsDirty(draft.containers, saved.containers)
  const subjects = toSubjectOverrides(saved.subjects, draft.subjects)
  const metricsDirty = TOPICS.flatMap((t) => t.items).some((i) => i.kind === 'threshold' && isMetricDirty(i.metric, draft.thresholds[i.metric], saved.thresholds[i.metric]))
  if (metricsDirty || mountsDirty || containersDirty || Object.keys(subjects).length > 0) {
    plan.thresholds = {
      overrides: toOverrides(draft.thresholds),
      parts: {
        ...(mountsDirty ? { mounts: toMountOverrides(saved.mounts, draft.mounts) } : {}),
        ...(containersDirty ? { containers: toMountOverrides(saved.containers, draft.containers, 'docker_restart') } : {}),
        ...(Object.keys(subjects).length > 0 ? { subjects } : {}),
      },
    }
  }
  const status = ruleChanges(saved.status, draft.status)
  if (Object.keys(status).length > 0) plan.status = status
  if (saved.disks && draft.disks && isDiskDirty(draft.disks, saved.disks)) plan.disks = diskToServer(draft.disks)
  return plan
}

// Kaydetmeden sonra server'dan okunan durum; kaydedilemeyen kısımlar taslaktaki hâlleriyle kalır (kaybolmasın).
export function keepFailed(fresh: HostRuleState, draft: HostRuleState, failed: ReadonlySet<SavePart>): HostRuleState {
  const out = { ...fresh }
  if (failed.has('thresholds')) Object.assign(out, { thresholds: draft.thresholds, mounts: draft.mounts, containers: draft.containers, subjects: draft.subjects })
  if (failed.has('status')) out.status = draft.status
  if (failed.has('disks')) out.disks = draft.disks
  return out
}
