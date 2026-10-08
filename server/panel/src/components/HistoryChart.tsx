import { CartesianGrid, Legend, Line, LineChart, ResponsiveContainer, Tooltip, XAxis, YAxis } from 'recharts'
import type { LucideIcon } from 'lucide-react'
import { EmptyState } from './EmptyState'
import { formatPoint, isLongSpan } from '../pages/metricHistory'

export interface Series {
  key: string
  label: string
  color: string
}

// Performans sekmesinin çizgi grafiği. X ekseni seçilen aralığın TAMAMINI kapsar (verinin nerede başlayıp bittiği görülür);
// tek Y ekseni vardır (iki ölçek gerekiyorsa iki grafik çizilir). Aynı syncId'yi taşıyan grafikler imleci zamana göre
// paylaşır. Satırda olmayan değer çizgide boşluk olur. Değerler ham gelir; biçim formatter'larla verilir.
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
}: {
  loading: boolean
  rows: Record<string, number>[]
  series: Series[]
  range: { start: number; end: number }
  emptyIcon: LucideIcon
  emptyText?: string
  height?: number
  yDomain?: [number, number | 'auto']
  yTick?: (v: number) => string
  yWidth?: number
  // Araç ipucundaki değer.
  format?: (v: number) => string
  syncId?: string
}) {
  if (loading) return <div className="muted">Yükleniyor…</div>
  if (rows.length === 0 || series.length === 0 || !rows.some((r) => series.some((s) => s.key in r))) {
    return <EmptyState icon={emptyIcon}>{emptyText}</EmptyState>
  }
  const span = range.end - range.start
  const long = isLongSpan(span)
  const labelOf = (key: string) => series.find((s) => s.key === key)?.label ?? key
  return (
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
        <YAxis domain={yDomain} tick={{ fontSize: 11 }} stroke="var(--text-muted)" tickFormatter={yTick} width={yWidth} allowDecimals />
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
        {series.length > 1 && <Legend formatter={(value: string) => labelOf(value)} wrapperStyle={{ fontSize: 12 }} />}
        {series.map((s) => (
          <Line key={s.key} type="monotone" dataKey={s.key} name={s.key} stroke={s.color} strokeWidth={2} dot={false} activeDot={{ r: 4 }} />
        ))}
      </LineChart>
    </ResponsiveContainer>
  )
}
