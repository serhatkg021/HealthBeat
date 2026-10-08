// Sunucu sayfasındaki salt okunur "Geçerli alert kuralları"nın saf mantığı: sunucunun eşik yanıtını, her kuralın
// geçerli değeri ve nereden geldiğiyle satırlara çevirir. React içermez; Node'un çalıştırıcısıyla birim test edilir.
import type { HostThresholdsResponse } from '../types/api.ts'
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

// Hangi mount'ların disk alert'i üretebileceğinin tek satırlık özeti.
export function diskAlertSummary(selection: { all_mounts_alert: boolean; custom_alert_mounts: string[] }): string {
  if (selection.all_mounts_alert) return 'Raporlanan tüm mount’lar'
  if (selection.custom_alert_mounts.length === 0) return 'Hiçbiri (disk alert’i üretilmez)'
  return [...selection.custom_alert_mounts].sort().join(', ')
}
