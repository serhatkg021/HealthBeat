import { useEffect, useState } from 'react'
import { Link, Navigate, useSearchParams } from 'react-router-dom'
import { BellRing, Building2, Contact, Server, Webhook, type LucideIcon } from 'lucide-react'
import { contactsApi, organizationsApi } from '../api/endpoints'
import { useAuth } from '../auth/AuthContext'
import { EmptyState } from '../components/EmptyState'
import { InfoTip } from '../components/InfoTip'
import { PageHeader } from '../components/PageHeader'
import { HostPicker, OrgPicker } from '../components/ScopePickers'
import { useScopeData } from '../components/useScopeData'
import { SearchInput } from '../components/SearchInput'
import { TabPanel, Tabs } from '../components/Tabs'
import { useTab } from '../components/useTab'
import { navigation, notificationsPath } from '../navigation'
import { NotificationPreviewCard } from './NotificationPreview'
import { NotificationRules } from './NotificationRules'
import { ChannelsSection, SystemContactsSection } from './NotificationSettings'
import { filterContacts, resolveRouteScope, type ContactRow, type RouteScopeChoice } from './notifications'

const ICONS: Record<string, LucideIcon> = {
  kanallar: BellRing,
  kurallar: Webhook,
  kisiler: Contact,
}

// Bildirim: bir alert'in nasıl (kanallar) ve kime (kurallar) gideceği tek yerde. Kanallar kurulum genelidir; kurallar
// sistem, organizasyon ya da sunucu kapsamındadır; Kişiler sekmesinde sistem kişileri düzenlenir, organizasyonların
// iletişim kişileri salt okunur listelenir (düzenleme organizasyon sayfasında). Her sekme yalnızca izni olana görünür.
export function NotificationsPage() {
  const { can } = useAuth()
  const [params] = useSearchParams()
  const tabs = navigation(can).notifications
  const ids = tabs.map((t) => t.id)
  const [active, setActive] = useTab(ids, ids[0] ?? '')
  // Eski "Sistem sahipleri" sekmesinin adresi: sistem kişileri artık Kişiler sekmesinde.
  if (params.get('sekme') === 'sahipler') return <Navigate to={notificationsPath('kisiler')} replace />
  if (tabs.length === 0) return <Navigate to="/" replace />
  return (
    <div>
      <PageHeader title="Bildirim" />
      <Tabs items={tabs.map((t) => ({ ...t, icon: ICONS[t.id] }))} active={active} onChange={setActive} label="Bildirim bölümleri" />
      <TabPanel id="kanallar" active={active}>
        <ChannelsSection canEdit={can('settings.manage')} />
      </TabPanel>
      <TabPanel id="kurallar" active={active}>
        <RoutesTab />
      </TabPanel>
      <TabPanel id="kisiler" active={active}>
        <div className="stack-col">
          {can('settings.view') && <SystemContactsSection canEdit={can('settings.manage')} />}
          {can('contact.view') && <ContactsTab />}
        </div>
      </TabPanel>
    </div>
  )
}

// Bildirim kuralları: sistem, organizasyon ya da sunucu seçilir, o kapsamın kuralları düzenlenir. Kapsam adreste
// (`?kapsam=&id=`). Sistem kurallarını settings.view görür, settings.manage düzenler.
function RoutesTab() {
  const { can } = useAuth()
  const canSeeOrgs = can('organization.view')
  const canSeeSystem = can('settings.view')
  const [params, setParams] = useSearchParams()
  const scope = resolveRouteScope(params.get('kapsam'), params.get('id'), canSeeOrgs, canSeeSystem)
  const { orgs, hosts, orgNames } = useScopeData(canSeeOrgs)

  function select(next: RouteScopeChoice) {
    const p = new URLSearchParams(params)
    p.set('kapsam', next.kind)
    if (next.id) p.set('id', next.id)
    else p.delete('id')
    setParams(p, { replace: true })
  }

  return (
    <div>
      <div className="toolbar" style={{ justifyContent: 'flex-start' }}>
        {(canSeeOrgs || canSeeSystem) && (
          <div className="segmented" role="group" aria-label="Kapsam">
            {canSeeSystem && (
              <button type="button" aria-pressed={scope.kind === 'sistem'} onClick={() => select({ kind: 'sistem' })}>
                Sistem
              </button>
            )}
            <button type="button" aria-pressed={scope.kind === 'org'} onClick={() => select({ kind: 'org' })}>
              Organizasyon
            </button>
            <button type="button" aria-pressed={scope.kind === 'sunucu'} onClick={() => select({ kind: 'sunucu' })}>
              Sunucu
            </button>
          </div>
        )}
        {scope.kind === 'org' && <OrgPicker idPrefix="routes" orgs={orgs} value={scope.id} onChange={(id) => select({ kind: 'org', id })} />}
        {scope.kind === 'sunucu' && (
          <HostPicker idPrefix="routes" hosts={hosts} orgNames={orgNames} value={scope.id} onChange={(id) => select({ kind: 'sunucu', id })} />
        )}
      </div>

      {scope.kind === 'sistem' ? (
        <div className="page-readable">
          <NotificationRules key="sistem" scope={{ system: true }} canEdit={can('settings.manage')} />
        </div>
      ) : scope.id ? (
        <div className="page-readable">
          <NotificationRules
            key={`${scope.kind}-${scope.id}`}
            scope={scope.kind === 'org' ? { organizationId: scope.id } : { hostId: scope.id }}
            canEdit={can('notification.edit')}
          />
        </div>
      ) : (
        <div className="card">
          <EmptyState icon={scope.kind === 'org' ? Building2 : Server}>
            {scope.kind === 'org' ? 'Kurallarını görmek için bir organizasyon seçin.' : 'Kurallarını görmek için bir sunucu seçin.'}
          </EmptyState>
        </div>
      )}

      <div className="page-readable" style={{ marginTop: 16 }}>
        <NotificationPreviewCard hosts={hosts} orgNames={orgNames} scopeHostId={scope.kind === 'sunucu' ? scope.id : undefined} />
      </div>
    </div>
  )
}

