import { useEffect, useMemo, useState, type FormEvent } from 'react'
import { Link } from 'react-router-dom'
import { organizationsApi } from '../api/endpoints'
import type { Organization } from '../types/api'
import { buildTree, parentChoices, type TreeRow } from './orgTree'
import { StatusBadge } from '../components/StatusBadge'
import { useAuth } from '../auth/AuthContext'
import { TabPanel, Tabs, type TabItem } from '../components/Tabs'
import { useTab } from '../components/useTab'
import { PageHeader } from '../components/PageHeader'
import { EmptyState } from '../components/EmptyState'
import { SearchInput } from '../components/SearchInput'
import { Building2, LayoutGrid, List as ListIcon, Plus } from 'lucide-react'

type ViewMode = 'card' | 'list'
// Görünüm tercihi yalnızca bu tarayıcıda hatırlanır (kullanıcı/organizasyon verisi değil) —
// localStorage okuma/yazma her zaman try/catch'li: gizli pencere ya da engellenmiş depolamada
// sayfa yine de doğru render olmalı.
const VIEW_KEY = 'healthbeat_orgs_view'

function loadView(): ViewMode {
  try {
    return localStorage.getItem(VIEW_KEY) === 'list' ? 'list' : 'card'
  } catch {
    return 'card'
  }
}

