import { useEffect, useState } from 'react'
import { Link, Navigate, useNavigate, useParams, useSearchParams } from 'react-router-dom'
import { hostsApi, organizationsApi } from '../api/endpoints'
import { usePagedQuery } from '../api/usePagedQuery'
import type { Organization } from '../types/api'
import { StatusBadge } from '../components/StatusBadge'
import { TabPanel, Tabs, type TabItem } from '../components/Tabs'
import { useTab } from '../components/useTab'
import { hostStatusLabel } from '../labels'
import { PageHeader } from '../components/PageHeader'
import { EmptyState } from '../components/EmptyState'
import { SearchInput } from '../components/SearchInput'
import { Pagination } from '../components/Pagination'
import { AgentBadge } from '../components/AgentBadge'
import { useAgentPolicy } from '../components/useAgentPolicy'
import { osLabel } from './inventory'
import { Contact, Plus, Server, Settings } from 'lucide-react'
import { useAuth } from '../auth/AuthContext'
import { OrganizationContacts } from './OrganizationContacts'
import { OrganizationSettingsModal } from './OrganizationSettings'
import { useDocumentTitle } from '../components/useDocumentTitle'
import { addHostPath, alertRulesPath, notificationsPath } from '../navigation'

const PAGE_SIZE = 20

