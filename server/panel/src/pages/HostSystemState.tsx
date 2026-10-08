import type { ReactNode } from 'react'
import { Clock, Gauge, HardDrive, Layers, MemoryStick, PackageOpen, Thermometer, type LucideIcon } from 'lucide-react'
import type { Host, HostThresholdsResponse, ProcessGroup, SystemState } from '../types/api'
import { EmptyState } from '../components/EmptyState'
import { StatusBadge } from '../components/StatusBadge'
import { UsageBar } from '../components/UsageBar'
import { useNow } from '../components/useNow'
import { agoText } from './cache'
import { formatMB } from './docker'
import { healthEmptyText } from './inventory'
import { capacityRows, daemonLabel, raidState, reachText, temperatureLevels, temperatureTone, timeSourceState, timeSyncIssues } from './systemState'
import { formatCelsius, formatCount, formatMs, formatOffsetMs, formatPct } from './units'

function Card({ title, icon: Icon, children }: { title: string; icon: LucideIcon; children: ReactNode }) {
  return (
    <div className="card">
      <h2 className="card-title">
        <Icon size={16} strokeWidth={1.75} />
        {title}
      </h2>
      {children}
    </div>
  )
}

function Row({ label, children }: { label: string; children: ReactNode }) {
  return (
    <div className="info-row">
      <dt>{label}</dt>
      <dd>{children}</dd>
    </div>
  )
}

const when = (at: string, now: number) => (
  <span title={new Date(at).toLocaleString('tr-TR')}>{agoText(at, now)}</span>
)

// Envanter sekmesindeki anlık durumlar (protokol 4): sıcaklık, süreçler, güncellemeler, kapasite, saat senkronu, RAID
// ve bellek yetmezliği. Geçmişleri tutulmaz; agent'ın son raporunu gösterir.
export function HostSystemState({ host, thresholds }: { host: Host; thresholds: HostThresholdsResponse | null }) {
  const now = useNow(60_000)
  const s = host.system_state
  if (!s) {
    return (
      <div className="card">
        <EmptyState icon={Gauge}>{healthEmptyText(host, 'Sıcaklık, süreçler, güncellemeler, kapasite ve saat senkronu')}</EmptyState>
      </div>
    )
  }
  return (
    <div className="grid-2">
      <div className="stack-col">
        <Temperatures state={s} thresholds={thresholds} />
        <CapacityCard state={s} />
        <Updates state={s} now={now} />
        <OOM state={s} now={now} />
      </div>
      <div className="stack-col">
        <TimeSyncCard state={s} now={now} />
        <ProcessesCard state={s} />
        <RAIDCard state={s} />
      </div>
    </div>
  )
}

function Temperatures({ state, thresholds }: { state: SystemState; thresholds: HostThresholdsResponse | null }) {
  const temps = [...(state.temperatures ?? [])].sort((a, b) => a.sensor.localeCompare(b.sensor))
  return (
    <Card title="Sıcaklık" icon={Thermometer}>
      {temps.length === 0 ? (
        <p className="form-hint">Sensör bildirilmedi (sanal makinelerde ve sensörü okunamayan donanımda olmaz).</p>
      ) : (
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
      )}
    </Card>
  )
}

function CapacityCard({ state }: { state: SystemState }) {
  const rows = capacityRows(state.capacity)
  return (
    <Card title="Kapasite sınırları" icon={Layers}>
      {rows.length === 0 ? (
        <p className="form-hint">Bildirilmedi.</p>
      ) : (
        <div className="capacity-rows">
          {rows.map((r) => (
            <div key={r.label}>
              <div className="capacity-head">
                <span>{r.label}</span>
                <span className="tnum muted">
                  {r.unlimited ? `${formatCount(r.used)} · sınır yok` : `${formatCount(r.used)} / ${formatCount(r.max)} · ${formatPct(r.pct)}`}
                </span>
              </div>
              {!r.unlimited && <UsageBar pct={r.pct} label={r.label} size="sm" />}
              <div className="form-hint">{r.hint}</div>
            </div>
          ))}
        </div>
      )}
    </Card>
  )
}

function Updates({ state, now }: { state: SystemState; now: number }) {
  const u = state.updates
  return (
    <Card title="Bekleyen güncellemeler" icon={PackageOpen}>
      {!u ? (
        <p className="form-hint">Bildirilmedi (paket yöneticisi desteklenmiyor ya da okunamadı).</p>
      ) : (
        <dl className="info-list single">
          <Row label="Bekleyen paket">{formatCount(u.pending)}</Row>
          <Row label="Güvenlik güncellemesi">{u.security > 0 ? <StatusBadge tone="critical">{formatCount(u.security)}</StatusBadge> : '0'}</Row>
          <Row label="Paket listesi güncellendi">{u.lists_updated_at ? when(u.lists_updated_at, now) : '—'}</Row>
        </dl>
      )}
    </Card>
  )
}

