// Alert kuralları sayfasının sistem ve organizasyon kapsamları: genel ve organizasyon eşikleri ile durum kuralları,
// sunucu kapsamıyla aynı taslak biçimine (HostRuleState) çevrilir; böylece satırlar, düğmeler ve kaydet çubuğu ortaktır.
// Kapsamın kendi satırı "özel", üstten gelen "varsayılan"dır. React yok; Node'un çalıştırıcısıyla birim test edilir.
import type { MetricType, StatusRule, StatusRuleChanges, StatusRuleConfig, ThresholdConfig } from '../types/api.ts'
import { emptySubjectDrafts, defaultDrafts, defaultSource, isMetricDirty, levelsDraft, orgChain, rowPayload, METRIC_TYPES, type Defaults, type ParentMap } from './thresholds.ts'
import { STATUS_RULES, inheritedForOrg, ruleChanges, scopeDrafts } from './statusRules.ts'
import type { HostRuleState } from './hostRuleRows.ts'

// organizationId yoksa sistem varsayılanıdır.
export interface ScopeTarget {
  organizationId?: string
}

// Devralınan değerin geldiği yerin adı ("Sistem" ya da organizasyonun adı); kural için devralınan bir şey yoksa yok.
export interface RuleSources {
  thresholds: Partial<Record<MetricType, string>>
  status: Partial<Record<StatusRule, string>>
}

export interface ScopeRules {
  state: HostRuleState
  // Kapsamın kendi eşik satırlarının kimlikleri (güncelleme ve silme için).
  ids: Partial<Record<MetricType, string>>
  sources: RuleSources
}

const SYSTEM = 'Sistem'

const levelsOf = (t: ThresholdConfig) => ({
  warning_level: t.warning_level,
  critical_level: t.critical_level,
  ...(t.duration_seconds ? { duration_seconds: t.duration_seconds } : {}),
})

export function scopeRules(
  thresholds: ThresholdConfig[],
  status: StatusRuleConfig[],
  target: ScopeTarget,
  parents: ParentMap,
  nameOf: (organizationId: string) => string,
): ScopeRules {
  const orgId = target.organizationId
  const drafts = defaultDrafts()
  const ids: ScopeRules['ids'] = {}
  const defaults: Defaults = {}
  const sources: RuleSources = { thresholds: {}, status: {} }
  for (const m of METRIC_TYPES) {
    const own = thresholds.find((t) => t.metric_type === m && (t.organization_id ?? undefined) === orgId)
    if (own) {
      drafts[m] = { mode: 'custom', ...levelsDraft(levelsOf(own)) }
      ids[m] = own.id
    }
    // Organizasyon üst şirketinden (yoksa sistemden) devralır; sistemin üstünde bir şey yoktur.
    if (orgId) {
      const inherited = defaultSource(thresholds, parents.get(orgId), parents, m)
      if (inherited) {
        defaults[m] = inherited.levels
        sources.thresholds[m] = inherited.fromOrganizationId ? nameOf(inherited.fromOrganizationId) : SYSTEM
      }
    }
  }
  const statusDrafts = scopeDrafts(status, orgId)
  const statusDefaults: HostRuleState['statusDefaults'] = {}
  for (const r of STATUS_RULES) {
    // Sistemde "kapalı" ile "tanımsız" aynıdır: ikisi de tanımsız gösterilir.
    if (!orgId && statusDrafts[r.rule].level === 'off') statusDrafts[r.rule] = { ...statusDrafts[r.rule], level: '' }
    if (orgId) {
      const inherited = inheritedForOrg(status, orgId, parents, r.rule)
      statusDefaults[r.rule] = inherited?.setting ?? null
      if (inherited) sources.status[r.rule] = inherited.fromOrganizationId ? nameOf(inherited.fromOrganizationId) : SYSTEM
    }
  }
  return {
    state: {
      thresholds: drafts,
      defaults,
      mounts: {},
      containers: {},
      subjects: emptySubjectDrafts(),
      status: statusDrafts,
      statusDefaults,
      disks: null,
      watched: null,
    },
    ids,
    sources,
  }
}

// Bir sunucunun devraldığı değerlerin kaynağı: sunucunun organizasyonundan köke doğru ilk tanımlı olan, yoksa sistem.
// Server sunucu yanıtında kaynağı söylemez; organizasyonları görebilen kullanıcıya listelerden hesaplanır.
export function hostSources(
  thresholds: ThresholdConfig[],
  status: StatusRuleConfig[],
  organizationId: string,
  parents: ParentMap,
  nameOf: (organizationId: string) => string,
): RuleSources {
  const out: RuleSources = { thresholds: {}, status: {} }
  for (const m of METRIC_TYPES) {
    const found = defaultSource(thresholds, organizationId, parents, m)
    if (found) out.thresholds[m] = found.fromOrganizationId ? nameOf(found.fromOrganizationId) : SYSTEM
  }
  for (const r of STATUS_RULES) {
    const chain = orgChain(organizationId, parents)
    const own = chain.map((id) => status.find((x) => x.rule === r.rule && x.organization_id === id)).find((x) => x !== undefined)
    if (own?.organization_id) out.status[r.rule] = nameOf(own.organization_id)
    else if (status.some((x) => x.rule === r.rule && !x.organization_id)) out.status[r.rule] = SYSTEM
  }
  return out
}

export type ThresholdOp =
  | { metric: MetricType; op: 'create'; body: ReturnType<typeof rowPayload> }
  | { metric: MetricType; op: 'update'; id: string; body: ReturnType<typeof rowPayload> }
  | { metric: MetricType; op: 'remove'; id: string }

export interface ScopeSavePlan {
  thresholds: ThresholdOp[]
  status?: StatusRuleChanges
}

// Server'da toplu eşik ucu yok: değişen her eşik ayrı oluşturulur, güncellenir ya da silinir. Durum kuralları tek çağrıdır.
export function scopeSavePlan(saved: HostRuleState, draft: HostRuleState, ids: ScopeRules['ids']): ScopeSavePlan {
  const ops: ThresholdOp[] = []
  for (const m of METRIC_TYPES) {
    const before = saved.thresholds[m]
    const after = draft.thresholds[m]
    if (!isMetricDirty(m, after, before)) continue
    const id = ids[m]
    if (after.mode === 'custom') ops.push(id ? { metric: m, op: 'update', id, body: rowPayload(m, after) } : { metric: m, op: 'create', body: rowPayload(m, after) })
    else if (id) ops.push({ metric: m, op: 'remove', id })
  }
  const status = ruleChanges(saved.status, draft.status)
  return Object.keys(status).length > 0 ? { thresholds: ops, status } : { thresholds: ops }
}

// Kaydetmeden sonra server'dan okunan durum; kaydedilemeyen eşikler ve (başarısızsa) durum kuralları taslakta kalır.
export function keepFailedScope(fresh: HostRuleState, draft: HostRuleState, failedMetrics: ReadonlySet<MetricType>, statusFailed: boolean): HostRuleState {
  const thresholds = { ...fresh.thresholds }
  for (const m of failedMetrics) thresholds[m] = draft.thresholds[m]
  return { ...fresh, thresholds, status: statusFailed ? draft.status : fresh.status }
}
