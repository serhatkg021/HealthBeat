import { alertReading } from './alertText'
import { useCallback, useEffect, useMemo, useState } from 'react'
import { Link, useSearchParams } from 'react-router-dom'
import {
  AlertTriangle,
  ArrowRight,
  Bell,
  CircleArrowUp,
  CheckCircle2,
  ListFilter,
  RefreshCw,
  Server,
  ServerCrash,
  ServerOff,
  Wifi,
  X,
} from 'lucide-react'
import { dashboardApi } from '../api/endpoints'
import type { DashboardOverview } from '../types/api'
import { Drawer } from '../components/Drawer'
import { EmptyState } from '../components/EmptyState'
import { PageHeader } from '../components/PageHeader'
import { StatTile } from '../components/StatTile'
import { StatusBadge } from '../components/StatusBadge'
import { useAgentPolicy } from '../components/useAgentPolicy'
import { agentKind, needsUpdate } from './agentStatus'
import { alertLevelLabel, alertLevelTone, alertMetricLabel, alertSubjectText, hostStatusLabel } from '../labels'
import { DashboardFilterPanel } from './DashboardFilterPanel'
import {
  activeChips,
  activeCount,
  applyFilters,
  emptyFilters,
  filtersHref,
  parseFilters,
  problemRows,
  pruneOrgs,
  writeFilters,
  type DashboardFilters,
} from './dashboardFilters'
import { HOSTS_PATH } from '../navigation'

const PROBLEM_LIMIT = 8
const REFRESH_MS = 60_000
const ALERT_FEED = 8

