import { useEffect, useState } from 'react'
import { CartesianGrid, Legend, Line, LineChart, ResponsiveContainer, Tooltip, XAxis, YAxis } from 'recharts'
import { hostsApi } from '../api/endpoints'
import type { MetricPoint } from '../types/api'
import { Modal } from '../components/Modal'
import { EmptyState } from '../components/EmptyState'
import { Activity, HardDrive } from 'lucide-react'

// Sabit kategorik tonlar (doğrulanmış varsayılan paletin 1. ve 2. yuvaları — bkz. dataviz
// skill'i): renk her zaman aynı seriyi tanımlar, grafik başına yeniden seçilmez.
const CPU_COLOR = '#2a78d6'
const RAM_COLOR = '#eb6834'
const DISK_COLORS = ['#2a78d6', '#eb6834', '#1baf7a', '#eda100', '#e87ba4', '#008300', '#4a3aa7', '#e34948']

type RangeKey = '1h' | '6h' | '24h' | '7d'
const RANGES: { key: RangeKey; label: string; hours: number }[] = [
  { key: '1h', label: 'Son 1 saat', hours: 1 },
  { key: '6h', label: 'Son 6 saat', hours: 6 },
  { key: '24h', label: 'Son 24 saat', hours: 24 },
  { key: '7d', label: 'Son 7 gün', hours: 24 * 7 },
]

const pad = (n: number) => String(n).padStart(2, '0')