// Tüm organizasyonların iletişim kişileri, salt okunur ve aranabilir. Kişiler organizasyona aittir; düzenleme organizasyon
// sayfasının "İletişim kişileri" sekmesindedir. Server kişileri organizasyon başına verdiği için her organizasyon ayrı
// sorgulanır (birkaç düzine organizasyonda sorun değil).
function ContactsTab() {
  const { can } = useAuth()
  const [rows, setRows] = useState<ContactRow[] | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [q, setQ] = useState('')

  useEffect(() => {
    let cancelled = false
    organizationsApi
      .list()
      .then((orgs) =>
        Promise.all(orgs.map((o) => contactsApi.list(o.id).then((list) => list.map((c) => ({ ...c, organizationName: o.name }))))),
      )
      .then((lists) => !cancelled && setRows(lists.flat()))
      .catch((err) => !cancelled && setError(err instanceof Error ? err.message : 'iletişim kişileri yüklenemedi'))
    return () => {
      cancelled = true
    }
  }, [])

  const shown = rows ? filterContacts(rows, q) : []

  return (
    <div>
      {error && <div className="error-banner">{error}</div>}
      <div className="card table-card">
        <div className="card-head">
          <h2 className="card-title">
            Organizasyon kişileri
            <InfoTip label="Organizasyon kişileri">Burada salt okunur listelenir; kişiler organizasyon sayfasında düzenlenir.</InfoTip>
          </h2>
          <SearchInput value={q} onChange={setQ} placeholder="Kişi ya da organizasyon ara…" debounceMs={0} />
        </div>
        <table className="stack">
          <thead>
            <tr>
              <th>Kişi</th>
              <th>Organizasyon</th>
              <th>E-posta</th>
              <th>Telefon</th>
              <th>Departman / Unvan</th>
              {can('contact.edit') && <th className="actions" />}
            </tr>
          </thead>
          <tbody>
            {rows === null && !error && (
              <tr>
                <td colSpan={6} className="empty-cell muted">
                  Yükleniyor…
                </td>
              </tr>
            )}
            {shown.map((c) => (
              <tr key={c.id}>
                <td className="primary">{c.name}</td>
                <td data-label="Organizasyon">{c.organizationName}</td>
                <td data-label="E-posta">{c.email || <span className="muted">—</span>}</td>
                <td data-label="Telefon" className="tnum">
                  {c.phone || <span className="muted">—</span>}
                </td>
                <td data-label="Departman / Unvan">{[c.department, c.title].filter(Boolean).join(' · ') || <span className="muted">—</span>}</td>
                {can('contact.edit') && (
                  <td className="actions">
                    <Link className="btn btn-sm" to={`/organizations/${c.organization_id}?sekme=kisiler`}>
                      Düzenle →
                    </Link>
                  </td>
                )}
              </tr>
            ))}
            {rows !== null && shown.length === 0 && (
              <tr>
                <td colSpan={6} className="empty-cell">
                  <EmptyState icon={Contact}>{rows.length === 0 ? 'Henüz iletişim kişisi yok.' : 'Aramayla eşleşen kişi yok.'}</EmptyState>
                </td>
              </tr>
            )}
          </tbody>
        </table>
      </div>
    </div>
  )
}
