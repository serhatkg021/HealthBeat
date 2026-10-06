import { useCallback, useEffect, useMemo, useState } from 'react'
import { Link, useSearchParams } from 'react-router-dom'
import { Building2, ListFilter, Plus, RefreshCw, Server, X } from 'lucide-react'
import { dashboardApi } from '../api/endpoints'
import { useAuth } from '../auth/AuthContext'
import { AgentBadge } from '../components/AgentBadge'
import { Drawer } from '../components/Drawer'
import { EmptyState } from '../components/EmptyState'
import { PageHeader } from '../components/PageHeader'
import { Pagination } from '../components/Pagination'
import { OrgPicker } from '../components/ScopePickers'
import { SearchInput } from '../components/SearchInput'
import { SecretNotice } from '../components/SecretNotice'
import { StatusBadge } from '../components/StatusBadge'
import { TabPanel, Tabs, type TabItem } from '../components/Tabs'
import { useAgentPolicy } from '../components/useAgentPolicy'
import { useScopeData } from '../components/useScopeData'
import { useTab } from '../components/useTab'
import { ADD_HOST_ORG_PARAM } from '../navigation'
import { hostStatusLabel } from '../labels'
import type { DashboardOverview, Host } from '../types/api'
import { AddHostWizard } from './AddHostWizard'
import { DashboardFilterPanel } from './DashboardFilterPanel'
import { activeChips, activeCount, applyFilters, emptyFilters, parseFilters, pruneOrgs, writeFilters, type DashboardFilters } from './dashboardFilters'

const PAGE = 20
const REFRESH_MS = 60_000

// Sunucular: kullanıcının görebildiği tüm sunucuların süzülebilir düz listesi (operatöre yalnızca atanmış olanlar; liste
// server'dan böyle gelir). Süzgeçler adreste tutulur ve Özet'le aynı biçimdedir; Özet'teki kutucuklar buraya süzgeçle
// bağlanır. "Sunucu ekle" sekmesi organizasyon seçilerek sihirbazı açar (`?sekme=ekle&hedef=`).
export function HostsPage() {
  const { can } = useAuth()
  const canAdd = can('host.create')
  const tabIds = canAdd ? ['liste', 'ekle'] : ['liste']
  const [tab, setTab] = useTab(tabIds, 'liste')
  const [, setParams] = useSearchParams()
  const [revealed, setRevealed] = useState<Host | null>(null)
  const [reloadKey, setReloadKey] = useState(0)

  // Sihirbazdan listeye dönüş: sekme ve sihirbazın organizasyonu tek güncellemede silinir (iki ayrı güncelleme
  // birbirini ezer).
  function backToList() {
    setParams(
      (prev) => {
        const next = new URLSearchParams(prev)
        next.delete('sekme')
        next.delete(ADD_HOST_ORG_PARAM)
        return next
      },
      { replace: true },
    )
  }

  const tabs: TabItem[] = [{ id: 'liste', label: 'Sunucular', icon: Server }, ...(canAdd ? [{ id: 'ekle', label: 'Sunucu ekle', icon: Plus }] : [])]

  return (
    <div>
      <PageHeader title="Sunucular" subtitle="Görebildiğiniz tüm sunucular; organizasyona, duruma ve alert’lere göre süzülebilir" />
      <Tabs items={tabs} active={tab} onChange={setTab} label="Sunucu bölümleri" />
      <TabPanel id="liste" active={tab}>
        {revealed && (
          <SecretNotice
            title={`${revealed.title} oluşturuldu — bu kimlik bilgisi bir daha gösterilmeyecek; host config dosyasına şimdi kopyalayın`}
            text={revealed.api_token ? `host_id: ${revealed.id}\napi_token: ${revealed.api_token}` : `host_id: ${revealed.id}\npull_secret: ${revealed.pull_secret}`}
            onClose={() => setRevealed(null)}
          />
        )}
        <HostList key={reloadKey} />
      </TabPanel>
      {canAdd && (
        <TabPanel id="ekle" active={tab} keepMounted>
          <AddHost
            onCancel={backToList}
            onCreated={(created) => {
              setRevealed(created)
              setReloadKey((k) => k + 1)
              backToList()
            }}
          />
        </TabPanel>
      )}
    </div>
  )
}

