import { useEffect, useState } from 'react'
import { CartesianGrid, Legend, Line, LineChart, ResponsiveContainer, Tooltip, XAxis, YAxis } from 'recharts'
import { Activity, HardDrive } from 'lucide-react'
import { hostsApi } from '../api/endpoints'
import type { DiskUsage, Host, HostThresholdsResponse, MetricPoint } from '../types/api'
import { DiskGroupCard } from '../components/DiskGroupCard'
import { EmptyState } from '../components/EmptyState'
import { MountMeter } from '../components/MountMeter'
import { supportsHardwareSummary } from './agentStatus'
import { diskLayout } from './diskLayout'
import { DiskIoPreview, NetworkPreview, ProcessesPreview, TemperaturePreview } from './HostComingSoon'
import { RANGES, cpuRamRows, diskMounts, diskRows, formatPoint, isLongSpan, toInputValue, type RangeKey } from './metricHistory'
import { mountLevels } from './usage'

// Sabit kategorik tonlar (doğrulanmış varsayılan paletin 1. ve 2. yuvaları — bkz. dataviz
// skill'i): renk her zaman aynı seriyi tanımlar, grafik başına yeniden seçilmez.
const CPU_COLOR = '#2a78d6'
const RAM_COLOR = '#eb6834'
const DISK_COLORS = ['#2a78d6', '#eb6834', '#1baf7a', '#eda100', '#e87ba4', '#008300', '#4a3aa7', '#e34948']

const HOUR_MS = 60 * 60 * 1000

interface Series {
  key: string
  label: string
  color: string
}

