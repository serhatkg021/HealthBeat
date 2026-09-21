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
import { Pagination } from '../components/Pagination'
import { SearchInput } from '../components/SearchInput'
import { StatTile } from '../components/StatTile'
import { StatusBadge } from '../components/StatusBadge'
import { AgentBadge } from '../components/AgentBadge'
import { useAgentPolicy } from '../components/useAgentPolicy'
import { agentKind, needsUpdate } from './agentStatus'
import { alertLevelLabel, alertLevelTone, alertMetricLabel, hostStatusLabel } from '../labels'
import { DashboardFilterPanel } from './DashboardFilterPanel'
import {
  activeChips,
  activeCount,
  applyFilters,
  emptyFilters,
  parseFilters,
  pruneOrgs,
  writeFilters,
  type DashboardFilters,
} from './dashboardFilters'

const PAGE = 10
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
  const [search, setSearch] = useState('')
  const [page, setPage] = useState(1)
  const [pageSize, setPageSizeState] = useState(PAGE)

  function setPageSize(n: number) {
    setPageSizeState(n)
    setPage(1)
  }

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
    setPage(1)
    setParams((prev) => writeFilters(prev, next), { replace: true })
  }

  function setSearchAndResetPage(v: string) {
    setSearch(v)
    setPage(1)
  }

  const view = useMemo(() => (data ? applyFilters(data, filters, now, agentPolicy) : null), [data, filters, now, agentPolicy])
  // Görünen sunucular arasında agent'ı güncellenmesi gerekenlerin sayısı (eski / güncelleme var / desteklenmiyor).
  const agentsToUpdate = useMemo(() => (view ? view.rows.filter((r) => needsUpdate(agentKind(r.host, agentPolicy))).length : 0), [view, agentPolicy])
  const searched = useMemo(() => {
    if (!view) return []
    const q = search.trim().toLowerCase()
    if (!q) return view.rows
    return view.rows.filter(({ host: c }) => c.title.toLowerCase().includes(q) || c.ip.toLowerCase().includes(q))
  }, [view, search])
  const pageRows = useMemo(() => searched.slice((page - 1) * pageSize, page * pageSize), [searched, page, pageSize])
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
            <StatTile label="Toplam sunucu" value={counts.total} icon={Server} tone="accent" />
            <StatTile label="Çevrimiçi" value={counts.online} icon={Wifi} tone="good" />
            <StatTile label="Çevrimdışı" value={counts.offline} icon={ServerOff} tone={counts.offline > 0 ? 'critical' : undefined} />
            <StatTile label="Agent güncellenmeli" value={agentsToUpdate} icon={CircleArrowUp} tone={agentsToUpdate > 0 ? 'warning' : undefined} />
          </div>
          <h2 className="section-title">Açık alert’ler</h2>
          <div className="stat-grid">
            <StatTile label="Açık alert" value={counts.alerts} icon={Bell} tone="accent" />
            <StatTile label="Kritik" value={counts.critical} icon={ServerCrash} tone={counts.critical > 0 ? 'critical' : undefined} />
            <StatTile label="Uyarı" value={counts.warning} icon={AlertTriangle} tone={counts.warning > 0 ? 'warning' : undefined} />
          </div>

          <div className="card table-card">
            <div className="card-head">
              <h2 className="card-title">
                <Server size={16} strokeWidth={1.75} />
                Sunucular
                <span className="tab-badge">{searched.length}</span>
              </h2>
              <SearchInput value={search} onChange={setSearchAndResetPage} placeholder="Sunucu adı ya da IP ara…" />
            </div>
            <Pagination page={page} pageSize={pageSize} total={searched.length} onPageChange={setPage} onPageSizeChange={setPageSize} />
            <table className="stack">
              <thead>
                <tr>
                  <th>Sunucu</th>
                  {organizations.length > 0 && <th>Organizasyon</th>}
                  <th>IP</th>
                  <th>Mod</th>
                  <th>Durum</th>
                  <th>Agent</th>
                  <th>Alert’ler</th>
                  <th>Son görülme</th>
                </tr>
              </thead>
              <tbody>
                {pageRows.map(({ host: c, critical, warning }) => (
                  <tr key={c.id}>
                    <td className="primary">
                      <Link to={`/hosts/${c.id}`}>{c.title}</Link>
                    </td>
                    {organizations.length > 0 && (
                      <td className="muted" data-label="Organizasyon">
                        {orgName.get(c.organization_id) ?? '—'}
                      </td>
                    )}
                    <td className="muted mono" data-label="IP">
                      {c.ip}
                    </td>
                    <td className="muted" data-label="Mod">
                      {c.mode}
                    </td>
                    <td data-label="Durum">
                      <StatusBadge tone={c.status === 'online' ? 'good' : 'critical'}>{hostStatusLabel(c.status)}</StatusBadge>
                    </td>
                    <td data-label="Agent">
                      <AgentBadge host={c} policy={agentPolicy} />
                    </td>
                    <td data-label="Alert’ler">
                      {critical + warning === 0 ? (
                        <span className="muted">—</span>
                      ) : (
                        <span className="row row-tight">
                          {critical > 0 && <StatusBadge tone="critical">{critical} kritik</StatusBadge>}
                          {warning > 0 && <StatusBadge tone="warning">{warning} uyarı</StatusBadge>}
                        </span>
                      )}
                    </td>
                    <td className="muted" data-label="Son görülme">
                      {c.last_seen ? new Date(c.last_seen).toLocaleString() : '—'}
                    </td>
                  </tr>
                ))}
                {searched.length === 0 && (
                  <tr>
                    <td colSpan={8} className="empty-cell">
                      <EmptyState icon={Server}>
                        {data.hosts.length === 0 ? 'Henüz sunucu yok.' : 'Filtrelerle eşleşen sunucu yok.'}
                        {data.hosts.length > 0 && (filterCount > 0 || search) && (
                          <div style={{ marginTop: 10 }}>
                            <button
                              type="button"
                              className="btn btn-sm"
                              onClick={() => {
                                setSearchAndResetPage('')
                                setFilters(emptyFilters())
                              }}
                            >
                              Filtreleri temizle
                            </button>
                          </div>
                        )}
                      </EmptyState>
                    </td>
                  </tr>
                )}
              </tbody>
            </table>
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
                        {a.subject && <span className="mono"> · {a.subject}</span>}
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
