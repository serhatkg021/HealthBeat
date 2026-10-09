// Grafiklerdeki eşik çizgileri (uyarı ve kritik): sunucunun eşik yanıtından değerler ve hangisinin ölçeğe sığdığı. React
// yok; Node'un çalıştırıcısıyla birim test edilir.
import type { HostThresholdsResponse, MetricType } from '../types/api.ts'

export interface ChartReference {
  value: number
  label: string
  tone: 'warning' | 'critical'
}

// Bir türün sunucuda geçerli seviyeleri (kendi değeri devralınanı ezer) çizgi olarak; tanımlı değilse çizgi yok.
// Konuya (mount, disk, sensör) özel değerler burada yoktur: o grafiklerde her konu ayrı bir çizgi olduğundan tek çizgi
// hepsini temsil etmez.
export function thresholdReferences(res: HostThresholdsResponse | null | undefined, metric: MetricType, format: (v: number) => string): ChartReference[] {
  const view = res?.thresholds.find((t) => t.metric_type === metric)
  const levels = view?.custom ?? view?.default
  if (!levels) return []
  return [
    { value: levels.warning_level, label: `uyarı ${format(levels.warning_level)}`, tone: 'warning' },
    { value: levels.critical_level, label: `kritik ${format(levels.critical_level)}`, tone: 'critical' },
  ]
}

// Ölçeğe sığanlar çizilir; sığmayanlar ölçeği büyütmez (veri düzleşirdi), grafiğin köşesinde yazıyla belirtilir. Değer
// eşiğe yaklaşınca ölçek büyür ve çizgi kendiliğinden görünür.
export function splitReferences(refs: readonly ChartReference[], top: number): { inside: ChartReference[]; above: ChartReference[] } {
  return { inside: refs.filter((r) => r.value <= top), above: refs.filter((r) => r.value > top) }
}
