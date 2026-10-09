import { useEffect, useState, type ReactNode } from 'react'
import { useSearchParams } from 'react-router-dom'
import { Gauge, HardDrive, Layers, Network, Thermometer } from 'lucide-react'
import { alertsApi, hostsApi, organizationsApi, statusRulesApi, thresholdsApi } from '../api/endpoints'
import { useAuth } from '../auth/AuthContext'
import type { Alert, Host, HostStatusRuleView, HostThresholdsResponse, MetricPoint } from '../types/api'
import { alertRulesPath } from '../navigation'
import { DiskGroupCard } from '../components/DiskGroupCard'
import { EmptyState } from '../components/EmptyState'
import { MountMeter } from '../components/MountMeter'
import { StatTile } from '../components/StatTile'
import { TopicMenu } from '../components/TopicMenu'
import { useNow } from '../components/useNow'
import { supportsHardwareSummary } from './agentStatus'
import { healthEmptyText, supportsHealth } from './inventory'
import { diskLayout } from './diskLayout'
import { RANGES, toInputValue, type RangeKey } from './metricHistory'
import { busiestDisk, busiestInterface, ioDisks, netInterfaces } from './perfSeries'
import {
  CpuDetailChart,
  DiskIOChart,
  DiskUsageChart,
  NetTrafficChart,
  Picker,
  PressureChart,
  RetransChart,
  SwapChart,
  UsageChart,
  type ChartProps,
} from './PerfCharts'
import { CapacityList, InterfacesTable, PanelCard, ProcessList, RaidTable, RuleList, SensorsTable } from './PerfPanels'
import { hostRow, hostRuleState } from './hostRuleRows'
import { parentMap } from './orgTree'
import { hostSources, type RuleSources } from './scopeRules'
import {
  PERF_TOPICS,
  chartLayout,
  openAlertsByTopic,
  perfRuleItems,
  perfTiles,
  perfTopicInfo,
  resolvePerfTopic,
  ruleLine,
  topicNow,
  type PerfTopicId,
} from './perfTopics'
import { mountLevels } from './usage'

const HOUR_MS = 60 * 60 * 1000

// Aralıkta görülenlerden biri seçilir; seçili olan aralıktan çıkarsa varsayılana (en yoğun) döner.
function useChoice(options: string[], fallback: string | undefined): [string | undefined, (v: string) => void] {
  const [chosen, setChosen] = useState<string>()
  return [chosen && options.includes(chosen) ? chosen : fallback, setChosen]
}

interface Block {
  key: string
  // Satırı tek başına doldurur (ör. tablo); yoksa en çok iki blok yan yana durur.
  wide?: boolean
  node: ReactNode
}