function toInputValue(d: Date) {
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}T${pad(d.getHours())}:${pad(d.getMinutes())}`
}

// Aralık bir günden uzunsa saat:dakika:saniye tek başına belirsiz kalır (hangi gün?); tarihi de
// ekle — yerel biçime (ay/gün sırası belirsiz olabilir) değil, sabit gün-ay-yıl sırasına göre.
function formatPoint(ms: number, spanMs: number) {
  const d = new Date(ms)
  const time = `${pad(d.getHours())}:${pad(d.getMinutes())}:${pad(d.getSeconds())}`
  if (spanMs > 24 * 60 * 60 * 1000) {
    return `${pad(d.getDate())}-${pad(d.getMonth() + 1)}-${d.getFullYear()} ${time}`
  }
  return time
}

// Sunucu detay sayfasındaki "Genel" kartlarının "Detay" düğmesiyle açılan modal: CPU/RAM ya da
// disk kullanımının geçmişi, hazır aralıklarla ya da elle seçilen bir tarih aralığıyla. X ekseni
// seçilen aralığın TAMAMINI kapsar (veri olmayan kısımlar dahil): veri yalnızca aralığın bir
// bölümünde varsa, grafikte nerede başlayıp nerede bittiği böylece görülür.
export function HostMetricHistory({
  hostId,
  kind,
  open,
  onClose,
}: {
  hostId: string
  kind: 'cpu-ram' | 'disk'
  open: boolean
  onClose: () => void
}) {
  const [preset, setPreset] = useState<RangeKey | 'custom'>('1h')
  const [fromInput, setFromInput] = useState('')
  const [toInput, setToInput] = useState('')
  const [rangeStart, setRangeStart] = useState(0)
  const [rangeEnd, setRangeEnd] = useState(0)
  const [points, setPoints] = useState<MetricPoint[]>([])
  const [error, setError] = useState<string | null>(null)
  const [loading, setLoading] = useState(false)

  function load(from: Date, to: Date) {
    setFromInput(toInputValue(from))
    setToInput(toInputValue(to))
    setRangeStart(from.getTime())
    setRangeEnd(to.getTime())
    setLoading(true)
    setError(null)
    hostsApi
      .metrics(hostId, from.toISOString(), to.toISOString())
      .then(setPoints)
      .catch((err) => setError(err instanceof Error ? err.message : 'metrikler yüklenemedi'))
      .finally(() => setLoading(false))
  }

  useEffect(() => {
    if (!open) return
    setPreset('1h')
    const to = new Date()
    const from = new Date(to.getTime() - 60 * 60 * 1000)
    load(from, to)
    // Yalnızca modal her açıldığında sıfırdan yükle; preset/tarih değişiklikleri kendi
    // handler'larından tetiklenir.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open, hostId, kind])

  function applyPreset(key: RangeKey) {
    setPreset(key)
    const to = new Date()
    const hours = RANGES.find((r) => r.key === key)!.hours
    load(new Date(to.getTime() - hours * 60 * 60 * 1000), to)
  }

  function applyCustom() {
    const from = new Date(fromInput)
    const to = new Date(toInput)
    if (Number.isNaN(from.getTime()) || Number.isNaN(to.getTime())) {
      setError('geçersiz tarih')
      return
    }
    if (from >= to) {
      setError('başlangıç, bitişten önce olmalı')
      return
    }
    setPreset('custom')
    load(from, to)
  }

  const rangeMs = rangeEnd - rangeStart
  const diskMounts = kind === 'disk' ? [...new Set(points.flatMap((p) => p.disk.map((d) => d.mount)))].sort().slice(0, 8) : []
  const chartData = points.map((p) => {
    const row: Record<string, number> = { ts: new Date(p.timestamp).getTime() }
    if (kind === 'cpu-ram') {
      row.cpu = Number(p.cpu_usage_pct.toFixed(1))
      row.ram = Number(p.ram_usage_pct.toFixed(1))
    } else {
      for (const d of p.disk) {
        if (diskMounts.includes(d.mount)) row[d.mount] = Number(d.used_pct.toFixed(1))
      }
    }
    return row
  })

  return (
    <Modal open={open} title={kind === 'cpu-ram' ? 'CPU / RAM geçmişi' : 'Disk kullanımı geçmişi'} onClose={onClose}>
      <div className="segmented" role="group" aria-label="Hazır aralık">
        {RANGES.map((r) => (
          <button key={r.key} type="button" aria-pressed={preset === r.key} onClick={() => applyPreset(r.key)}>
            {r.label}
          </button>
        ))}
      </div>

      <div className="row" style={{ margin: '10px 0 16px' }}>
        <label className="visually-hidden" htmlFor="hist-from">Başlangıç</label>
        <input id="hist-from" type="datetime-local" value={fromInput} onChange={(e) => setFromInput(e.target.value)} />
        <span className="muted">–</span>
        <label className="visually-hidden" htmlFor="hist-to">Bitiş</label>
        <input id="hist-to" type="datetime-local" value={toInput} onChange={(e) => setToInput(e.target.value)} />
        <button className="btn btn-sm" type="button" onClick={applyCustom}>
          Uygula
        </button>
      </div>

      {error && <div className="error-banner">{error}</div>}

      {loading ? (
        <div className="muted">Yükleniyor…</div>
      ) : points.length === 0 ? (
        <EmptyState icon={kind === 'disk' ? HardDrive : Activity}>Bu aralıkta metrik verisi yok.</EmptyState>
      ) : (
        <ResponsiveContainer width="100%" height={340}>
          <LineChart data={chartData} margin={{ top: 8, right: 8, bottom: 24, left: -12 }}>
            <CartesianGrid strokeDasharray="3 3" stroke="var(--border)" vertical={false} />
            <XAxis
              dataKey="ts"
              type="number"
              scale="time"
              domain={[rangeStart, rangeEnd]}
              tickFormatter={(ms: number) => formatPoint(ms, rangeMs)}
              tick={{ fontSize: 11 }}
              stroke="var(--text-muted)"
              minTickGap={rangeMs > 24 * 60 * 60 * 1000 ? 70 : 48}
              angle={rangeMs > 24 * 60 * 60 * 1000 ? -20 : 0}
              textAnchor={rangeMs > 24 * 60 * 60 * 1000 ? 'end' : 'middle'}
              height={rangeMs > 24 * 60 * 60 * 1000 ? 40 : 24}
            />
            <YAxis domain={[0, 100]} tick={{ fontSize: 11 }} stroke="var(--text-muted)" unit="%" />
            <Tooltip
              contentStyle={{
                fontSize: 12,
                borderRadius: 8,
                background: 'var(--surface-1)',
                border: '1px solid var(--border)',
                color: 'var(--text-primary)',
              }}
              labelFormatter={(label) => formatPoint(Number(label), rangeMs)}
              formatter={(value, name) => [`${value}%`, kind === 'cpu-ram' ? (name === 'cpu' ? 'CPU' : 'RAM') : name]}
            />
            {(kind === 'cpu-ram' || diskMounts.length > 1) && (
              <Legend
                formatter={(value: string) => (kind === 'cpu-ram' ? (value === 'cpu' ? 'CPU' : 'RAM') : value)}
                wrapperStyle={{ fontSize: 12 }}
              />
            )}
            {kind === 'cpu-ram' ? (
              <>
                <Line type="monotone" dataKey="cpu" stroke={CPU_COLOR} strokeWidth={2} dot={false} activeDot={{ r: 4 }} />
                <Line type="monotone" dataKey="ram" stroke={RAM_COLOR} strokeWidth={2} dot={false} activeDot={{ r: 4 }} />
              </>
            ) : (
              diskMounts.map((mount, i) => (
                <Line
                  key={mount}
                  type="monotone"
                  dataKey={mount}
                  name={mount}
                  stroke={DISK_COLORS[i % DISK_COLORS.length]}
                  strokeWidth={2}
                  dot={false}
                  activeDot={{ r: 4 }}
                />
              ))
            )}
          </LineChart>
        </ResponsiveContainer>
      )}
    </Modal>
  )
}
