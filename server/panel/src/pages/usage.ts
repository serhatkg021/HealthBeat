// Kullanım çubuklarının (CPU, RAM, disk, container restart) rengi ve boyutu — saf mantık. Renk,
// server'ın alert motoruyla aynı kuralı izler (alertengine): değer >= kritik ise kritik, >= uyarı ise
// uyarı, aksi halde normal. Böylece çubuk sarı/kırmızıya döndüğünde bir alert de açılmış demektir.
import type { HostThresholdView, ContainerThreshold, MetricType, MountThreshold, ThresholdLevels } from '../types/api.ts'

export type UsageTone = 'good' | 'warning' | 'critical'

export function usageTone(value: number, levels: ThresholdLevels | null | undefined): UsageTone {
  if (!levels || Number.isNaN(value)) return 'good'
  if (value >= levels.critical_level) return 'critical'
  if (value >= levels.warning_level) return 'warning'
  return 'good'
}

// Bir metriğin sunucu için geçerli eşiği: özel değer varsa o, yoksa varsayılan; ikisi de yoksa null
// (metrik için eşik tanımlı değil: alert üretmez, çubukta eşik işareti gösterilmez).
export function effectiveLevels(views: HostThresholdView[] | undefined, metric: MetricType): ThresholdLevels | null {
  const v = views?.find((x) => x.metric_type === metric)
  return v?.custom ?? v?.default ?? null
}

// Bir mount'un eşiği: kendi özel değeri varsa o, yoksa sunucunun disk eşiği.
export function mountLevels(
  views: HostThresholdView[] | undefined,
  mounts: MountThreshold[] | undefined,
  mount: string,
): ThresholdLevels | null {
  return mounts?.find((m) => m.mount === mount)?.custom ?? effectiveLevels(views, 'disk')
}

// Bir container'ın restart eşiği: kendi özel değeri varsa o, yoksa sunucunun docker_restart eşiği.
export function containerLevels(
  views: HostThresholdView[] | undefined,
  containers: ContainerThreshold[] | undefined,
  container: string,
): ThresholdLevels | null {
  return containers?.find((c) => c.container === container)?.custom ?? effectiveLevels(views, 'docker_restart')
}

// Çubuğun dolu kısmı için 0–100 aralığına sıkıştırılmış yüzde (bozuk/aşan değer çubuğu taşırmasın).
export function clampPct(pct: number): number {
  if (!Number.isFinite(pct)) return 0
  return Math.min(100, Math.max(0, pct))
}

// Yüzde metni: "%42.5". Tek ondalık, bilinmiyorsa çizgi.
export function pctText(pct: number | undefined | null): string {
  return pct === undefined || pct === null || !Number.isFinite(pct) ? '—' : `%${pct.toFixed(1)}`
}

export const TONE_LABEL: Record<UsageTone, string> = { good: 'normal', warning: 'uyarı seviyesinde', critical: 'kritik seviyede' }

// Çubuğun ekran okuyucu metni: renk tek başına bilgi taşımaz.
export function usageAriaLabel(label: string, pct: number, tone: UsageTone): string {
  return `${label}: ${pctText(pct)}, ${TONE_LABEL[tone]}`
}
