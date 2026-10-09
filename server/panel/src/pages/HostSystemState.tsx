import type { ReactNode } from 'react'
import { Clock, Gauge, HardDrive, Layers, MemoryStick, PackageOpen, Thermometer, type LucideIcon } from 'lucide-react'
import type { Host, HostThresholdsResponse, SystemState } from '../types/api'
import { EmptyState } from '../components/EmptyState'
import { StatusBadge } from '../components/StatusBadge'
import { useNow } from '../components/useNow'
import { agoText } from './cache'
import { healthEmptyText } from './inventory'
import { CapacityList, ProcessList, RaidTable, SensorsTable } from './PerfPanels'
import { daemonLabel, reachText, timeSourceState, timeSyncIssues } from './systemState'
import { formatCount, formatMs, formatOffsetMs } from './units'

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

function Temperatures({ state, thresholds }: { state: SystemState; thresholds: HostThresholdsResponse | null }) {
  return (
    <Card title="Sıcaklık" icon={Thermometer}>
      <SensorsTable state={state} thresholds={thresholds} />
    </Card>
  )
}

function CapacityCard({ state }: { state: SystemState }) {
  return (
    <Card title="Kapasite sınırları" icon={Layers}>
      <CapacityList state={state} />
    </Card>
  )
}

function ProcessesCard({ state }: { state: SystemState }) {
  return (
    <Card title="En çok kaynak kullananlar" icon={Gauge}>
      <h3 className="chart-subtitle">CPU</h3>
      <ProcessList state={state} kind="cpu" />
      <h3 className="chart-subtitle">RAM</h3>
      <ProcessList state={state} kind="ram" />
    </Card>
  )
}

function RAIDCard({ state }: { state: SystemState }) {
  if ((state.raid ?? []).length === 0) return null
  return (
    <Card title="Yazılım RAID" icon={HardDrive}>
      <RaidTable state={state} />
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
