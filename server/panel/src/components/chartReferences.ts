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

// Eşik değeri girildiği gibi, ondalık virgülle ("0,001 ms"): yuvarlansaydı küçük eşikler "0,00 ms" olurdu.
export const rawValue = (v: number, unit: string): string => `${String(v).replace('.', ',')} ${unit}`

// Ölçeğe sığanlar çizilir; sığmayanlar ölçeği büyütmez (veri düzleşirdi), grafiğin köşesinde yazıyla belirtilir. Değer
// eşiğe yaklaşınca ölçek büyür ve çizgi kendiliğinden görünür.
export function splitReferences(refs: readonly ChartReference[], top: number): { inside: ChartReference[]; above: ChartReference[] } {
  return { inside: refs.filter((r) => r.value <= top), above: refs.filter((r) => r.value > top) }
}

// Çizilecek eşiklerin etiketleri: iki çizgi ölçeğin %8'inden yakınsa etiketleri üst üste binerdi; o zaman tek etikette
// birleşir (üstteki çizgide), alttakinin etiketi boş kalır.
export function referenceLabels(inside: readonly ChartReference[], top: number): string[] {
  if (inside.length !== 2 || top <= 0) return inside.map((r) => r.label)
  const [a, b] = inside
  if (Math.abs(a.value - b.value) >= top * 0.08) return [a.label, b.label]
  const upper = a.value >= b.value ? 0 : 1
  const joined = `${inside[1 - upper].label} · ${inside[upper].label}`
  return upper === 0 ? [joined, ''] : ['', joined]
}
