import { useEffect, useState } from 'react'
import { hostsApi, organizationsApi, statusRulesApi, thresholdsApi } from '../api/endpoints'
import { useAuth } from '../auth/AuthContext'
import type { Host, HostStatusRuleView, HostThresholdsResponse } from '../types/api'
import { parentMap } from './orgTree'
import { ruleLinesFor, type RuleLine } from './perfTopics'
import type { RuleItem } from './ruleTopics'
import { hostSources, type RuleSources } from './scopeRules'

// Sunucu sayfasındaki alert kuralları listeleri (Performans konuları, Envanter'in Saat ve Bakım kartları): durum
// kurallarını ve — organizasyonları görebilene — devralınan değerlerin kaynak adlarını okur. Okunamazlarsa liste yalnızca
// eşikleri ya da kaynak adı olmadan gösterir. Kuralları göremeyen kullanıcıda `canSee` false'tur ve liste çıkmaz.
export function useHostRules(host: Host, thresholds: HostThresholdsResponse | null) {
  const { can } = useAuth()
  const canSee = can('threshold.view')
  const canSeeOrgs = can('organization.view')
  const [status, setStatus] = useState<HostStatusRuleView[] | null>(null)
  const [sources, setSources] = useState<RuleSources | undefined>(undefined)

  useEffect(() => {
    if (!canSee) return
    let cancelled = false
    hostsApi
      .statusRules(host.id)
      .then((r) => !cancelled && setStatus(r))
      .catch(() => undefined)
    if (canSeeOrgs) {
      Promise.all([organizationsApi.list(), thresholdsApi.list(), statusRulesApi.list()])
        .then(([orgs, t, s]) => {
          if (cancelled) return
          const nameOf = (id: string) => orgs.find((o) => o.id === id)?.name ?? 'üst şirket'
          setSources(hostSources(t, s, host.organization_id, parentMap(orgs), nameOf))
        })
        .catch(() => undefined)
    }
    return () => {
      cancelled = true
    }
  }, [host.id, host.organization_id, canSee, canSeeOrgs])

  return {
    canSee,
    canEdit: can('threshold.edit'),
    linesFor: (items: readonly RuleItem[]): RuleLine[] => (canSee ? ruleLinesFor(items, thresholds, status, sources) : []),
  }
}
