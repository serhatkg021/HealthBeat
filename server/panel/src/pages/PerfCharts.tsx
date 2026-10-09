import { Activity, Cpu, Gauge, HardDrive, HardDriveDownload, MemoryStick, Network } from 'lucide-react'
import type { HostThresholdsResponse, MetricPoint } from '../types/api'
import { ChartCard } from '../components/ChartCard'
import { thresholdReferences } from '../components/chartReferences'
import { HistoryChart, type Series } from '../components/HistoryChart'
import { cpuRamRows, diskMounts, diskRows } from './metricHistory'
import { cpuDetailRows, diskIORows, netErrorTotals, netRows, psiRows, swapRows, tcpRows } from './perfSeries'
import { formatBitRate, formatByteRate, formatCount, formatIOPS, formatMs, formatPct, formatPerSecond, formatTick } from './units'

// Performans konularının grafik blokları. Hepsi aynı noktalardan çizilir ve imleci paylaşır (SYNC); her biri büyütülebilir
// (ChartCard). Değerler ham gelir, gösterimde yuvarlanır. Eski agent'ın satırlarında protokol 4 serileri yoktur: o
// grafikler `emptyText` ile neden boş olduğunu söyler.

// Sabit kategorik tonlar (doğrulanmış varsayılan paletin 1. ve 2. yuvaları — bkz. dataviz skill'i): renk her zaman aynı
// seriyi tanımlar; iki seri olduğunda ilki mavi, ikincisi turuncu (okuma/yazma, gelen/giden, some/full, iowait/steal).
const C1 = '#2a78d6'
const C2 = '#eb6834'
const DISK_COLORS = ['#2a78d6', '#eb6834', '#1baf7a', '#eda100', '#e87ba4', '#008300', '#4a3aa7', '#e34948']

const SYNC = 'perf'

const pctTick = (v: number) => `%${formatTick(v)}`

export interface ChartProps {
  points: MetricPoint[]
  range: { start: number; end: number }
  loading: boolean
  // Protokol 4 grafiklerinin boş durum metni (eski agent'ta neden yok).
  emptyText?: string
}

// Grafiğin yüksekliği: kartta ve büyütülmüş pencerede.
const h = (large: boolean, normal = 220) => (large ? 440 : normal)

export function UsageChart({ kind, points, range, loading, thresholds }: ChartProps & { kind: 'cpu' | 'ram'; thresholds: HostThresholdsResponse | null }) {
  const cpu = kind === 'cpu'
  return (
    <ChartCard title={cpu ? 'CPU kullanımı' : 'Bellek kullanımı'} icon={cpu ? Activity : MemoryStick}>
      {(large) => (
        <HistoryChart
          loading={loading}
          rows={cpuRamRows(points)}
          series={[cpu ? { key: 'cpu', label: 'CPU', color: C1 } : { key: 'ram', label: 'RAM', color: C2 }]}
          range={range}
          emptyIcon={cpu ? Activity : MemoryStick}
          height={h(large)}
          format={(v) => formatPct(v)}
          syncId={SYNC}
          references={thresholdReferences(thresholds, kind, (v) => `%${v}`)}
        />
      )}
    </ChartCard>
  )
}

export function DiskUsageChart({ points, range, loading, thresholds }: ChartProps & { thresholds: HostThresholdsResponse | null }) {
  const mounts = diskMounts(points)
  return (
    <ChartCard title="Disk doluluğu" icon={HardDrive}>
      {(large) => (
        <HistoryChart
          loading={loading}
          rows={diskRows(points, mounts)}
          series={mounts.map((m, i) => ({ key: m, label: m, color: DISK_COLORS[i % DISK_COLORS.length] }))}
          range={range}
          emptyIcon={HardDrive}
          height={h(large)}
          format={(v) => formatPct(v)}
          syncId={SYNC}
          references={thresholdReferences(thresholds, 'disk', (v) => `%${v}`)}
        />
      )}
    </ChartCard>
  )
}