// Sunucu sayfasının "Performans" sekmesi, konuya göre: üstte tek zaman seçici, solda konu menüsü (Özet, CPU, Bellek,
// Disk, Ağ, Sıcaklık, Sistem sınırları), sağda seçili konunun "şu an" kutuları ve blokları. Bir konunun geçmişi
// (grafikler) ile son raporu (süreçler, mount'lar, arayüzler, sensörler) yan yanadır; ana grafikte alert eşikleri kesikli
// çizgidir. Bir kez yüklenen metrik noktaları bütün grafikleri besler ve grafikler imleci paylaşır; X ekseni seçilen
// aralığın TAMAMINI kapsar. Seçili konu adreste (`?konu=`) tutulur.
export function HostPerformance({ host, latest, thresholds }: { host: Host; latest: MetricPoint | null; thresholds: HostThresholdsResponse | null }) {
  const [params, setParams] = useSearchParams()
  const topic = resolvePerfTopic(params.get('konu'))
  const [preset, setPreset] = useState<RangeKey | 'custom'>('1h')
  const [fromInput, setFromInput] = useState('')
  const [toInput, setToInput] = useState('')
  const [range, setRange] = useState({ start: 0, end: 0 })
  const [points, setPoints] = useState<MetricPoint[]>([])
  const [error, setError] = useState<string | null>(null)
  const [loading, setLoading] = useState(false)
  const now = useNow(60_000)
  const { can } = useAuth()
  const canSeeRules = can('threshold.view')
  const canSeeAlerts = can('alert.view')
  const canSeeOrgs = can('organization.view')
  const [statusRules, setStatusRules] = useState<HostStatusRuleView[] | null>(null)
  const [sources, setSources] = useState<RuleSources | undefined>(undefined)
  const [openAlerts, setOpenAlerts] = useState<Alert[]>([])

  // Konuların alert kuralları şeridi: durum kuralları, ve organizasyonları görebilene devralınan değerin kaynağı.
  // Okunamazlarsa şerit yalnızca eşikleri ya da hiç kaynak adı göstermez.
  useEffect(() => {
    if (!canSeeRules) return
    let cancelled = false
    hostsApi
      .statusRules(host.id)
      .then((r) => !cancelled && setStatusRules(r))
      .catch(() => undefined)
    if (canSeeOrgs) {
      Promise.all([organizationsApi.list(), thresholdsApi.list(), statusRulesApi.list()])
        .then(([orgs, t, s]) => {
          if (cancelled) return
          const nameOf = (id: string) => orgs.find((o) => o.id === id)?.name ?? 'üst şirket'
          setSources(hostSources(t, s, host.organization_id, parentMap(orgs), nameOf))
        })
        .catch(() => undefined)
    }
    return () => {
      cancelled = true
    }
  }, [host.id, host.organization_id, canSeeRules, canSeeOrgs])

  // Menüdeki açık alert noktaları.
  useEffect(() => {
    if (!canSeeAlerts) return
    let cancelled = false
    alertsApi
      .list({ status: 'open', hostId: host.id, q: '', limit: 200, offset: 0 })
      .then((page) => !cancelled && setOpenAlerts(page.items))
      .catch(() => undefined)
    return () => {
      cancelled = true
    }
  }, [host.id, canSeeAlerts])

  const disks = ioDisks(points)
  const [disk, setDisk] = useChoice(disks, busiestDisk(points))
  const ifaces = netInterfaces(points)
  const [iface, setIface] = useChoice(ifaces, busiestInterface(points))

  function selectTopic(id: PerfTopicId) {
    setParams(
      (prev) => {
        const next = new URLSearchParams(prev)
        if (id === PERF_TOPICS[0].id) next.delete('konu')
        else next.set('konu', id)
        return next
      },
      { replace: true },
    )
  }

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

  const state = host.system_state
  const chart: ChartProps = { points, range, loading, ...(supportsHealth(host) ? {} : { emptyText: healthEmptyText(host, 'Bu grafik') }) }
  const info = perfTopicInfo(topic)
  // Aralıkta veri yoksa (sunucu sessiz) kutular son rapordaki en yoğun disk ve arayüzü gösterir.
  const reported = latest ? [latest] : []
  const tiles = perfTiles(topic, {
    latest,
    state,
    cores: host.cpu_cores ?? undefined,
    ramTotalMB: host.ram_total_mb ?? undefined,
    info: host.host_info,
    disk: disk ?? busiestDisk(reported),
    iface: iface ?? busiestInterface(reported),
    now,
  })
  const alertDots = openAlertsByTopic(openAlerts)
  const menu = PERF_TOPICS.map((t) => {
    const value = topicNow(t.id, latest, state)
    const open = alertDots[t.id]
    return {
      id: t.id,
      title: t.title,
      hint: t.desc,
      ...(value ? { meta: value, metaTitle: 'son rapor' } : {}),
      ...(open ? { dot: { label: `${open.count} açık alert`, tone: open.level === 'critical' ? ('critical' as const) : open.level === 'warning' ? ('warning' as const) : ('accent' as const) } } : {}),
    }
  })
  // Konunun alert kuralları (eşikler sayfada yüklü; durum kuralları ayrı okunur).
  const ruleItems = perfRuleItems(topic)
  const ruleState = canSeeRules && thresholds && ruleItems.length > 0 ? hostRuleState(thresholds, statusRules ?? [], null, null) : null
  const ruleLines = ruleState
    ? ruleItems
        .filter((item) => item.kind === 'threshold' || statusRules !== null)
        .map((item) => ruleLine(hostRow(item, ruleState), item.kind === 'threshold' ? sources?.thresholds[item.metric] : sources?.status[item.rule]))
    : []
  const ruleTopic = info.rules?.topic
  const stateMissing = (what: string) => (
    <p className="form-hint">{healthEmptyText(host, what)}</p>
  )

  function blocks(): Block[] {
    switch (topic) {
      case 'ozet':
        return [
          { key: 'cpu', node: <UsageChart kind="cpu" thresholds={thresholds} {...chart} /> },
          { key: 'ram', node: <UsageChart kind="ram" thresholds={thresholds} {...chart} /> },
          { key: 'disk', node: <DiskUsageChart thresholds={thresholds} {...chart} /> },
          { key: 'net', node: <NetTrafficChart iface={iface} {...chart} /> },
        ]
      case 'cpu':
        return [
          { key: 'usage', node: <UsageChart kind="cpu" thresholds={thresholds} {...chart} /> },
          {
            key: 'procs',
            node: (
              <PanelCard title="En çok CPU kullananlar · son rapor" icon={Gauge}>
                {state ? <ProcessList state={state} kind="cpu" /> : stateMissing('Süreç listesi')}
              </PanelCard>
            ),
          },
          { key: 'detail', node: <CpuDetailChart {...chart} /> },
          { key: 'psi', node: <PressureChart resource="cpu" {...chart} /> },
        ]
      case 'bellek':
        return [
          { key: 'usage', node: <UsageChart kind="ram" thresholds={thresholds} {...chart} /> },
          {
            key: 'procs',
            node: (
              <PanelCard title="En çok bellek kullananlar · son rapor" icon={Gauge}>
                {state ? <ProcessList state={state} kind="ram" /> : stateMissing('Süreç listesi')}
              </PanelCard>
            ),
          },
          { key: 'psi', node: <PressureChart resource="memory" {...chart} /> },
          { key: 'swap', node: <SwapChart {...chart} /> },
        ]
      case 'disk': {
        const out: Block[] = [
          { key: 'usage', node: <DiskUsageChart thresholds={thresholds} {...chart} /> },
          { key: 'disks', node: <PhysicalDisks host={host} latest={latest} thresholds={thresholds} /> },
          { key: 'latency', node: <DiskIOChart metric="latency" disk={disk} thresholds={thresholds} {...chart} /> },
          { key: 'speed', node: <DiskIOChart metric="speed" disk={disk} thresholds={thresholds} {...chart} /> },
          { key: 'iops', node: <DiskIOChart metric="iops" disk={disk} thresholds={thresholds} {...chart} /> },
          { key: 'psi', node: <PressureChart resource="io" {...chart} /> },
        ]
        if (state?.raid && state.raid.length > 0) {
          out.push({
            key: 'raid',
            wide: true,
            node: (
              <PanelCard title="Yazılım RAID · son rapor" icon={HardDrive}>
                <RaidTable state={state} />
              </PanelCard>
            ),
          })
        }
        return out
      }
      case 'ag':
        return [
          { key: 'traffic', node: <NetTrafficChart iface={iface} {...chart} /> },
          {
            key: 'ifaces',
            node: (
              <PanelCard title="Arayüzler · son rapor" icon={Network}>
                <InterfacesTable latest={latest} selected={iface} />
              </PanelCard>
            ),
          },
          { key: 'retrans', node: <RetransChart {...chart} /> },
        ]
      case 'sicaklik':
        return [
          {
            key: 'sensors',
            wide: true,
            node: (
              <PanelCard title="Sensörler · son rapor" icon={Thermometer}>
                {state ? <SensorsTable state={state} thresholds={thresholds} /> : stateMissing('Sıcaklık')}
              </PanelCard>
            ),
          },
        ]
      case 'sinirlar':
        return [
          {
            key: 'capacity',
            wide: true,
            node: (
              <PanelCard title="Kapasite · son rapor" icon={Layers}>
                {state ? <CapacityList state={state} /> : stateMissing('Kapasite sınırları')}
              </PanelCard>
            ),
          },
        ]
    }
  }

  const list = blocks()
  const full = chartLayout(list)

  return (
    <div className="stack-col">
      {/* Tek satır: solda hazır aralıklar, sağda tarih aralığı. Aralık ve imleç bütün konulardaki grafiklerde ortak. */}
      <div className="card perf-range">
        <div className="segmented" role="group" aria-label="Hazır aralık">
          {RANGES.map((r) => (
            <button key={r.key} type="button" aria-pressed={preset === r.key} onClick={() => applyPreset(r.key)}>
              {r.label}
            </button>
          ))}
        </div>
        <div className="row row-tight perf-range-dates">
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
        </div>
      </div>

      {error && <div className="error-banner">{error}</div>}

      <div className="topic-layout">
        <TopicMenu items={menu} active={topic} onSelect={selectTopic} panelId="perf-paneli" />
        <section className="perf-panel" id="perf-paneli" role="tabpanel" aria-labelledby={`konu-${topic}`}>
          {/* Seçili konunun adı soldaki menüde; panelde yalnızca konunun seçicisi (disk, arayüz) durur. */}
          {topic === 'disk' && disks.length > 1 && (
            <div className="perf-head">
              <Picker id="perf-disk" label="Disk" options={disks} value={disk} onChange={setDisk} />
            </div>
          )}
          {(topic === 'ag' || topic === 'ozet') && ifaces.length > 1 && (
            <div className="perf-head">
              <Picker id="perf-iface" label="Arayüz" options={ifaces} value={iface} onChange={setIface} />
            </div>
          )}
          {tiles.length > 0 && (
            <div className="perf-tiles">
              {tiles.map((t) => (
                <StatTile key={t.label} label={t.label} value={t.value} hint={t.hint} small={t.value.length > 12} />
              ))}
            </div>
          )}
          {ruleLines.length > 0 && ruleTopic && (
            <RuleList lines={ruleLines} to={alertRulesPath({ kind: 'sunucu', id: host.id }, ruleTopic)} canEdit={can('threshold.edit')} />
          )}
          <div className="perf-grid">
            {list.map((b, i) => (
              <div key={b.key} className={full[i] ? 'perf-block full' : 'perf-block'}>
                {b.node}
              </div>
            ))}
          </div>
        </section>
      </div>
    </div>
  )
}