export function DashboardPage() {
  const [params, setParams] = useSearchParams()
  const agentPolicy = useAgentPolicy()
  const [data, setData] = useState<DashboardOverview | null>(null)
  const [now, setNow] = useState(() => new Date())
  const [error, setError] = useState<string | null>(null)
  const [loading, setLoading] = useState(false)
  const [panelOpen, setPanelOpen] = useState(false)

  const fetchOverview = useCallback(
    () =>
      dashboardApi.overview().then((d) => {
        setData(d)
        setNow(new Date())
        setError(null)
      }),
    [],
  )
  const describe = (err: unknown) => (err instanceof Error ? err.message : 'özet yüklenemedi')

  useEffect(() => {
    fetchOverview().catch((err) => setError(describe(err)))
    // Arka plandaki yenileme başarısız olursa eldeki veri kalır; hata yalnızca ilk yüklemede ve elle yenilemede gösterilir.
    const timer = setInterval(() => fetchOverview().catch(() => undefined), REFRESH_MS)
    return () => clearInterval(timer)
  }, [fetchOverview])

  function refreshNow() {
    setLoading(true)
    fetchOverview()
      .catch((err) => setError(describe(err)))
      .finally(() => setLoading(false))
  }

  const organizations = useMemo(() => data?.organizations ?? [], [data])
  const filters = useMemo(() => {
    const parsed = parseFilters(params)
    return data ? pruneOrgs(parsed, organizations.map((o) => o.id)) : parsed
  }, [params, data, organizations])

  function setFilters(next: DashboardFilters) {
    setParams((prev) => writeFilters(prev, next), { replace: true })
  }

  const view = useMemo(() => (data ? applyFilters(data, filters, now, agentPolicy) : null), [data, filters, now, agentPolicy])
  // Görünen sunucular arasında agent'ı güncellenmesi gerekenlerin sayısı (eski / güncelleme var / desteklenmiyor).
  const agentsToUpdate = useMemo(() => (view ? view.rows.filter((r) => needsUpdate(agentKind(r.host, agentPolicy))).length : 0), [view, agentPolicy])
  const problems = useMemo(() => (view ? problemRows(view.rows, PROBLEM_LIMIT) : []), [view])
  // Kutucuklar Sunucular sayfasını Özet'in süzgeçlerine ek olarak kendi koşuluyla açar.
  const hostsHref = (extra: Partial<DashboardFilters> = {}) => filtersHref(HOSTS_PATH, { ...filters, ...extra })
  const orgName = useMemo(() => new Map(organizations.map((o) => [o.id, o.name])), [organizations])
  const hostTitles = useMemo(() => new Map((data?.hosts ?? []).map((c) => [c.id, c.title])), [data])
  const filterCount = activeCount(filters)
  const chips = activeChips(filters, (id) => orgName.get(id) ?? id)
  const counts = view?.counts

  const refresh = (
    <button type="button" className="btn" onClick={refreshNow} disabled={loading} aria-label="Yenile" title="Verileri yenile">
      <RefreshCw size={15} strokeWidth={1.9} className={loading ? 'spin' : undefined} />
      <span className="btn-label">Yenile</span>
    </button>
  )
  const filterButton = (
    <button type="button" className="btn" onClick={() => setPanelOpen(true)} aria-haspopup="dialog">
      <ListFilter size={15} strokeWidth={1.9} />
      Filtrele
      {filterCount > 0 && <span className="filter-count">{filterCount}</span>}
    </button>
  )

  return (
    <div>
      <PageHeader
        title="Özet"
        subtitle={
          filterCount > 0 && counts
            ? `Filtrelenmiş görünüm · ${counts.total} sunucu, ${counts.alerts} alert`
            : 'Sunucuların ve açık alert’lerin anlık durumu'
        }
        actions={
          <>
            {refresh}
            {filterButton}
          </>
        }
      />
      {error && <div className="error-banner">{error}</div>}

      {chips.length > 0 && (
        <div className="chips" aria-label="Açık filtreler">
          {chips.map((c) => (
            <button key={c.key} type="button" className="chip" onClick={() => setFilters(c.without)} aria-label={`${c.label} filtresini kaldır`}>
              {c.label}
              <X size={13} strokeWidth={2.2} />
            </button>
          ))}
          <button type="button" className="chip-clear" onClick={() => setFilters(emptyFilters())}>
            Filtreleri temizle
          </button>
        </div>
      )}

      {counts && view && data && (
        <>
          <h2 className="section-title">Sunucular</h2>
          <div className="stat-grid">
            <StatTile label="Toplam sunucu" value={counts.total} icon={Server} tone="accent" to={hostsHref()} />
            <StatTile label="Çevrimiçi" value={counts.online} icon={Wifi} tone="good" to={hostsHref({ status: 'online' })} />
            <StatTile label="Çevrimdışı" value={counts.offline} icon={ServerOff} tone={counts.offline > 0 ? 'critical' : undefined} to={hostsHref({ status: 'offline' })} />
            <StatTile
              label="Agent güncellenmeli"
              value={agentsToUpdate}
              icon={CircleArrowUp}
              tone={agentsToUpdate > 0 ? 'warning' : undefined}
              to={hostsHref({ agentUpdate: true })}
            />
          </div>
          <h2 className="section-title">Açık alert’ler</h2>
          <div className="stat-grid">
            <StatTile label="Açık alert" value={counts.alerts} icon={Bell} tone="accent" to={hostsHref({ withAlerts: true })} />
            <StatTile
              label="Kritik"
              value={counts.critical}
              icon={ServerCrash}
              tone={counts.critical > 0 ? 'critical' : undefined}
              to={hostsHref({ levels: ['critical'] })}
            />
            <StatTile label="Uyarı" value={counts.warning} icon={AlertTriangle} tone={counts.warning > 0 ? 'warning' : undefined} to={hostsHref({ levels: ['warning'] })} />
          </div>

          <div className="card table-card">
            <div className="card-head">
              <h2 className="card-title">
                <Server size={16} strokeWidth={1.75} />
                Sorunlu sunucular
                <span className="tab-badge">{problems.length}</span>
              </h2>
              <Link to={hostsHref()} className="card-link">
                Tüm sunucular
                <ArrowRight size={14} strokeWidth={2} />
              </Link>
            </div>
            {problems.length === 0 ? (
              <EmptyState icon={CheckCircle2}>{data.hosts.length === 0 ? 'Henüz sunucu yok.' : 'Çevrimdışı ya da açık alert’i olan sunucu yok.'}</EmptyState>
            ) : (
              <ul className="feed">
                {problems.map(({ host: c, critical, warning }) => (
                  <li key={c.id}>
                    <StatusBadge tone={c.status === 'online' ? 'good' : 'critical'}>{hostStatusLabel(c.status)}</StatusBadge>
                    <div className="feed-main">
                      <Link to={`/hosts/${c.id}`}>{c.title}</Link>
                      <span className="muted">{orgName.get(c.organization_id) ?? c.ip}</span>
                    </div>
                    <span className="row row-tight">
                      {critical > 0 && <StatusBadge tone="critical">{critical} kritik</StatusBadge>}
                      {warning > 0 && <StatusBadge tone="warning">{warning} uyarı</StatusBadge>}
                    </span>
                  </li>
                ))}
              </ul>
            )}
          </div>

          <div className="card table-card">
            <div className="card-head">
              <h2 className="card-title">
                <Bell size={16} strokeWidth={1.75} />
                Açık alert’ler
                <span className="tab-badge">{view.alerts.length}</span>
              </h2>
              <Link to="/alerts" className="card-link">
                Tümünü gör
                <ArrowRight size={14} strokeWidth={2} />
              </Link>
            </div>
            {view.alerts.length === 0 ? (
              <EmptyState icon={CheckCircle2}>{filterCount > 0 ? 'Bu filtrelerle eşleşen açık alert yok.' : 'Açık alert yok — her şey yolunda.'}</EmptyState>
            ) : (
              <ul className="feed">
                {view.alerts.slice(0, ALERT_FEED).map((a) => (
                  <li key={a.id}>
                    <StatusBadge tone={alertLevelTone(a.level)}>{alertLevelLabel(a.level)}</StatusBadge>
                    <div className="feed-main">
                      <Link to={`/hosts/${a.host_id}`}>{hostTitles.get(a.host_id) ?? a.host_id}</Link>
                      <span className="muted">
                        {alertMetricLabel(a.alert_type)}
                        {a.subject && <span className="mono"> · {alertSubjectText(a.alert_type, a.subject)}</span>}
                        {alertReading(a) && <span className="tnum"> · {alertReading(a)}</span>}
                      </span>
                    </div>
                    <time className="muted feed-time" dateTime={a.created_at}>
                      {new Date(a.created_at).toLocaleString()}
                    </time>
                  </li>
                ))}
              </ul>
            )}
          </div>
        </>
      )}

      <Drawer
        open={panelOpen}
        title="Filtreler"
        onClose={() => setPanelOpen(false)}
        footer={
          <>
            <button type="button" className="btn btn-ghost" onClick={() => setFilters(emptyFilters())} disabled={filterCount === 0}>
              Temizle
            </button>
            <button type="button" className="btn btn-primary" onClick={() => setPanelOpen(false)}>
              {counts ? `Sonuçları göster (${counts.total} sunucu · ${counts.alerts} alert)` : 'Kapat'}
            </button>
          </>
        }
      >
        <DashboardFilterPanel filters={filters} onChange={setFilters} organizations={organizations} />
      </Drawer>
    </div>
  )
}