export function CpuDetailChart({ points, range, loading, emptyText }: ChartProps) {
  return (
    <ChartCard
      title="iowait ve steal"
      icon={Cpu}
      desc={
        <>
          <strong>iowait</strong>: CPU’nun diski beklerken boşta geçirdiği zaman. <strong>steal</strong>: sanal makinede hipervizörün
          CPU’yu başka makinelere verdiği zaman; sürekli yüksekse makine yeterli CPU alamıyordur.
        </>
      }
    >
      {(large) => (
        <HistoryChart
          loading={loading}
          rows={cpuDetailRows(points)}
          series={[
            { key: 'iowait', label: 'iowait', color: C1 },
            { key: 'steal', label: 'steal', color: C2 },
          ]}
          range={range}
          emptyIcon={Cpu}
          emptyText={emptyText}
          height={h(large, 200)}
          yDomain={[0, 'auto']}
          yTick={pctTick}
          format={(v) => formatPct(v)}
          syncId={SYNC}
        />
      )}
    </ChartCard>
  )
}

const PSI: Record<'cpu' | 'memory' | 'io', { title: string; desc: string; series: Series[] }> = {
  cpu: { title: 'Baskı (PSI) · CPU', desc: 'Süreçlerin CPU beklerken geçirdiği zamanın yüzdesi (60 sn ortalaması).', series: [{ key: 'cpu_some', label: 'some', color: C1 }] },
  memory: {
    title: 'Baskı (PSI) · bellek',
    desc: 'some: en az bir süreç belleği bekledi; full: çalışmak isteyen süreçlerin hepsi aynı anda bekledi (60 sn ortalaması).',
    series: [
      { key: 'mem_some', label: 'some', color: C1 },
      { key: 'mem_full', label: 'full', color: C2 },
    ],
  },
  io: {
    title: 'Baskı (PSI) · G/Ç',
    desc: 'some: en az bir süreç diski bekledi; full: hepsi aynı anda bekledi. Doluluk yüzdesinin aksine işlerin ne kadar yavaşladığını gösterir.',
    series: [
      { key: 'io_some', label: 'some', color: C1 },
      { key: 'io_full', label: 'full', color: C2 },
    ],
  },
}

export function PressureChart({ resource, points, range, loading, emptyText }: ChartProps & { resource: 'cpu' | 'memory' | 'io' }) {
  const c = PSI[resource]
  return (
    <ChartCard title={c.title} icon={Gauge} desc={c.desc}>
      {(large) => (
        <HistoryChart
          loading={loading}
          rows={psiRows(points)}
          series={c.series}
          range={range}
          emptyIcon={Gauge}
          emptyText={emptyText ?? 'Bu aralıkta PSI verisi yok (çekirdek 4.20 öncesi ya da PSI kapalı).'}
          height={h(large, 200)}
          yDomain={[0, 'auto']}
          yTick={pctTick}
          format={(v) => formatPct(v)}
          syncId={SYNC}
        />
      )}
    </ChartCard>
  )
}

export function SwapChart({ points, range, loading, emptyText }: ChartProps) {
  return (
    <ChartCard title="Swap" icon={MemoryStick} desc="Swap’ten okunan ve swap’e yazılan sayfa/sn. Sürekli yazılıyorsa bellek yetmiyordur.">
      {(large) => (
        <HistoryChart
          loading={loading}
          rows={swapRows(points)}
          series={[
            { key: 'swap_in', label: 'okunan', color: C1 },
            { key: 'swap_out', label: 'yazılan', color: C2 },
          ]}
          range={range}
          emptyIcon={MemoryStick}
          emptyText={emptyText}
          height={h(large, 200)}
          yDomain={[0, 'auto']}
          yTick={formatTick}
          format={(v) => formatPerSecond(v)}
          syncId={SYNC}
        />
      )}
    </ChartCard>
  )
}