// Fiziksel diskler ve üzerlerindeki mount'lar (son rapor); bir diske bağlanamayanlar ayrı kartta.
function PhysicalDisks({ host, latest, thresholds }: { host: Host; latest: MetricPoint | null; thresholds: HostThresholdsResponse | null }) {
  const layout = diskLayout(host.physical_disks, latest?.disk ?? [])
  if (layout.groups.length === 0) {
    return (
      <PanelCard title="Fiziksel diskler · son rapor" icon={HardDrive}>
        <EmptyState icon={HardDrive}>
          {!supportsHardwareSummary(host)
            ? 'Bu agent donanım özetini göndermiyor; fiziksel diskler için agent güncellenmeli.'
            : 'Fiziksel disk bilgisi yok (diskler keşfedilemedi).'}
        </EmptyState>
      </PanelCard>
    )
  }
  return (
    <div className="stack-col">
      {layout.groups.map((g) => (
        <DiskGroupCard key={g.disk.name} group={g} thresholds={thresholds} />
      ))}
      {layout.unassigned.length > 0 && (
        <PanelCard title="Diğer bağlama noktaları" icon={HardDrive} desc="Fiziksel bir diske bağlanamayan dosya sistemleri (ağ paylaşımı, sanal dosya sistemi vb.).">
          <div className="mount-grid">
            {layout.unassigned.map((u) => (
              <MountMeter key={u.mount} mount={u.mount} usage={u} levels={mountLevels(thresholds?.thresholds, thresholds?.mount_thresholds, u.mount)} />
            ))}
          </div>
        </PanelCard>
      )}
    </div>
  )
}