export function OrganizationsPage() {
  const { user } = useAuth()
  const [orgs, setOrgs] = useState<Organization[]>([])
  const [error, setError] = useState<string | null>(null)
  const [newName, setNewName] = useState('')
  const [newParent, setNewParent] = useState('')
  const [newAddress, setNewAddress] = useState('')
  const [creating, setCreating] = useState(false)
  const [search, setSearch] = useState('')
  const [view, setView] = useState<ViewMode>(loadView)

  function changeView(v: ViewMode) {
    setView(v)
    try {
      localStorage.setItem(VIEW_KEY, v)
    } catch {
      // Depolama kullanılamıyor: tercih yalnızca bu oturumda uygulanır.
    }
  }

  const canCreate = user?.role === 'super_admin'
  const [tab, setTab] = useTab(canCreate ? ['liste', 'yeni'] : ['liste'], 'liste')
  const tabItems: TabItem[] = [
    { id: 'liste', label: 'Organizasyonlar', badge: orgs.length, icon: Building2 },
    ...(canCreate ? [{ id: 'yeni', label: 'Yeni organizasyon', icon: Plus }] : []),
  ]

  function reload() {
    organizationsApi
      .list()
      .then(setOrgs)
      .catch((err) => setError(err instanceof Error ? err.message : 'organizasyonlar yüklenemedi'))
  }

  useEffect(reload, [])

  // Ağaç sırasında; arama yalnızca eşleşenleri bırakır (girinti korunur, yol bilgisi sırayla görünür).
  const filtered = useMemo(() => {
    const q = search.trim().toLowerCase()
    const rows = buildTree(orgs)
    if (!q) return rows
    return rows.filter((r) => r.org.name.toLowerCase().includes(q))
  }, [orgs, search])

  async function handleCreate(e: FormEvent) {
    e.preventDefault()
    setCreating(true)
    setError(null)
    try {
      await organizationsApi.create({
        name: newName,
        ...(newParent ? { parent_organization_id: newParent } : {}),
        ...(newAddress.trim() ? { address: newAddress.trim() } : {}),
      })
      setNewName('')
      setNewParent('')
      setNewAddress('')
      reload()
      setTab('liste')
    } catch (err) {
      setError(err instanceof Error ? err.message : 'organizasyon oluşturulamadı')
    } finally {
      setCreating(false)
    }
  }

  return (
    <div>
      <PageHeader title="Organizasyonlar" subtitle="Sunucuları ve kullanıcıları gruplayan birimler; alt organizasyonlar üst şirketin altında toplanır" />
      {error && <div className="error-banner">{error}</div>}

      <Tabs items={tabItems} active={tab} onChange={setTab} label="Organizasyon bölümleri" />

      <TabPanel id="liste" active={tab}>
        <div className="toolbar">
          <SearchInput value={search} onChange={setSearch} placeholder="Organizasyon ara…" />
          <div className="segmented" role="group" aria-label="Görünüm">
            <button type="button" aria-pressed={view === 'card'} aria-label="Kart görünümü" title="Kart görünümü" onClick={() => changeView('card')}>
              <LayoutGrid size={15} strokeWidth={1.9} />
            </button>
            <button type="button" aria-pressed={view === 'list'} aria-label="Liste görünümü" title="Liste görünümü" onClick={() => changeView('list')}>
              <ListIcon size={15} strokeWidth={1.9} />
            </button>
          </div>
        </div>

        {filtered.length === 0 ? (
          <div className="card">
            <EmptyState icon={Building2}>{orgs.length === 0 ? 'Henüz organizasyon yok.' : 'Aramayla eşleşen organizasyon yok.'}</EmptyState>
          </div>
        ) : view === 'card' ? (
          <div className="entity-grid">
            {filtered.map((row) => {
              const { org } = row
              const body = (
                <>
                  <span className="entity-card-icon">
                    <Building2 size={18} strokeWidth={1.9} />
                  </span>
                  <span className="entity-card-name">{org.name}</span>
                  {row.path.length > 0 && <span className="entity-card-meta">{row.path.join(' › ')}</span>}
                  {org.access === 'context' ? (
                    <span className="entity-card-meta">Yalnızca üst zincir bilgisi</span>
                  ) : (
                    <span className="entity-card-meta">{org.address ?? `Oluşturulma: ${new Date(org.created_at).toLocaleDateString()}`}</span>
                  )}
                </>
              )
              return org.access === 'context' ? (
                <div key={org.id} className="entity-card entity-card-context" title="Bu organizasyona atanmadınız; yalnızca alt organizasyonunuzun bağlı olduğu üst şirket olarak görünür">
                  {body}
                </div>
              ) : (
                <Link key={org.id} to={`/organizations/${org.id}`} className="entity-card">
                  {body}
                </Link>
              )
            })}
          </div>
        ) : (
          <div className="card table-card">
            <table className="stack">
              <thead>
                <tr>
                  <th>Ad</th>
                  <th>Adres</th>
                  <th>Oluşturulma</th>
                </tr>
              </thead>
              <tbody>
                {filtered.map((row: TreeRow) => (
                  <tr key={row.org.id} className={row.org.access === 'context' ? undefined : 'clickable'}>
                    <td className="primary">
                      <span className="org-indent" style={{ paddingLeft: row.depth * 20 }}>
                        {row.org.access === 'context' ? (
                          <>
                            {row.org.name} <StatusBadge tone="neutral" title="Yalnızca bilgi: bu organizasyona atanmadınız">üst zincir</StatusBadge>
                          </>
                        ) : (
                          <Link to={`/organizations/${row.org.id}`}>{row.org.name}</Link>
                        )}
                      </span>
                    </td>
                    <td className="muted" data-label="Adres">{row.org.address ?? '—'}</td>
                    <td className="muted" data-label="Oluşturulma">{new Date(row.org.created_at).toLocaleString()}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </TabPanel>

      {canCreate && (
        <TabPanel id="yeni" active={tab}>
          <form className="card form-card" onSubmit={handleCreate}>
            <div className="form-row">
              <label htmlFor="org-name">Organizasyon adı</label>
              <input id="org-name" value={newName} onChange={(e) => setNewName(e.target.value)} required />
            </div>
            <div className="form-row">
              <label htmlFor="org-parent">Üst şirket</label>
              <select id="org-parent" value={newParent} onChange={(e) => setNewParent(e.target.value)}>
                <option value="">Yok (kök organizasyon)</option>
                {parentChoices(orgs).map((o) => (
                  <option key={o.id} value={o.id}>
                    {o.name}
                  </option>
                ))}
              </select>
              <p className="form-hint">Alt organizasyonun yöneticileri yalnızca kendi dallarını görür; üst şirketin eşikleri ve bildirim kuralları alt dala miras kalır.</p>
            </div>
            <div className="form-row">
              <label htmlFor="org-address">Adres (isteğe bağlı)</label>
              <textarea id="org-address" rows={2} value={newAddress} onChange={(e) => setNewAddress(e.target.value)} maxLength={1000} />
            </div>
            <button className="btn btn-primary" type="submit" disabled={creating}>
              Oluştur
            </button>
          </form>
        </TabPanel>
      )}
    </div>
  )
}
