import { CartesianGrid, Line, LineChart, ReferenceLine, ResponsiveContainer, Tooltip, XAxis, YAxis } from 'recharts'
import type { LucideIcon } from 'lucide-react'
import { EmptyState } from './EmptyState'
import { referenceLabels, splitReferences, type ChartReference } from './chartReferences'
import { formatPoint, isLongSpan } from '../pages/metricHistory'
import { niceAxis } from '../pages/units'

export interface Series {
  key: string
  label: string
  color: string
}

// Performans sekmesinin çizgi grafiği. X ekseni seçilen aralığın TAMAMINI kapsar (verinin nerede başlayıp bittiği görülür);
// tek Y ekseni vardır (iki ölçek gerekiyorsa iki grafik çizilir). Aynı syncId'yi taşıyan grafikler imleci zamana göre
// paylaşır. Satırda olmayan değer çizgide boşluk olur. Değerler ham gelir; biçim formatter'larla verilir. Y ekseninin üst
// sınırı (verilen ya da verinin en büyüğü) yuvarlak bir değere çekilir ve etiketler eşit aralıklıdır (niceAxis). Seri
// açıklaması grafiğin altında HTML'dir: grafiğin içinde olsaydı üzerine gelmek araç ipucunu açık bırakıyordu. Eşik
// çizgileri (references) kesikli çizilir; ölçeğin üstünde kalan eşik ölçeği büyütmez, grafiğin altında yazıyla belirtilir.
export function HistoryChart({
  loading,
  rows,
  series,
  range,
  emptyIcon,
  emptyText = 'Bu aralıkta metrik verisi yok.',
  height = 280,
  yDomain = [0, 100],
  yTick = (v) => `${v}%`,
  yWidth,
  format = (v) => `${v}%`,
  syncId,
  references = [],
}: {
  loading: boolean
  rows: Record<string, number>[]
  series: Series[]
  range: { start: number; end: number }
  emptyIcon: LucideIcon
  emptyText?: string
  height?: number
  // Üst sınır: sayı (ör. yüzdeler için 100, birden çok grafiğin ortak ölçeği) ya da 'auto' (verinin en büyüğü).
  yDomain?: [number, number | 'auto']
  yTick?: (v: number) => string
  yWidth?: number
  // Araç ipucundaki değer.
  format?: (v: number) => string
  syncId?: string
  references?: ChartReference[]
}) {
  if (loading) return <div className="muted">Yükleniyor…</div>
  if (rows.length === 0 || series.length === 0 || !rows.some((r) => series.some((s) => s.key in r))) {
    return <EmptyState icon={emptyIcon}>{emptyText}</EmptyState>
  }
  const span = range.end - range.start
  const long = isLongSpan(span)
  const labelOf = (key: string) => series.find((s) => s.key === key)?.label ?? key
  let dataMax = 0
  if (yDomain[1] === 'auto') for (const r of rows) for (const s of series) if (r[s.key] > dataMax) dataMax = r[s.key]
  const axis = niceAxis(yDomain[1] === 'auto' ? dataMax : yDomain[1])
  const refs = splitReferences(references, axis.top)
  const refLabels = referenceLabels(refs.inside, axis.top)
  return (
    <>
      <ResponsiveContainer width="100%" height={height}>
        <LineChart data={rows} margin={{ top: 8, right: 8, bottom: 8, left: yWidth ? 0 : -12 }} syncId={syncId} syncMethod="value">
          <CartesianGrid strokeDasharray="3 3" stroke="var(--border)" vertical={false} />
          <XAxis
            dataKey="ts"
            type="number"
            scale="time"
            domain={[range.start, range.end]}
            tickFormatter={(ms: number) => formatPoint(ms, span)}
            tick={{ fontSize: 11 }}
            stroke="var(--text-muted)"
            minTickGap={long ? 70 : 48}
            angle={long ? -20 : 0}
            textAnchor={long ? 'end' : 'middle'}
            height={long ? 40 : 24}
          />
          <YAxis domain={[yDomain[0], axis.top]} ticks={axis.ticks} tick={{ fontSize: 11 }} stroke="var(--text-muted)" tickFormatter={yTick} width={yWidth} />
          <Tooltip
            contentStyle={{
              fontSize: 12,
              borderRadius: 8,
              background: 'var(--surface-1)',
              border: '1px solid var(--border)',
              color: 'var(--text-primary)',
            }}
            labelFormatter={(label) => formatPoint(Number(label), span)}
            formatter={(value, name) => [format(Number(value)), labelOf(String(name))]}
          />
          {refs.inside.map((r, i) => (
            <ReferenceLine
              key={r.label}
              y={r.value}
              stroke={r.tone === 'critical' ? 'var(--status-critical)' : 'var(--status-warning)'}
              strokeDasharray="5 4"
              ifOverflow="discard"
              // Dibe yakın çizginin etiketi solda: sağ alttaki saat etiketiyle çakışmasın.
              label={{ value: refLabels[i], position: r.value < axis.top * 0.1 ? 'insideTopLeft' : 'insideTopRight', fontSize: 11, fill: r.tone === 'critical' ? 'var(--status-critical-text)' : 'var(--status-warning-text)' }}
            />
          ))}
          {series.map((s) => (
            <Line key={s.key} type="monotone" dataKey={s.key} name={s.key} stroke={s.color} strokeWidth={2} dot={false} activeDot={{ r: 4 }} />
          ))}
        </LineChart>
      </ResponsiveContainer>
      {refs.above.length > 0 && (
        <p className="chart-above">Ölçeğin üstünde: {refs.above.map((r) => r.label).join(' · ')}</p>
      )}
      {series.length > 1 && (
        <div className="chart-legend">
          {series.map((s) => (
            <span key={s.key}>
              <i style={{ background: s.color }} />
              {s.label}
            </span>
          ))}
        </div>
      )}
    </>
  )
}
