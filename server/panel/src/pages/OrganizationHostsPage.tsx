import { useEffect, useState } from 'react'
import { Link, useParams } from 'react-router-dom'
import { hostsApi, organizationsApi } from '../api/endpoints'
import { usePagedQuery } from '../api/usePagedQuery'
import type { Host, Organization } from '../types/api'
import { StatusBadge } from '../components/StatusBadge'
import { TabPanel, Tabs, type TabItem } from '../components/Tabs'
import { useTab } from '../components/useTab'
import { AddHostWizard } from './AddHostWizard'
import { hostStatusLabel } from '../labels'
import { PageHeader } from '../components/PageHeader'
import { EmptyState } from '../components/EmptyState'
import { SecretNotice } from '../components/SecretNotice'
import { SearchInput } from '../components/SearchInput'
import { Pagination } from '../components/Pagination'
import { AgentBadge } from '../components/AgentBadge'
import { useAgentPolicy } from '../components/useAgentPolicy'
import { osLabel } from './inventory'
import { BellRing, Contact, Plus, Server, Settings, SlidersHorizontal } from 'lucide-react'
import { useAuth } from '../auth/AuthContext'
import { OrganizationContacts } from './OrganizationContacts'
import { NotificationRules } from './NotificationRules'
import { OrganizationThresholds } from './OrganizationThresholds'
import { OrganizationSettings } from './OrganizationSettings'
import { useDocumentTitle } from '../components/useDocumentTitle'

const PAGE_SIZE = 20

export function OrganizationHostsPage() {
  const { id } = useParams<{ id: string }>()
  const agentPolicy = useAgentPolicy()
  const { user } = useAuth()
  const canEdit = user?.role === 'super_admin' || user?.role === 'org_admin' // server ile aynı kural (contact.edit, notification.edit)
  const isSuper = user?.role === 'super_admin'
  const [org, setOrg] = useState<Organization | null>(null)
  const [allOrgs, setAllOrgs] = useState<Organization[]>([]) // üst zincir ve üst şirket seçimi için
  useDocumentTitle(org?.name)
  const [wizardKey, setWizardKey] = useState(0) // sihirbazı sıfırlar (vazgeç / eklendi)
  const [revealedHost, setRevealedHost] = useState<Host | null>(null)

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
    reload,
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

  const tabIds = ['sunucular', 'ekle', 'kisiler', 'bildirimler', 'esikler', ...(isSuper ? ['ayarlar'] : [])]
  const [tab, setTab] = useTab(tabIds, 'sunucular')
  const tabItems: TabItem[] = [
    { id: 'sunucular', label: 'Sunucular', badge: total, icon: Server },
    { id: 'ekle', label: 'Sunucu ekle', icon: Plus },
    { id: 'kisiler', label: 'İletişim kişileri', icon: Contact },
    { id: 'bildirimler', label: 'Bildirim kuralları', icon: BellRing },
    { id: 'esikler', label: 'Eşikler', icon: SlidersHorizontal },
    ...(isSuper ? [{ id: 'ayarlar', label: 'Ayarlar', icon: Settings }] : []),
  ]
  // Üst zincir: bu organizasyonun üst şirketleri (adları bilgi olarak görünür).
  const ancestors: Organization[] = []
  for (let cur = org?.parent_organization_id; cur && ancestors.length < 20; ) {
    const parent = allOrgs.find((o) => o.id === cur)
    if (!parent) break
    ancestors.unshift(parent)
    cur = parent.parent_organization_id
  }

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
      />
      {error && <div className="error-banner">{error}</div>}

      <Tabs items={tabItems} active={tab} onChange={setTab} label="Organizasyon sunucu bölümleri" />

      <TabPanel id="sunucular" active={tab}>
        {revealedHost && (
          <SecretNotice
            title={`${revealedHost.title} oluşturuldu — bu kimlik bilgisi bir daha gösterilmeyecek; host config dosyasına şimdi kopyalayın`}
            text={
              revealedHost.api_token
                ? `host_id: ${revealedHost.id}\napi_token: ${revealedHost.api_token}`
                : `host_id: ${revealedHost.id}\npull_secret: ${revealedHost.pull_secret}`
            }
            onClose={() => setRevealedHost(null)}
          />
        )}
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
          <OrganizationContacts organizationId={id} canEdit={canEdit} />
        </TabPanel>
      )}

      {id && (
        <TabPanel id="bildirimler" active={tab}>
          <div className="page-readable">
            <NotificationRules scope={{ organizationId: id }} canEdit={canEdit} />
          </div>
        </TabPanel>
      )}

      {org && (
        <TabPanel id="esikler" active={tab}>
          <OrganizationThresholds organization={org} orgs={allOrgs} canEdit={canEdit} />
        </TabPanel>
      )}

      {org && isSuper && (
        <TabPanel id="ayarlar" active={tab}>
          <OrganizationSettings key={org.id + org.name + (org.parent_organization_id ?? '')} organization={org} orgs={allOrgs} onChanged={loadOrg} />
        </TabPanel>
      )}

      {id && (
        <TabPanel id="ekle" active={tab} keepMounted>
          <AddHostWizard
            key={wizardKey}
            organizationId={id}
            onCancel={() => {
              setWizardKey((k) => k + 1)
              setTab('sunucular')
            }}
            onCreated={(created) => {
              setRevealedHost(created)
              setWizardKey((k) => k + 1)
              setTab('sunucular')
              reload()
            }}
          />
        </TabPanel>
      )}
    </div>
  )
}
