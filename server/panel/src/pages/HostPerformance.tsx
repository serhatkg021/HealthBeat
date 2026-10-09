import { useEffect, useState } from 'react'
import { Activity, HardDrive } from 'lucide-react'
import { hostsApi } from '../api/endpoints'
import type { DiskUsage, Host, HostThresholdsResponse, MetricPoint } from '../types/api'
import { ChartCard } from '../components/ChartCard'
import { thresholdReferences } from '../components/chartReferences'
import { DiskGroupCard } from '../components/DiskGroupCard'
import { EmptyState } from '../components/EmptyState'
import { HistoryChart } from '../components/HistoryChart'
import { MountMeter } from '../components/MountMeter'
import { supportsHardwareSummary } from './agentStatus'
import { diskLayout } from './diskLayout'
import { HostHealthCharts } from './HostHealthCharts'
import { RANGES, cpuRamRows, diskMounts, diskRows, toInputValue, type RangeKey } from './metricHistory'
import { mountLevels } from './usage'

// Sabit kategorik tonlar (doğrulanmış varsayılan paletin 1. ve 2. yuvaları — bkz. dataviz
// skill'i): renk her zaman aynı seriyi tanımlar, grafik başına yeniden seçilmez.
const CPU_COLOR = '#2a78d6'
const RAM_COLOR = '#eb6834'
const DISK_COLORS = ['#2a78d6', '#eb6834', '#1baf7a', '#eda100', '#e87ba4', '#008300', '#4a3aa7', '#e34948']

const HOUR_MS = 60 * 60 * 1000

// Sunucu sayfasının "Performans" sekmesi: zamana bağlı her şey tek zaman seçicinin altında. Bir kez yüklenen metrik
// noktaları hem CPU/RAM hem disk doluluğu grafiğini besler; X ekseni seçilen aralığın TAMAMINI kapsar (veri olmayan
// kısımlar dahil), böylece verinin nerede başlayıp bittiği görülür. Protokol 4 grafikleri (CPU ayrıntısı, PSI, disk G/Ç,
// ağ, swap ve TCP) aynı noktalardan çizilir ve imleci paylaşır; altında fiziksel diskler (son rapor) durur.
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

      <ChartCard title="CPU ve RAM" icon={Activity}>
        {(large) => (
          <HistoryChart
            loading={loading}
            rows={cpuRamRows(points)}
            series={[
              { key: 'cpu', label: 'CPU', color: CPU_COLOR },
              { key: 'ram', label: 'RAM', color: RAM_COLOR },
            ]}
            range={range}
            emptyIcon={Activity}
            height={large ? 460 : undefined}
            syncId="perf"
          />
        )}
      </ChartCard>

      <ChartCard title="Disk doluluğu" icon={HardDrive}>
        {(large) => (
          <HistoryChart
            loading={loading}
            rows={diskRows(points, mounts)}
            series={mounts.map((m, i) => ({ key: m, label: m, color: DISK_COLORS[i % DISK_COLORS.length] }))}
            range={range}
            emptyIcon={HardDrive}
            height={large ? 460 : undefined}
            syncId="perf"
            references={thresholdReferences(thresholds, 'disk', (v) => `%${v}`)}
          />
        )}
      </ChartCard>

      <HostHealthCharts host={host} points={points} range={range} loading={loading} thresholds={thresholds} />

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

    </div>
  )
}
