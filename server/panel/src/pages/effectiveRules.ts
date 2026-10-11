// Sunucu sayfasındaki salt okunur "Geçerli alert kuralları"nın saf mantığı: sunucunun eşik yanıtını, her kuralın
// geçerli değeri ve nereden geldiğiyle satırlara çevirir. React içermez; Node'un çalıştırıcısıyla birim test edilir.
import type { HostStatusRuleView, HostThresholdsResponse } from '../types/api.ts'
import { TOPICS, itemKey } from './ruleTopics.ts'
import { ruleInfo, settingText } from './statusRules.ts'
import { METRICS, formatLevels, metricInfo } from './thresholds.ts'

export type RuleSource = 'custom' | 'inherited' | 'none'

export interface EffectiveRule {
  key: string
  label: string
  // Geçerli değer; tanımlı değilse null (alert üretilmez).
  value: string | null
  source: RuleSource
}

// Metrik başına bir satır (özel değer devralınanı ezer), ardından mount'ların, container'ların ve protokol 4 konularının
// (disk, sensör, servis) kendi eşikleri.
export function effectiveRules(res: HostThresholdsResponse): EffectiveRule[] {
  const rows: EffectiveRule[] = METRICS.map((m) => {
    const view = res.thresholds.find((t) => t.metric_type === m.type)
    if (view?.custom) return { key: m.type, label: m.label, value: formatLevels(m.type, view.custom), source: 'custom' }
    if (view?.default) return { key: m.type, label: m.label, value: formatLevels(m.type, view.default), source: 'inherited' }
    return { key: m.type, label: m.label, value: null, source: 'none' }
  })
  for (const t of [...res.mount_thresholds].sort((a, b) => a.mount.localeCompare(b.mount))) {
    rows.push({ key: `mount:${t.mount}`, label: `Disk · ${t.mount}`, value: formatLevels('disk', t.custom), source: 'custom' })
  }
  for (const t of [...res.container_thresholds].sort((a, b) => a.container.localeCompare(b.container))) {
    rows.push({ key: `container:${t.container}`, label: `Docker restart · ${t.container}`, value: formatLevels('docker_restart', t.custom), source: 'custom' })
  }
  const subjects = [...(res.subject_thresholds ?? [])].sort((a, b) => a.metric_type.localeCompare(b.metric_type) || a.subject.localeCompare(b.subject))
  for (const t of subjects) {
    rows.push({
      key: `${t.metric_type}:${t.subject}`,
      label: `${metricInfo(t.metric_type).label} · ${t.subject}`,
      value: formatLevels(t.metric_type, t.custom),
      source: 'custom',
    })
  }
  return rows
}

// Durum kuralı başına bir satır: sunucunun kendi ayarı devralınanı ezer; hiçbiri yoksa kural kapalıdır.
export function statusEffectiveRules(views: HostStatusRuleView[]): EffectiveRule[] {
  return views.map((v) => {
    const label = ruleInfo(v.rule)?.label ?? v.rule
    const key = `status:${v.rule}`
    if (v.custom) return { key, label, value: settingText(v.rule, v.custom), source: 'custom' }
    if (v.default) return { key, label, value: settingText(v.rule, v.default), source: 'inherited' }
    return { key, label, value: null, source: 'none' }
  })
}

// DISK_SELECTION_KEY, disk alert seçiminin satırıdır (Disk konusunda gösterilir).
export const DISK_SELECTION_KEY = 'selection:disk_mounts'

export interface RuleGroup {
  id: string
  title: string
  rows: EffectiveRule[]
}

// topicItemKey, bir satırın anahtarını Alert kuralları sayfasındaki öğenin anahtarına çevirir (bkz. ruleTopics itemKey):
// durum kuralı ve seçim aynıdır; mount eşiği disk, container eşiği docker_restart, "metrik:konu" eşiği metriğin kendisidir.
function topicItemKey(key: string): string {
  const head = key.split(':')[0]
  if (head === 'status' || head === 'selection') return key
  if (head === 'mount') return 'threshold:disk'
  if (head === 'container') return 'threshold:docker_restart'
  return `threshold:${head}`
}

// groupByTopic, satırları Alert kuralları sayfasının konularına ayırır: konular menü sırasıyla, konu içinde satırlar
// konunun öğe sırasıyla (aynı öğenin satırları, ör. mount eşikleri, geldikleri sırayla). Satırı olmayan konu listelenmez;
// hiçbir konuya düşmeyen (yeni bir tür) satırlar sonda "Diğer"dedir.
export function groupByTopic(rows: EffectiveRule[]): RuleGroup[] {
  const placed = TOPICS.map((t) => ({ id: t.id, title: t.title, rows: [] as { row: EffectiveRule; order: number }[] }))
  const other: RuleGroup = { id: 'diger', title: 'Diğer', rows: [] }
  for (const row of rows) {
    const k = topicItemKey(row.key)
    const i = TOPICS.findIndex((t) => t.items.some((x) => itemKey(x) === k))
    if (i < 0) other.rows.push(row)
    else placed[i].rows.push({ row, order: TOPICS[i].items.findIndex((x) => itemKey(x) === k) })
  }
  const groups = placed.map((g) => ({ id: g.id, title: g.title, rows: g.rows.sort((a, b) => a.order - b.order).map((p) => p.row) }))
  return [...groups, other].filter((g) => g.rows.length > 0)
}

// Hangi mount'ların disk alert'i üretebileceğinin tek satırlık özeti.
export function diskAlertSummary(selection: { all_mounts_alert: boolean; custom_alert_mounts: string[] }): string {
  if (selection.all_mounts_alert) return 'Raporlanan tüm mount’lar'
  if (selection.custom_alert_mounts.length === 0) return 'Hiçbiri (disk alert’i üretilmez)'
  return [...selection.custom_alert_mounts].sort().join(', ')
}
