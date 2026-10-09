import { useState } from 'react'
import { Cpu, Gauge, HardDriveDownload, MemoryStick, Network } from 'lucide-react'
import type { Host, HostThresholdsResponse, MetricPoint } from '../types/api'
import { ChartCard } from '../components/ChartCard'
import { thresholdReferences } from '../components/chartReferences'
import { EmptyState } from '../components/EmptyState'
import { HistoryChart, type Series } from '../components/HistoryChart'
import { supportsHealth } from './inventory'
import {
  busiestDisk,
  busiestInterface,
  cpuDetailRows,
  diskIORows,
  hasHealthSeries,
  ioDisks,
  latestTcp,
  netErrorTotals,
  netInterfaces,
  netRows,
  psiMax,
  psiRows,
  swapRows,
  tcpRows,
} from './perfSeries'
import { formatBitRate, formatByteRate, formatCount, formatIOPS, formatMs, formatPct, formatPerSecond, formatTick } from './units'

// Doğrulanmış varsayılan paletin 1. ve 2. yuvaları (CPU/RAM grafiğiyle aynı); bir grafikte iki seri olduğunda ilki mavi,
// ikincisi turuncu: okuma/yazma, gelen/giden, some/full, iowait/steal.
const C1 = '#2a78d6'
const C2 = '#eb6834'

// Bütün Performans grafikleri imleci zamana göre paylaşır.
const SYNC = 'perf'

type Range = { start: number; end: number }

const pctTick = (v: number) => `%${formatTick(v)}`

// Performans sekmesinin protokol 4 grafikleri (CPU ayrıntısı, PSI, disk G/Ç, ağ, swap ve TCP). Değerler ham gelir,
// gösterimde yuvarlanır. Eski agent'ın satırlarında bu seriler yoktur.
export function HostHealthCharts({
  host,
  points,
  range,
  loading,
  thresholds,
}: {
  host: Host
  points: MetricPoint[]
  range: Range
  loading: boolean
  thresholds: HostThresholdsResponse | null
}) {
  if (!loading && !hasHealthSeries(points)) {
    return (
      <div className="card">
        <EmptyState icon={Gauge}>
          {supportsHealth(host)
            ? 'Bu aralıkta CPU ayrıntısı, PSI, disk G/Ç ve ağ verisi yok.'
            : 'CPU ayrıntısı, PSI, disk G/Ç ve ağ bu agent sürümünde toplanmıyor; agent güncellenince görünür (protokol 4).'}
        </EmptyState>
      </div>
    )
  }
  return (
    <>
      <CpuDetail points={points} range={range} loading={loading} />
      <Pressure points={points} range={range} loading={loading} />
      <DiskIO points={points} range={range} loading={loading} thresholds={thresholds} />
      <NetworkTraffic points={points} range={range} loading={loading} />
      <SwapAndTcp points={points} range={range} loading={loading} />
    </>
  )
}

interface ChartProps {
  points: MetricPoint[]
  range: Range
  loading: boolean
}

function CpuDetail({ points, range, loading }: ChartProps) {
  return (
    <ChartCard
      title="CPU ayrıntısı"
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
          height={large ? 420 : 200}
          yDomain={[0, 'auto']}
          yTick={pctTick}
          format={(v) => formatPct(v)}
          syncId={SYNC}
        />
      )}
    </ChartCard>
  )
}

const PSI_CHARTS: { title: string; series: Series[] }[] = [
  { title: 'CPU', series: [{ key: 'cpu_some', label: 'some', color: C1 }] },
  {
    title: 'Bellek',
    series: [
      { key: 'mem_some', label: 'some', color: C1 },
      { key: 'mem_full', label: 'full', color: C2 },
    ],
  },
  {
    title: 'G/Ç (disk)',
    series: [
      { key: 'io_some', label: 'some', color: C1 },
      { key: 'io_full', label: 'full', color: C2 },
    ],
  },
]

function Pressure({ points, range, loading }: ChartProps) {
  const rows = psiRows(points)
  const max = psiMax(rows)
  return (
    <ChartCard
      title="Baskı (PSI)"
      icon={Gauge}
      desc={
        <>
          Süreçlerin bir kaynağı beklerken geçirdiği zamanın yüzdesi (60 sn ortalaması). <strong>some</strong>: en az bir süreç
          bekledi; <strong>full</strong>: çalışmak isteyen süreçlerin hepsi aynı anda bekledi. Doluluk yüzdesinin aksine işlerin
          ne kadar yavaşladığını gösterir. Üç grafik aynı ölçektedir.
        </>
      }
    >
      {(large) => (
        <div className={large ? undefined : 'psi-charts'}>
          {PSI_CHARTS.map((c) => (
            <div key={c.title}>
              <h3 className="chart-subtitle">{c.title}</h3>
              <HistoryChart
                loading={loading}
                rows={rows}
                series={c.series}
                range={range}
                emptyIcon={Gauge}
                emptyText="Bu aralıkta PSI verisi yok (çekirdek 4.20 öncesi ya da PSI kapalı)."
                height={large ? 200 : 150}
                yDomain={[0, max]}
                yTick={pctTick}
                format={(v) => formatPct(v)}
                syncId={SYNC}
              />
            </div>
          ))}
        </div>
      )}
    </ChartCard>
  )
}