// Sunucu sayfasının "Performans" sekmesi: zamana bağlı her şey tek zaman seçicinin altında. Bir kez yüklenen metrik
// noktaları hem CPU/RAM hem disk doluluğu grafiğini besler; X ekseni seçilen aralığın TAMAMINI kapsar (veri olmayan
// kısımlar dahil), böylece verinin nerede başlayıp bittiği görülür. Altında fiziksel diskler (son rapor) ve henüz
// gelmemiş metriklerin "Yakında" kartları durur.
export function HostPerformance({
  host,
  disk,
  thresholds,
}: {
  host: Host
  // Son raporlanan mount kullanımı (Genel sekmesiyle aynı veri).
  disk: DiskUsage[]
  thresholds: HostThresholdsResponse | null
}) {
  const [preset, setPreset] = useState<RangeKey | 'custom'>('1h')
  const [fromInput, setFromInput] = useState('')
  const [toInput, setToInput] = useState('')
  const [range, setRange] = useState({ start: 0, end: 0 })
  const [points, setPoints] = useState<MetricPoint[]>([])
  const [error, setError] = useState<string | null>(null)
  const [loading, setLoading] = useState(false)

  function load(from: Date, to: Date) {
    setFromInput(toInputValue(from))
    setToInput(toInputValue(to))
    setRange({ start: from.getTime(), end: to.getTime() })
    setLoading(true)
    setError(null)
    hostsApi
      .metrics(host.id, from.toISOString(), to.toISOString())
      .then(setPoints)
      .catch((err) => setError(err instanceof Error ? err.message : 'metrikler yüklenemedi'))
      .finally(() => setLoading(false))
  }

  useEffect(() => {
    const to = new Date()
    load(new Date(to.getTime() - HOUR_MS), to)
    // Yalnızca sunucu değişince sıfırdan yükle; aralık değişiklikleri kendi handler'larından tetiklenir.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [host.id])

  function applyPreset(key: RangeKey) {
    setPreset(key)
    const to = new Date()
    load(new Date(to.getTime() - RANGES.find((r) => r.key === key)!.hours * HOUR_MS), to)
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

  const mounts = diskMounts(points)
  const layout = diskLayout(host.physical_disks, disk)

  return (
    <div>
      <div className="card">
        <div className="segmented" role="group" aria-label="Hazır aralık">
          {RANGES.map((r) => (
            <button key={r.key} type="button" aria-pressed={preset === r.key} onClick={() => applyPreset(r.key)}>
              {r.label}
            </button>
          ))}
        </div>
        <div className="row" style={{ marginTop: 10 }}>
          <label className="visually-hidden" htmlFor="perf-from">
            Başlangıç
          </label>
          <input id="perf-from" type="datetime-local" value={fromInput} onChange={(e) => setFromInput(e.target.value)} />
          <span className="muted">–</span>
          <label className="visually-hidden" htmlFor="perf-to">
            Bitiş
          </label>
          <input id="perf-to" type="datetime-local" value={toInput} onChange={(e) => setToInput(e.target.value)} />
          <button className="btn btn-sm" type="button" onClick={applyCustom}>
            Uygula
          </button>
          <span className="muted" style={{ fontSize: 13 }}>
            Aralık bu sekmedeki tüm grafiklere uygulanır.
          </span>
        </div>
      </div>

      {error && <div className="error-banner">{error}</div>}

      <div className="card">
        <h2 className="card-title">
          <Activity size={16} strokeWidth={1.75} />
          CPU ve RAM
        </h2>
        <HistoryChart
          loading={loading}
          rows={cpuRamRows(points)}
          series={[
            { key: 'cpu', label: 'CPU', color: CPU_COLOR },
            { key: 'ram', label: 'RAM', color: RAM_COLOR },
          ]}
          range={range}
          emptyIcon={Activity}
        />
      </div>

      <div className="card">
        <h2 className="card-title">
          <HardDrive size={16} strokeWidth={1.75} />
          Disk doluluğu
        </h2>
        <HistoryChart
          loading={loading}
          rows={diskRows(points, mounts)}
          series={mounts.map((m, i) => ({ key: m, label: m, color: DISK_COLORS[i % DISK_COLORS.length] }))}
          range={range}
          emptyIcon={HardDrive}
        />
      </div>

      <h2 className="section-title">
        <HardDrive size={16} strokeWidth={1.75} />
        Fiziksel diskler
      </h2>
      {layout.groups.length === 0 ? (
        <div className="card">
          <EmptyState icon={HardDrive}>
            {!supportsHardwareSummary(host)
              ? 'Bu agent donanım özetini göndermiyor; fiziksel diskler, çekirdek sayısı ve toplam RAM için agent güncellenmeli.'
              : 'Fiziksel disk bilgisi yok (diskler keşfedilemedi).'}
          </EmptyState>
        </div>
      ) : (
        <div className="grid-2 disk-groups">
          {layout.groups.map((g) => (
            <DiskGroupCard key={g.disk.name} group={g} thresholds={thresholds} />
          ))}
        </div>
      )}

      {layout.groups.length > 0 && layout.unassigned.length > 0 && (
        <div className="card">
          <h2 className="card-title">Diğer bağlama noktaları</h2>
          <p className="card-desc">Fiziksel bir diske bağlanamayan dosya sistemleri (ağ paylaşımı, sanal dosya sistemi vb.).</p>
          <div className="mount-grid">
            {layout.unassigned.map((u) => (
              <MountMeter key={u.mount} mount={u.mount} usage={u} levels={mountLevels(thresholds?.thresholds, thresholds?.mount_thresholds, u.mount)} />
            ))}
          </div>
        </div>
      )}

      <DiskIoPreview />
      <div className="grid-2">
        <NetworkPreview />
        <TemperaturePreview />
      </div>
      <ProcessesPreview />
    </div>
  )
}

function HistoryChart({
  loading,
  rows,
  series,
  range,
  emptyIcon,
}: {
  loading: boolean
  rows: Record<string, number>[]
  series: Series[]
  range: { start: number; end: number }
  emptyIcon: typeof Activity
}) {
  if (loading) return <div className="muted">Yükleniyor…</div>
  if (rows.length === 0 || series.length === 0) return <EmptyState icon={emptyIcon}>Bu aralıkta metrik verisi yok.</EmptyState>
  const span = range.end - range.start
  const long = isLongSpan(span)
  const labelOf = (key: string) => series.find((s) => s.key === key)?.label ?? key
  return (
    <ResponsiveContainer width="100%" height={280}>
      <LineChart data={rows} margin={{ top: 8, right: 8, bottom: 8, left: -12 }}>
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
        <YAxis domain={[0, 100]} tick={{ fontSize: 11 }} stroke="var(--text-muted)" unit="%" />
        <Tooltip
          contentStyle={{
            fontSize: 12,
            borderRadius: 8,
            background: 'var(--surface-1)',
            border: '1px solid var(--border)',
            color: 'var(--text-primary)',
          }}
          labelFormatter={(label) => formatPoint(Number(label), span)}
          formatter={(value, name) => [`${value}%`, labelOf(String(name))]}
        />
        {series.length > 1 && <Legend formatter={(value: string) => labelOf(value)} wrapperStyle={{ fontSize: 12 }} />}
        {series.map((s) => (
          <Line key={s.key} type="monotone" dataKey={s.key} name={s.key} stroke={s.color} strokeWidth={2} dot={false} activeDot={{ r: 4 }} />
        ))}
      </LineChart>
    </ResponsiveContainer>
  )
}