function HostList() {
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

  const fetchOverview = useCallback(
    () =>
      dashboardApi.overview().then((d) => {
        setData(d)
        setNow(new Date())
        setError(null)
      }),
    [],
  )
  const describe = (err: unknown) => (err instanceof Error ? err.message : 'sunucular yüklenemedi')

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
  const searched = useMemo(() => {
    if (!view) return []
    const q = search.trim().toLocaleLowerCase('tr')
    if (!q) return view.rows
    return view.rows.filter(({ host: c }) => c.title.toLocaleLowerCase('tr').includes(q) || c.ip.toLowerCase().includes(q))
  }, [view, search])
  const pageRows = useMemo(() => searched.slice((page - 1) * pageSize, page * pageSize), [searched, page, pageSize])
  const orgName = useMemo(() => new Map(organizations.map((o) => [o.id, o.name])), [organizations])
  const filterCount = activeCount(filters)
  const chips = activeChips(filters, (id) => orgName.get(id) ?? id)
  const counts = view?.counts

  return (
    <div>
      {error && <div className="error-banner">{error}</div>}

      <div className="toolbar">
        <SearchInput value={search} onChange={setSearchAndResetPage} placeholder="Sunucu adı ya da IP ara…" />
        <span className="row">
          <button type="button" className="btn" onClick={refreshNow} disabled={loading} aria-label="Yenile" title="Verileri yenile">
            <RefreshCw size={15} strokeWidth={1.9} className={loading ? 'spin' : undefined} />
            <span className="btn-label">Yenile</span>
          </button>
          <button type="button" className="btn" onClick={() => setPanelOpen(true)} aria-haspopup="dialog">
            <ListFilter size={15} strokeWidth={1.9} />
            Filtrele
            {filterCount > 0 && <span className="filter-count">{filterCount}</span>}
          </button>
        </span>
      </div>

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

      {data && view && (
        <div className="card table-card">
          <Pagination
            page={page}
            pageSize={pageSize}
            total={searched.length}
            onPageChange={setPage}
            onPageSizeChange={(n) => {
              setPageSizeState(n)
              setPage(1)
            }}
          />
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
              {counts ? `Sonuçları göster (${counts.total} sunucu)` : 'Kapat'}
            </button>
          </>
        }
      >
        <DashboardFilterPanel filters={filters} onChange={setFilters} organizations={organizations} />
      </Drawer>
    </div>
  )
}

// Sunucu ekleme: önce organizasyon (adresteki `org` ile gelinmişse seçili), sonra mevcut sihirbaz.
function AddHost({ onCreated, onCancel }: { onCreated: (host: Host) => void; onCancel: () => void }) {
  const { can } = useAuth()
  const [params, setParams] = useSearchParams()
  const { orgs } = useScopeData(can('organization.view'))
  const orgId = params.get(ADD_HOST_ORG_PARAM) ?? undefined
  const [wizardKey, setWizardKey] = useState(0)

  function selectOrg(id: string) {
    setParams(
      (prev) => {
        const next = new URLSearchParams(prev)
        next.set(ADD_HOST_ORG_PARAM, id)
        return next
      },
      { replace: true },
    )
    setWizardKey((k) => k + 1)
  }

  return (
    <div>
      <div className="toolbar" style={{ justifyContent: 'flex-start' }}>
        <OrgPicker idPrefix="add-host" orgs={orgs.filter((o) => o.access !== 'context')} value={orgId} onChange={selectOrg} />
      </div>
      {orgId ? (
        <AddHostWizard
          key={`${orgId}-${wizardKey}`}
          organizationId={orgId}
          onCancel={() => {
            setWizardKey((k) => k + 1)
            onCancel()
          }}
          onCreated={(created) => {
            setWizardKey((k) => k + 1)
            onCreated(created)
          }}
        />
      ) : (
        <div className="card">
          <EmptyState icon={Building2}>Sunucunun ekleneceği organizasyonu seçin.</EmptyState>
        </div>
      )}
    </div>
  )
}