// Aralıkta görülenlerden biri seçilir; seçili olan aralıktan çıkarsa varsayılana (en çok trafikli) döner.
function useChoice(options: string[], fallback: string | undefined): [string | undefined, (v: string) => void] {
  const [chosen, setChosen] = useState<string>()
  return [chosen && options.includes(chosen) ? chosen : fallback, setChosen]
}

function Picker({ id, label, options, value, onChange }: { id: string; label: string; options: string[]; value?: string; onChange: (v: string) => void }) {
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

function DiskIO({ points, range, loading, thresholds }: ChartProps & { thresholds: HostThresholdsResponse | null }) {
  const disks = ioDisks(points)
  const [disk, setDisk] = useChoice(disks, busiestDisk(points))
  const rows = disk ? diskIORows(points, disk) : []
  const refs = thresholdReferences(thresholds, 'disk_latency', (v) => formatMs(v))
  return (
    <ChartCard
      title={disk ? `Disk G/Ç · ${disk}` : 'Disk G/Ç'}
      icon={HardDriveDownload}
      desc="Fiziksel disk başına. Doluluk normalken diskin boğulduğunu gecikme gösterir: bir işlemin ortalama süresi."
      actions={<Picker id="perf-disk" label="Disk" options={disks} value={disk} onChange={setDisk} />}
    >
      {(large) => {
        const common = { loading, rows, range, emptyIcon: HardDriveDownload, height: large ? 240 : 170, yDomain: [0, 'auto'] as [number, 'auto'], syncId: SYNC, yWidth: 76 }
        if (disks.length === 0 && !loading) return <EmptyState icon={HardDriveDownload}>Bu aralıkta disk G/Ç verisi yok.</EmptyState>
        return (
          <>
            <h3 className="chart-subtitle">Gecikme</h3>
            <HistoryChart {...common} series={[{ key: 'await', label: 'gecikme', color: C1 }]} yTick={(v) => `${formatTick(v)} ms`} format={(v) => formatMs(v)} references={refs} />
            <h3 className="chart-subtitle">Hız</h3>
            <HistoryChart
              {...common}
              series={[
                { key: 'read_bps', label: 'okuma', color: C1 },
                { key: 'write_bps', label: 'yazma', color: C2 },
              ]}
              yTick={(v) => formatByteRate(v)}
              format={(v) => formatByteRate(v)}
            />
            <h3 className="chart-subtitle">IOPS</h3>
            <HistoryChart
              {...common}
              series={[
                { key: 'read_iops', label: 'okuma', color: C1 },
                { key: 'write_iops', label: 'yazma', color: C2 },
              ]}
              yTick={formatTick}
              format={(v) => formatIOPS(v)}
            />
          </>
        )
      }}
    </ChartCard>
  )
}

function NetworkTraffic({ points, range, loading }: ChartProps) {
  const ifaces = netInterfaces(points)
  const [iface, setIface] = useChoice(ifaces, busiestInterface(points))
  const totals = iface ? netErrorTotals(points, iface) : null
  return (
    <ChartCard title={iface ? `Ağ · ${iface}` : 'Ağ'} icon={Network} actions={<Picker id="perf-iface" label="Arayüz" options={ifaces} value={iface} onChange={setIface} />}>
      {(large) =>
        ifaces.length === 0 && !loading ? (
          <EmptyState icon={Network}>Bu aralıkta ağ verisi yok.</EmptyState>
        ) : (
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
              height={large ? 420 : 200}
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
        )
      }
    </ChartCard>
  )
}

function SwapAndTcp({ points, range, loading }: ChartProps) {
  const tcp = latestTcp(points)
  return (
    <div className="grid-2">
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
            height={large ? 420 : 180}
            yDomain={[0, 'auto']}
            yTick={formatTick}
            format={(v) => formatPerSecond(v)}
            syncId={SYNC}
          />
        )}
      </ChartCard>
      <ChartCard
        title="TCP"
        icon={Network}
        desc={
          <>
            Yeniden iletilen segmentlerin oranı; ağda kayıp ya da tıkanma varsa yükselir.
            {tcp && (
              <>
                {' '}
                Son raporda {formatCount(tcp.established)} kurulu bağlantı, {formatCount(tcp.time_wait)} TIME_WAIT.
              </>
            )}
          </>
        }
      >
        {(large) => (
          <HistoryChart
            loading={loading}
            rows={tcpRows(points)}
            series={[{ key: 'retrans', label: 'yeniden iletim', color: C1 }]}
            range={range}
            emptyIcon={Network}
            height={large ? 420 : 180}
            yDomain={[0, 'auto']}
            yTick={pctTick}
            format={(v) => formatPct(v, 2)}
            syncId={SYNC}
          />
        )}
      </ChartCard>
    </div>
  )
}