function OOM({ state, now }: { state: SystemState; now: number }) {
  if (state.oom_kills === undefined) return null
  return (
    <Card title="Bellek yetmezliği (OOM)" icon={MemoryStick}>
      <dl className="info-list single">
        <Row label="Açılıştan beri öldürülen süreç">{state.oom_kills > 0 ? <StatusBadge tone="warning">{formatCount(state.oom_kills)}</StatusBadge> : '0'}</Row>
        <Row label="Son artış">{state.oom_last_increase_at ? when(state.oom_last_increase_at, now) : 'görülmedi'}</Row>
      </dl>
    </Card>
  )
}

function TimeSyncCard({ state, now }: { state: SystemState; now: number }) {
  const t = state.time_sync
  if (!t) {
    return (
      <Card title="Saat senkronu" icon={Clock}>
        <p className="form-hint">Ayrıntı bildirilmedi.</p>
      </Card>
    )
  }
  const issues = timeSyncIssues(t, now)
  return (
    <Card title="Saat senkronu" icon={Clock}>
      <dl className="info-list single">
        <Row label="Servis">{daemonLabel(t.daemon)}</Row>
        <Row label="Durum">
          {t.synchronized === undefined ? '—' : t.synchronized ? <StatusBadge tone="good">senkron</StatusBadge> : <StatusBadge tone="critical">senkron değil</StatusBadge>}
        </Row>
        <Row label="Saat sunucusu">
          {t.server || t.server_address ? (
            <span className="mono">
              {t.server}
              {t.server_address && t.server_address !== t.server && <span className="muted"> ({t.server_address})</span>}
            </span>
          ) : (
            '—'
          )}
        </Row>
        <Row label="Fark">{formatOffsetMs(t.offset_ms)}</Row>
        <Row label="Gecikme · titreşim">
          {formatMs(t.delay_ms)} · {formatMs(t.jitter_ms)}
        </Row>
        <Row label="Stratum">{t.stratum ?? '—'}</Row>
        <Row label="Son senkron">{t.last_sync ? when(t.last_sync, now) : '—'}</Row>
        {issues.length > 0 && (
          <Row label="Sorunlar">
            <ul className="info-addresses">
              {issues.map((i) => (
                <li key={i}>
                  <StatusBadge tone="warning">{i}</StatusBadge>
                </li>
              ))}
            </ul>
          </Row>
        )}
      </dl>
      {t.sources && t.sources.length > 0 && (
        <table className="stack compact-table">
          <thead>
            <tr>
              <th>Kaynak</th>
              <th>Durum</th>
              <th>Ulaşılabilirlik</th>
              <th>Fark</th>
            </tr>
          </thead>
          <tbody>
            {t.sources.map((src) => {
              const st = timeSourceState(src.state)
              return (
                <tr key={src.name}>
                  <td className="primary mono">{src.name}</td>
                  <td data-label="Durum">
                    <StatusBadge tone={st.tone}>{st.label}</StatusBadge>
                  </td>
                  <td className="tnum" data-label="Ulaşılabilirlik" title="son 8 denemenin başarılı olanları">
                    {reachText(src.reach)}
                  </td>
                  <td className="tnum" data-label="Fark">
                    {formatOffsetMs(src.offset_ms)}
                  </td>
                </tr>
              )
            })}
          </tbody>
        </table>
      )}
    </Card>
  )
}

function ProcessTable({ title, rows, value }: { title: string; rows: ProcessGroup[]; value: (p: ProcessGroup) => string }) {
  if (rows.length === 0) return null
  return (
    <>
      <h3 className="chart-subtitle">{title}</h3>
      <table className="stack compact-table">
        <tbody>
          {rows.map((p) => (
            <tr key={p.name}>
              <td className="primary mono">{p.name}</td>
              <td className="tnum muted">{p.count > 1 ? `${p.count} süreç` : '1 süreç'}</td>
              <td className="tnum">{value(p)}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </>
  )
}

function ProcessesCard({ state }: { state: SystemState }) {
  const p = state.processes
  return (
    <Card title="En çok kaynak kullananlar" icon={Gauge}>
      {!p ? (
        <p className="form-hint">Bildirilmedi.</p>
      ) : (
        <>
          <p className="card-desc">
            Toplam {formatCount(p.total)} süreç
            {p.zombie > 0 && (
              <>
                {' '}
                · <StatusBadge tone="warning">{formatCount(p.zombie)} zombi</StatusBadge>
              </>
            )}
            . Aynı adlı süreçler toplanır; yalnızca süreç adı gelir, komut satırı gelmez.
          </p>
          <ProcessTable title="CPU" rows={p.top_cpu ?? []} value={(x) => formatPct(x.cpu_pct)} />
          <ProcessTable title="RAM" rows={p.top_ram ?? []} value={(x) => formatMB(x.rss_mb)} />
        </>
      )}
    </Card>
  )
}

function RAIDCard({ state }: { state: SystemState }) {
  const arrays = state.raid ?? []
  if (arrays.length === 0) return null
  return (
    <Card title="Yazılım RAID" icon={HardDrive}>
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
    </Card>
  )
}
