import { useEffect, useState } from 'react'
import { Link, Navigate, useSearchParams } from 'react-router-dom'
import { BellRing, Building2, Contact, Server, Users, Webhook, type LucideIcon } from 'lucide-react'
import { contactsApi, organizationsApi } from '../api/endpoints'
import { useAuth } from '../auth/AuthContext'
import { ComingSoon } from '../components/ComingSoon'
import { EmptyState } from '../components/EmptyState'
import { PageHeader } from '../components/PageHeader'
import { HostPicker, OrgPicker } from '../components/ScopePickers'
import { useScopeData } from '../components/useScopeData'
import { SearchInput } from '../components/SearchInput'
import { TabPanel, Tabs } from '../components/Tabs'
import { useTab } from '../components/useTab'
import { navigation } from '../navigation'
import { SAMPLE_RECIPIENTS } from './comingSoonSamples'
import { NotificationRules } from './NotificationRules'
import { ChannelsSection, OwnersSection } from './NotificationSettings'
import { filterContacts, resolveRouteScope, type ContactRow, type RouteScopeChoice } from './notifications'

const ICONS: Record<string, LucideIcon> = {
  kanallar: BellRing,
  sahipler: Users,
  kurallar: Webhook,
  kisiler: Contact,
}

// Bildirim: bir alert'in kime, hangi kanaldan gideceği tek yerde. Kanallar ve sistem sahipleri kurulum genelidir; kurallar
// organizasyon ya da sunucu kapsamındadır; iletişim kişileri burada salt okunur listelenir (düzenleme organizasyon
// sayfasında). Her sekme yalnızca izni olana görünür.
export function NotificationsPage() {
  const { can } = useAuth()
  const tabs = navigation(can).notifications
  const ids = tabs.map((t) => t.id)
  const [active, setActive] = useTab(ids, ids[0] ?? '')
  if (tabs.length === 0) return <Navigate to="/" replace />
  return (
    <div>
      <PageHeader title="Bildirim" />
      <Tabs items={tabs.map((t) => ({ ...t, icon: ICONS[t.id] }))} active={active} onChange={setActive} label="Bildirim bölümleri" />
      <TabPanel id="kanallar" active={active}>
        <ChannelsSection canEdit={can('settings.manage')} />
        <div className="grid-2" style={{ marginTop: 16 }}>
          <ComingSoon title="Telegram" description="Bir bot üzerinden sohbete ya da gruba alert iletisi.">
            <dl className="info-list single">
              <div className="info-row">
                <dt>Bot</dt>
                <dd className="mono">@healthbeat_alert_bot</dd>
              </div>
              <div className="info-row">
                <dt>Sohbet</dt>
                <dd>NOC grubu</dd>
              </div>
            </dl>
          </ComingSoon>
          <ComingSoon title="Webhook" description="Slack, Teams ya da kendi sisteminize JSON gövdeli POST isteği.">
            <dl className="info-list single">
              <div className="info-row">
                <dt>Adres</dt>
                <dd className="mono">https://hooks.example.com/…</dd>
              </div>
              <div className="info-row">
                <dt>Son gönderim</dt>
                <dd>başarılı · 2 dk önce</dd>
              </div>
            </dl>
          </ComingSoon>
        </div>
      </TabPanel>
      <TabPanel id="sahipler" active={active}>
        <OwnersSection canEdit={can('settings.manage')} />
      </TabPanel>
      <TabPanel id="kurallar" active={active}>
        <RoutesTab />
      </TabPanel>
      <TabPanel id="kisiler" active={active}>
        <ContactsTab />
      </TabPanel>
    </div>
  )
}

// Bildirim kuralları: organizasyon ya da sunucu seçilir, o kapsamın kuralları düzenlenir. Kapsam adreste (`?kapsam=&id=`).
function RoutesTab() {
  const { can } = useAuth()
  const canSeeOrgs = can('organization.view')
  const [params, setParams] = useSearchParams()
  const scope = resolveRouteScope(params.get('kapsam'), params.get('id'), canSeeOrgs)
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
        {canSeeOrgs && (
          <div className="segmented" role="group" aria-label="Kapsam">
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

      {scope.id ? (
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

      <div style={{ marginTop: 16 }}>
        <ComingSoon
          title="Bu alert kime gider?"
          description="Bir sunucu ve seviye seçilince sistem sahipleri, organizasyon zinciri ve sunucu kuralları birleştirilip alıcılar gösterilir."
        >
          <p className="muted" style={{ margin: '0 0 8px' }}>
            {SAMPLE_RECIPIENTS.host} · {SAMPLE_RECIPIENTS.level}
          </p>
          <table>
            <thead>
              <tr>
                <th>Alıcı</th>
                <th>Nereden</th>
                <th>Kanal</th>
              </tr>
            </thead>
            <tbody>
              {SAMPLE_RECIPIENTS.recipients.map((r) => (
                <tr key={r.name}>
                  <td>{r.name}</td>
                  <td className="muted">{r.source}</td>
                  <td>{r.channel}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </ComingSoon>
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
      <div className="toolbar">
        <SearchInput value={q} onChange={setQ} placeholder="Kişi ya da organizasyon ara…" debounceMs={0} />
        <span className="muted" style={{ fontSize: 13 }}>
          Salt okunur; kişiler organizasyon sayfasında düzenlenir.
        </span>
      </div>
      {error && <div className="error-banner">{error}</div>}
      <div className="card table-card">
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
