import { Link, useSearchParams } from 'react-router-dom'
import { Building2, ListPlus, Server } from 'lucide-react'
import { useAuth } from '../auth/AuthContext'
import { ComingSoon } from '../components/ComingSoon'
import { HostPicker, OrgPicker } from '../components/ScopePickers'
import { useScopeData } from '../components/useScopeData'
import { EmptyState } from '../components/EmptyState'
import { PageHeader } from '../components/PageHeader'
import { StatusBadge } from '../components/StatusBadge'
import { alertLevelLabel, alertLevelTone } from '../labels'
import type { RuleScope } from '../navigation'
import type { Organization, OverviewHost } from '../types/api'
import { resolveScope } from './alertRules'
import { SAMPLE_RULES } from './comingSoonSamples'
import { DiskAlertSettings } from './DiskAlertSettings'
import { HostThresholdSettings } from './HostThresholdSettings'
import { OrganizationThresholds } from './OrganizationThresholds'
import { SystemThresholds } from './SystemThresholds'

// Alert kuralları: sistem varsayılanı, organizasyon ve sunucu eşikleri tek sayfada, üstteki kapsam seçiciyle. Kurallar
// sistem → organizasyon (üst şirketten alta) → sunucu sırasıyla devralınır; her kapsam kendi editörünü gösterir. Kapsam
// adreste (`?kapsam=&id=`) tutulur, böylece organizasyon ve sunucu sayfalarından doğrudan bağlantı verilebilir.
export function AlertRulesPage() {
  const { can } = useAuth()
  const canSeeOrgs = can('organization.view')
  const [params, setParams] = useSearchParams()
  const scope = resolveScope(params.get('kapsam'), params.get('id'), canSeeOrgs)

  const { orgs, hosts, orgNames } = useScopeData(canSeeOrgs)

  function select(next: RuleScope) {
    const p = new URLSearchParams()
    if (next.kind !== 'sistem') p.set('kapsam', next.kind)
    if (next.kind !== 'sistem' && next.id) p.set('id', next.id)
    setParams(p, { replace: true })
  }

  return (
    <div>
      <PageHeader title="Alert kuralları" subtitle="Ne zaman alert açılacağı: sistem varsayılanı, organizasyon ve sunucu eşikleri tek yerde" />

      <div className="toolbar" style={{ justifyContent: 'flex-start' }}>
        <div className="segmented" role="group" aria-label="Kapsam">
          <button type="button" aria-pressed={scope.kind === 'sistem'} onClick={() => select({ kind: 'sistem' })}>
            Sistem varsayılanı
          </button>
          {canSeeOrgs && (
            <button type="button" aria-pressed={scope.kind === 'org'} onClick={() => select({ kind: 'org' })}>
              Organizasyon
            </button>
          )}
          <button type="button" aria-pressed={scope.kind === 'sunucu'} onClick={() => select({ kind: 'sunucu' })}>
            Sunucu
          </button>
        </div>
        {scope.kind === 'org' && <OrgPicker idPrefix="rules" orgs={orgs} value={scope.id} onChange={(id) => select({ kind: 'org', id })} />}
        {scope.kind === 'sunucu' && <HostPicker idPrefix="rules" hosts={hosts} orgNames={orgNames} value={scope.id} onChange={(id) => select({ kind: 'sunucu', id })} />}
      </div>

      {scope.kind === 'sistem' && <SystemThresholds />}
      {scope.kind === 'org' && <OrgScope orgs={orgs} id={scope.id} />}
      {scope.kind === 'sunucu' && <HostScope hosts={hosts} id={scope.id} />}

      {/* Editörlerin kökü kart değil (sarmalayıcı div), bu yüzden kartlar arası boşluk kendiliğinden oluşmaz. */}
      <div style={{ marginTop: 16 }}>
        <RulesPreview />
      </div>
    </div>
  )
}

function OrgScope({ orgs, id }: { orgs: Organization[]; id?: string }) {
  const { can } = useAuth()
  const org = orgs.find((o) => o.id === id)
  if (!org) {
    return (
      <div className="card">
        <EmptyState icon={Building2}>{id && orgs.length > 0 ? 'Bu organizasyon bulunamadı.' : 'Kurallarını görmek için bir organizasyon seçin.'}</EmptyState>
      </div>
    )
  }
  return <OrganizationThresholds key={org.id} organization={org} orgs={orgs} canEdit={can('threshold.edit')} />
}

function HostScope({ hosts, id }: { hosts: OverviewHost[]; id?: string }) {
  const { can } = useAuth()
  if (!id) {
    return (
      <div className="card">
        <EmptyState icon={Server}>Kurallarını görmek için bir sunucu seçin.</EmptyState>
      </div>
    )
  }
  const host = hosts.find((h) => h.id === id)
  return (
    <div>
      <p className="muted" style={{ margin: '0 0 12px' }}>
        {host ? (
          <>
            <Link to={`/hosts/${id}`}>{host.title}</Link> için geçerli değerler; “Varsayılan” seçili metrikler sistemden ya da organizasyondan devralınır.
          </>
        ) : (
          'Seçili sunucu için geçerli değerler.'
        )}
      </p>
      <div className="stack-col">
        <HostThresholdSettings key={`t-${id}`} hostId={id} canEdit={can('threshold.edit')} />
        <DiskAlertSettings key={`d-${id}`} hostId={id} canEdit={can('host.update')} />
      </div>
    </div>
  )
}

function RulesPreview() {
  return (
    <ComingSoon
      title="Yeni kural türleri"
      icon={ListPlus}
      description="Servis, Docker sağlığı, disk G/Ç, sıcaklık ve sistem durumu kuralları; aynı kapsamlarla (sistem → organizasyon → sunucu) devralınacak."
    >
      <table>
        <thead>
          <tr>
            <th>Grup</th>
            <th>Kural</th>
            <th>Koşul</th>
            <th>Süre</th>
            <th>Seviye</th>
          </tr>
        </thead>
        <tbody>
          {SAMPLE_RULES.map((r) => (
            <tr key={r.name}>
              <td className="muted">{r.group}</td>
              <td>{r.name}</td>
              <td>{r.condition}</td>
              <td className="muted">{r.duration}</td>
              <td>
                <StatusBadge tone={alertLevelTone(r.level)}>{alertLevelLabel(r.level)}</StatusBadge>
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </ComingSoon>
  )
}