// Disk G/Ç'nin üç grafiği (gecikme, hız, IOPS) seçili fiziksel disk içindir; disk seçici konunun başındadır.
export function DiskIOChart({
  metric,
  disk,
  points,
  range,
  loading,
  emptyText,
  thresholds,
}: ChartProps & { metric: 'latency' | 'speed' | 'iops'; disk?: string; thresholds: HostThresholdsResponse | null }) {
  const rows = disk ? diskIORows(points, disk) : []
  const name = disk ? ` · ${disk}` : ''
  const common = { loading, rows, range, emptyIcon: HardDriveDownload, emptyText: emptyText ?? 'Bu aralıkta disk G/Ç verisi yok.', yDomain: [0, 'auto'] as [number, 'auto'], syncId: SYNC, yWidth: 76 }
  if (metric === 'latency') {
    return (
      <ChartCard title={`Gecikme${name}`} icon={HardDriveDownload} desc="Bir disk işleminin ortalama süresi. Doluluk normalken diskin boğulduğunu gecikme gösterir.">
        {(large) => (
          <HistoryChart
            {...common}
            height={h(large, 200)}
            series={[{ key: 'await', label: 'gecikme', color: C1 }]}
            yTick={(v) => `${formatTick(v)} ms`}
            format={(v) => formatMs(v)}
            references={thresholdReferences(thresholds, 'disk_latency', (v) => formatMs(v))}
          />
        )}
      </ChartCard>
    )
  }
  if (metric === 'speed') {
    return (
      <ChartCard title={`Hız${name}`} icon={HardDriveDownload}>
        {(large) => (
          <HistoryChart
            {...common}
            height={h(large, 200)}
            series={[
              { key: 'read_bps', label: 'okuma', color: C1 },
              { key: 'write_bps', label: 'yazma', color: C2 },
            ]}
            yTick={(v) => formatByteRate(v)}
            format={(v) => formatByteRate(v)}
          />
        )}
      </ChartCard>
    )
  }
  return (
    <ChartCard title={`IOPS${name}`} icon={HardDriveDownload}>
      {(large) => (
        <HistoryChart
          {...common}
          height={h(large, 200)}
          series={[
            { key: 'read_iops', label: 'okuma', color: C1 },
            { key: 'write_iops', label: 'yazma', color: C2 },
          ]}
          yTick={formatTick}
          format={(v) => formatIOPS(v)}
        />
      )}
    </ChartCard>
  )
}

export function NetTrafficChart({ iface, points, range, loading, emptyText }: ChartProps & { iface?: string }) {
  const totals = iface ? netErrorTotals(points, iface) : null
  return (
    <ChartCard title={iface ? `Trafik · ${iface}` : 'Trafik'} icon={Network}>
      {(large) => (
        <>
          <HistoryChart
            loading={loading}
            rows={iface ? netRows(points, iface) : []}
            series={[
              { key: 'rx', label: 'gelen', color: C1 },
              { key: 'tx', label: 'giden', color: C2 },
            ]}
            range={range}
            emptyIcon={Network}
            emptyText={emptyText ?? 'Bu aralıkta ağ verisi yok.'}
            height={h(large)}
            yDomain={[0, 'auto']}
            yWidth={84}
            yTick={(v) => formatBitRate(v)}
            format={(v) => formatBitRate(v)}
            syncId={SYNC}
          />
          {totals && (
            <p className="form-hint">
              Bu aralıkta hata: gelen {formatCount(totals.rx_errors)} · giden {formatCount(totals.tx_errors)} — düşen paket: gelen{' '}
              {formatCount(totals.rx_drops)} · giden {formatCount(totals.tx_drops)}
            </p>
          )}
        </>
      )}
    </ChartCard>
  )
}

export function RetransChart({ points, range, loading, emptyText }: ChartProps) {
  return (
    <ChartCard title="TCP yeniden iletim" icon={Network} desc="Yeniden iletilen segmentlerin oranı; ağda kayıp ya da tıkanma varsa yükselir.">
      {(large) => (
        <HistoryChart
          loading={loading}
          rows={tcpRows(points)}
          series={[{ key: 'retrans', label: 'yeniden iletim', color: C1 }]}
          range={range}
          emptyIcon={Network}
          emptyText={emptyText}
          height={h(large, 200)}
          yDomain={[0, 'auto']}
          yTick={pctTick}
          format={(v) => formatPct(v, 2)}
          syncId={SYNC}
        />
      )}
    </ChartCard>
  )
}

// Seçiciler (disk, arayüz): aralıkta görülenlerden biri; tek seçenek varsa yalnızca adı yazılır.
export function Picker({ id, label, options, value, onChange }: { id: string; label: string; options: string[]; value?: string; onChange: (v: string) => void }) {
  if (options.length < 2) return value ? <code>{value}</code> : null
  return (
    <span className="row row-tight">
      <label htmlFor={id} className="muted">
        {label}
      </label>
      <select id={id} value={value} onChange={(e) => onChange(e.target.value)}>
        {options.map((o) => (
          <option key={o} value={o}>
            {o}
          </option>
        ))}
      </select>
    </span>
  )
}
