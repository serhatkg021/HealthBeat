import { useSearchParams } from 'react-router-dom'
import { Building2, Server } from 'lucide-react'
import { useAuth } from '../auth/AuthContext'
import { HostPicker, OrgPicker } from '../components/ScopePickers'
import { useScopeData } from '../components/useScopeData'
import { EmptyState } from '../components/EmptyState'
import { PageHeader } from '../components/PageHeader'
import type { RuleScope } from '../navigation'
import type { Organization } from '../types/api'
import { resolveScope } from './alertRules'
import { HostRules } from './HostRules'
import { OrganizationThresholds } from './OrganizationThresholds'
import { TOPICS, resolveTopic, type TopicId } from './ruleTopics'
import { StatusRulesCard } from './StatusRulesCard'
import { SystemThresholds } from './SystemThresholds'

// Alert kuralları: sistem varsayılanı, organizasyon ve sunucu eşikleri ile durum kuralları tek sayfada, üstteki kapsam
// seçiciyle. Kurallar
// sistem → organizasyon (üst şirketten alta) → sunucu sırasıyla devralınır; her kapsam kendi editörünü gösterir. Kapsam
// adreste (`?kapsam=&id=`) tutulur, böylece organizasyon ve sunucu sayfalarından doğrudan bağlantı verilebilir.
export function AlertRulesPage() {
  const { can, user } = useAuth()
  const canSeeOrgs = can('organization.view')
  const [params, setParams] = useSearchParams()
  const scope = resolveScope(params.get('kapsam'), params.get('id'), canSeeOrgs)
  const topic = resolveTopic(params.get('konu'))

  const { orgs, hosts, orgNames } = useScopeData(canSeeOrgs)

  // Kapsam ya da sunucu değişince seçili konu korunur; kaydedilmemiş değişiklikler (kapsamın bileşeni yeniden kurulduğu
  // için) atılır.
  function select(next: RuleScope, nextTopic: TopicId = topic) {
    const p = new URLSearchParams()
    if (next.kind !== 'sistem') p.set('kapsam', next.kind)
    if (next.kind !== 'sistem' && next.id) p.set('id', next.id)
    if (nextTopic !== TOPICS[0].id) p.set('konu', nextTopic)
    setParams(p, { replace: true })
  }

  return (
    <div>
      <PageHeader title="Alert kuralları" subtitle="Ne zaman alert açılacağı: sistem varsayılanı, organizasyon ve sunucu eşikleri ve durum kuralları tek yerde" />

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

      {scope.kind === 'sistem' && (
        <div className="stack-col">
          <SystemThresholds />
          {/* Genel kuralları server yalnızca super_admin'e yazdırır (eşiklerde olduğu gibi). */}
          <StatusRulesCard scope={{ kind: 'system' }} canEdit={can('threshold.edit') && user?.role === 'super_admin'} />
        </div>
      )}
      {scope.kind === 'org' && <OrgScope orgs={orgs} id={scope.id} />}
      {scope.kind === 'sunucu' &&
        (scope.id ? (
          <HostRules key={scope.id} hostId={scope.id} host={hosts.find((h) => h.id === scope.id)} topic={topic} onTopic={(t) => select(scope, t)} />
        ) : (
          <div className="card">
            <EmptyState icon={Server}>Kurallarını görmek için bir sunucu seçin.</EmptyState>
          </div>
        ))}
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
  return (
    <div className="stack-col">
      <OrganizationThresholds key={org.id} organization={org} orgs={orgs} canEdit={can('threshold.edit')} />
      <StatusRulesCard key={`s-${org.id}`} scope={{ kind: 'org', organization: org, orgs }} canEdit={can('threshold.edit')} />
    </div>
  )
}
