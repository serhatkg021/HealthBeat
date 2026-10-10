import type { ReactNode } from 'react'
import { Link } from 'react-router-dom'
import type { LucideIcon } from 'lucide-react'
import type { HostThresholdsResponse, MetricPoint, ProcessGroup, SystemState } from '../types/api'
import { InfoTip } from '../components/InfoTip'
import { StatusBadge } from '../components/StatusBadge'
import { UsageBar } from '../components/UsageBar'
import { formatMB } from './docker'
import type { RuleLine } from './perfTopics'
import { capacityRows, raidState, temperatureLevels, temperatureTone } from './systemState'
import { formatBitRate, formatCelsius, formatCount, formatPct } from './units'

// Performans konularının son rapor blokları (sensörler, kapasite, süreçler, RAID, ağ arayüzleri). Geçmişleri tutulmaz;
// agent'ın son raporunu gösterir. Grafik bloklarıyla aynı ızgarada durur.

export function PanelCard({ title, icon: Icon, desc, children }: { title: string; icon: LucideIcon; desc?: ReactNode; children: ReactNode }) {
  return (
    <div className="card">
      <h2 className="card-title">
        <Icon size={16} strokeWidth={1.75} />
        {title}
        {desc && <InfoTip label={title}>{desc}</InfoTip>}
      </h2>
      {children}
    </div>
  )
}

export function SensorsTable({ state, thresholds }: { state: SystemState; thresholds: HostThresholdsResponse | null }) {
  const temps = [...(state.temperatures ?? [])].sort((a, b) => a.sensor.localeCompare(b.sensor))
  if (temps.length === 0) return <p className="form-hint">Sensör bildirilmedi (sanal makinelerde ve sensörü okunamayan donanımda olmaz).</p>
  return (
    <table className="stack compact-table">
      <thead>
        <tr>
          <th>Sensör</th>
          <th>Sıcaklık</th>
          <th>Donanım sınırı</th>
          <th>Alert eşiği</th>
        </tr>
      </thead>
      <tbody>
        {temps.map((t) => {
          const levels = temperatureLevels(thresholds, t.sensor)
          const tone = temperatureTone(t, levels)
          const hw = [t.max !== undefined ? formatCelsius(t.max) : '', t.crit !== undefined ? `kritik ${formatCelsius(t.crit)}` : ''].filter(Boolean).join(' · ')
          return (
            <tr key={t.sensor}>
              <td className="primary mono">{t.sensor}</td>
              <td className="tnum" data-label="Sıcaklık">
                {tone === 'neutral' || tone === 'good' ? formatCelsius(t.celsius) : <StatusBadge tone={tone}>{formatCelsius(t.celsius)}</StatusBadge>}
              </td>
              <td className="tnum muted" data-label="Donanım sınırı">
                {hw || '—'}
              </td>
              <td className="tnum muted" data-label="Alert eşiği">
                {levels ? `${levels.warning_level} / ${levels.critical_level} °C` : 'tanımlı değil'}
              </td>
            </tr>
          )
        })}
      </tbody>
    </table>
  )
}

export function CapacityList({ state }: { state: SystemState }) {
  const rows = capacityRows(state.capacity)
  if (rows.length === 0) return <p className="form-hint">Bildirilmedi.</p>
  return (
    <div className="capacity-rows">
      {rows.map((r) => (
        <div key={r.label}>
          <div className="capacity-head">
            <span>{r.label}</span>
            <span className="tnum muted">{r.unlimited ? `${formatCount(r.used)} · sınır yok` : `${formatCount(r.used)} / ${formatCount(r.max)} · ${formatPct(r.pct)}`}</span>
          </div>
          {!r.unlimited && <UsageBar pct={r.pct} label={r.label} size="sm" />}
          <div className="form-hint">{r.hint}</div>
        </div>
      ))}
    </div>
  )
}