export function OrganizationHostsPage() {
  const { id } = useParams<{ id: string }>()
  const agentPolicy = useAgentPolicy()
  const { can } = useAuth()
  const canAddHost = can('host.create')
  const canSeeSettings = can('organization.update') || can('organization.delete')
  const [org, setOrg] = useState<Organization | null>(null)
  const [allOrgs, setAllOrgs] = useState<Organization[]>([]) // üst zincir ve üst şirket seçimi için
  useDocumentTitle(org?.name)

  const {
    items: hosts,
    total,
    page,
    setPage,
    pageSize,
    setPageSize,
    q,
    setSearch,
    error,
  } = usePagedQuery(
    (p) => {
      if (!id) return Promise.resolve({ items: [], total: 0 })
      return hostsApi.searchByOrganization(id, p)
    },
    PAGE_SIZE,
    [id],
  )

  function loadOrg() {
    if (!id) return
    organizationsApi.get(id).then(setOrg).catch(() => undefined)
    organizationsApi.list().then(setAllOrgs).catch(() => undefined)
  }
  useEffect(loadOrg, [id])

  const tabIds = ['sunucular', 'kisiler']
  const [params] = useSearchParams()
  const navigate = useNavigate()
  // Ayarlar pencere olarak açılır; eski "Ayarlar" sekmesinin adresi (?sekme=ayarlar) pencereyi açık getirir.
  const [settingsOpen, setSettingsOpen] = useState(() => canSeeSettings && params.get('sekme') === 'ayarlar')
  const [tab, setTab] = useTab(tabIds, 'sunucular')
  const tabItems: TabItem[] = [
    { id: 'sunucular', label: 'Sunucular', badge: total, icon: Server },
    { id: 'kisiler', label: 'İletişim kişileri', icon: Contact },
  ]
  // Üst zincir: bu organizasyonun üst şirketleri (adları bilgi olarak görünür).
  const ancestors: Organization[] = []
  for (let cur = org?.parent_organization_id; cur && ancestors.length < 20; ) {
    const parent = allOrgs.find((o) => o.id === cur)
    if (!parent) break
    ancestors.unshift(parent)
    cur = parent.parent_organization_id
  }

  // Eski "Eşikler" sekmesi artık Alert kuralları sayfasında bu organizasyonun kapsamıdır.
  if (id && params.get('sekme') === 'esikler') return <Navigate to={alertRulesPath({ kind: 'org', id })} replace />
  // Eski "Bildirim kuralları" sekmesi artık Bildirim sayfasında bu organizasyonun kapsamıdır.
  if (id && params.get('sekme') === 'bildirimler') return <Navigate to={notificationsPath('kurallar', { kind: 'org', id })} replace />
  // Eski "Sunucu ekle" sekmesi artık Sunucular sayfasında, bu organizasyon seçili.
  if (id && params.get('sekme') === 'ekle') return <Navigate to={addHostPath(id)} replace />

  return (
    <div>
      <PageHeader
        back={{ to: '/organizations', label: 'Organizasyonlar' }}
        title={org?.name ?? 'Organizasyon'}
        subtitle={
          org && (ancestors.length > 0 || org.address) ? (
            <>
              {ancestors.length > 0 && <span>{ancestors.map((a) => a.name).join(' › ')} › {org.name}</span>}
              {ancestors.length > 0 && org.address && ' · '}
              {org.address && <span>{org.address}</span>}
            </>
          ) : undefined
        }
        actions={
          id && (
            <>
              {canAddHost && (
                <Link className="btn btn-sm" to={addHostPath(id)}>
                  <Plus size={15} strokeWidth={1.9} />
                  Sunucu ekle
                </Link>
              )}
              {can('threshold.view') && (
                <Link className="btn btn-sm" to={alertRulesPath({ kind: 'org', id })}>
                  Alert kuralları →
                </Link>
              )}
              {can('notification.view') && (
                <Link className="btn btn-sm" to={notificationsPath('kurallar', { kind: 'org', id })}>
                  Bildirim kuralları →
                </Link>
              )}
              {canSeeSettings && org && (
                <button type="button" className="btn btn-sm" onClick={() => setSettingsOpen(true)}>
                  <Settings size={15} strokeWidth={1.75} />
                  Ayarlar
                </button>
              )}
            </>
          )
        }
      />
      <OrganizationSettingsModal
        organization={settingsOpen ? org : null}
        orgs={allOrgs}
        onClose={() => setSettingsOpen(false)}
        onSaved={loadOrg}
        onDeleted={() => navigate('/organizations')}
      />
      {error && <div className="error-banner">{error}</div>}

      <Tabs items={tabItems} active={tab} onChange={setTab} label="Organizasyon sunucu bölümleri" />

      <TabPanel id="sunucular" active={tab}>
        <div className="toolbar">
          <SearchInput value={q} onChange={setSearch} placeholder="Sunucu adı ya da IP ara…" />
        </div>
        <div className="card table-card">
          <Pagination page={page} pageSize={pageSize} total={total} onPageChange={setPage} onPageSizeChange={setPageSize} />
          <table className="stack">
            <thead>
              <tr>
                <th>Sunucu</th>
                <th>IP</th>
                <th>Mod</th>
                <th>İşletim sistemi</th>
                <th>Durum</th>
                <th>Agent</th>
                <th>Son görülme</th>
              </tr>
            </thead>
            <tbody>
              {hosts.map((c) => (
                <tr key={c.id} className="clickable">
                  <td className="primary">
                    <Link to={`/hosts/${c.id}`}>{c.title}</Link>
                  </td>
                  <td className="muted mono" data-label="IP">{c.ip}</td>
                  <td className="muted" data-label="Mod">{c.mode}</td>
                  <td className="muted" data-label="İşletim sistemi">{osLabel(c.host_info)}</td>
                  <td data-label="Durum">
                    <StatusBadge tone={c.status === 'online' ? 'good' : 'critical'}>{hostStatusLabel(c.status)}</StatusBadge>
                  </td>
                  <td data-label="Agent">
                    <AgentBadge host={c} policy={agentPolicy} />
                  </td>
                  <td className="muted" data-label="Son görülme">{c.last_seen ? new Date(c.last_seen).toLocaleString() : '—'}</td>
                </tr>
              ))}
              {hosts.length === 0 && (
                <tr>
                  <td colSpan={7} className="empty-cell">
                    <EmptyState icon={Server}>Bu organizasyonda henüz sunucu yok.</EmptyState>
                  </td>
                </tr>
              )}
            </tbody>
          </table>
        </div>
      </TabPanel>

      {id && (
        <TabPanel id="kisiler" active={tab}>
          <OrganizationContacts organizationId={id} canEdit={can('contact.edit')} />
        </TabPanel>
      )}

    </div>
  )
}