// En çok CPU ya da bellek kullanan süreç grupları; aynı adlı süreçler toplanır, komut satırı gelmez.
export function ProcessList({ state, kind }: { state: SystemState; kind: 'cpu' | 'ram' }) {
  const p = state.processes
  if (!p) return <p className="form-hint">Bildirilmedi.</p>
  const rows: ProcessGroup[] = (kind === 'cpu' ? p.top_cpu : p.top_ram) ?? []
  return (
    <>
      <p className="form-hint" style={{ margin: '0 0 6px' }}>
        Toplam {formatCount(p.total)} süreç
        {p.zombie > 0 && (
          <>
            {' '}
            · <StatusBadge tone="warning">{formatCount(p.zombie)} zombi</StatusBadge>
          </>
        )}
        . Aynı adlı süreçler toplanır.
      </p>
      {rows.length === 0 ? (
        <p className="form-hint">Bildirilmedi.</p>
      ) : (
        <table className="stack compact-table">
          <tbody>
            {rows.map((x) => (
              <tr key={x.name}>
                <td className="primary mono">{x.name}</td>
                <td className="tnum muted">{x.count > 1 ? `${x.count} süreç` : '1 süreç'}</td>
                <td className="tnum">{kind === 'cpu' ? formatPct(x.cpu_pct) : formatMB(x.rss_mb)}</td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
    </>
  )
}

export function RaidTable({ state }: { state: SystemState }) {
  const arrays = state.raid ?? []
  return (
    <table className="stack compact-table">
      <thead>
        <tr>
          <th>Dizi</th>
          <th>Seviye</th>
          <th>Durum</th>
          <th>Disk</th>
        </tr>
      </thead>
      <tbody>
        {arrays.map((a) => {
          const st = raidState(a)
          return (
            <tr key={a.name}>
              <td className="primary mono">{a.name}</td>
              <td data-label="Seviye">{a.level || '—'}</td>
              <td data-label="Durum">
                <StatusBadge tone={st.tone}>{st.label}</StatusBadge>
                {a.sync_pct !== undefined && <span className="muted tnum"> {formatPct(a.sync_pct)}</span>}
              </td>
              <td className="tnum" data-label="Disk">
                {a.active}/{a.devices} etkin
              </td>
            </tr>
          )
        })}
      </tbody>
    </table>
  )
}

// Son rapordaki bütün ağ arayüzleri, en yoğundan başlayarak; seçili olan işaretli.
export function InterfacesTable({ latest, selected }: { latest: MetricPoint | null; selected?: string }) {
  const rows = [...(latest?.net_io ?? [])].sort((a, b) => b.rx_bps + b.tx_bps - (a.rx_bps + a.tx_bps))
  if (rows.length === 0) return <p className="form-hint">Bildirilmedi.</p>
  return (
    <table className="stack compact-table">
      <thead>
        <tr>
          <th>Arayüz</th>
          <th>Gelen</th>
          <th>Giden</th>
        </tr>
      </thead>
      <tbody>
        {rows.map((n) => (
          <tr key={n.interface}>
            <td className="primary mono">
              {n.interface}
              {n.interface === selected && <span className="muted"> · grafikte</span>}
            </td>
            <td className="tnum" data-label="Gelen">
              {formatBitRate(n.rx_bps)}
            </td>
            <td className="tnum" data-label="Giden">
              {formatBitRate(n.tx_bps)}
            </td>
          </tr>
        ))}
      </tbody>
    </table>
  )
}

// Konunun alert kuralları: etkin kurallar sütunlu satırlarda (ad · değer · süre · kaynak), tanımlı olmayanlar altta tek
// satırda; sağ üstte Alert kurallarında o konuyu açan bağlantı.
// embedded: bir kartın içinde (Envanter), kendi kutusu olmadan üst çizgiyle ayrılır.
export function RuleList({ lines, to, canEdit, embedded = false }: { lines: RuleLine[]; to: string; canEdit: boolean; embedded?: boolean }) {
  const on = lines.filter((l) => l.on)
  const off = lines.filter((l) => !l.on)
  return (
    <div className={embedded ? 'perf-rules embedded' : 'perf-rules'}>
      <div className="perf-rules-head">
        <span className="perf-rules-title">Alert kuralları</span>
        <Link to={to}>{canEdit ? 'Alert kurallarında düzenle →' : 'Alert kurallarında gör →'}</Link>
      </div>
      {on.map((l) => (
        <div key={l.name} className="perf-rule">
          <span className="perf-rule-name">
            <i className={`perf-rule-dot ${l.tone}`} aria-hidden="true" />
            {l.name}
          </span>
          <span className="perf-rule-value tnum">
            {l.value}
            {l.extra && <span className="muted"> · {l.extra}</span>}
          </span>
          <span className="perf-rule-duration muted">{l.duration}</span>
          <span className="perf-rule-source muted">{l.source}</span>
        </div>
      ))}
      {on.length === 0 && off.length === 0 && <div className="perf-rule-off">Bu konuda etkin alert kuralı yok.</div>}
      {off.length > 0 && <div className="perf-rule-off">Tanımlı değil: {off.map((l) => l.name).join(' · ')}</div>}
    </div>
  )
}
