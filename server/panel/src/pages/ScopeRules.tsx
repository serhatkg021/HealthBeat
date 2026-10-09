import { useEffect, useMemo, useState } from 'react'
import { Check } from 'lucide-react'
import { statusRulesApi, thresholdsApi } from '../api/endpoints'
import { useAuth } from '../auth/AuthContext'
import type { MetricType, Organization } from '../types/api'
import { changedKeys, customizeItem, hostProblems, hostRow, hostSummary, inheritItem, inheritedText, revertItem, type HostRuleState, type RuleRowView } from './hostRuleRows'
import { StatusDurationField, StatusLevelField, ThresholdDurationField, ThresholdLevelFields } from './RuleEditors'
import { RuleButtons, RuleRow, RuleSaveBar, RuleTopicLayout, type TopicMenuItem } from './RuleTopics'
import { TOPICS, itemKey, ruleCount, topicHasChanges, topicInfo, topicItems, type TopicId, type TopicItem } from './ruleTopics'
import { keepFailedScope, scopeRules, scopeSavePlan, type ScopeRules as Loaded } from './scopeRules'
import { parentMap } from './orgTree'
import { validateRuleDraft } from './statusRules'
import { metricInfo, validateDraft } from './thresholds'

export type ScopeTarget = { kind: 'system' } | { kind: 'org'; organization: Organization; orgs: Organization[] }

// Alert kuralları sayfasının sistem ve organizasyon kapsamları: kapsamın kendi eşikleri ve durum kuralları, sunucu
// kapsamıyla aynı konu düzeninde. Organizasyon üst şirketinden, o da sistemden devralır; sistemde devralınacak bir şey
// yoktur (tanımlanmayan kural kapalıdır). Kapsam değişince bileşen yeniden kurulur (key) ve kaydedilmemiş değişiklikler
// atılır.
export function ScopeRules({ target, topic, onTopic }: { target: ScopeTarget; topic: TopicId; onTopic: (id: TopicId) => void }) {
  const { can, user } = useAuth()
  const system = target.kind === 'system'
  const organizationId = target.kind === 'org' ? target.organization.id : undefined
  const orgs = target.kind === 'org' ? target.orgs : undefined
  // Genel kuralları server yalnızca super_admin'e yazdırır; organizasyonda yetki kapsamını server denetler.
  const canEdit = can('threshold.edit') && (!system || user?.role === 'super_admin')
  const scopeKind = system ? 'sistem' : 'org'
  const [loaded, setLoaded] = useState<Loaded | null>(null)
  const [draft, setDraft] = useState<HostRuleState | null>(null)
  const [open, setOpen] = useState<Set<string>>(new Set())
  const [loadError, setLoadError] = useState<string | null>(null)
  const [saveError, setSaveError] = useState<string | null>(null)
  const [notice, setNotice] = useState(false)
  const [saving, setSaving] = useState(false)

  const load = useMemo(() => {
    const parents = parentMap(orgs ?? [])
    const nameOf = (id: string) => orgs?.find((o) => o.id === id)?.name ?? 'üst şirket'
    return async () => {
      const [thresholds, status] = await Promise.all([thresholdsApi.list(), statusRulesApi.list()])
      return scopeRules(thresholds, status, { organizationId }, parents, nameOf)
    }
  }, [orgs, organizationId])

  useEffect(() => {
    let cancelled = false
    load()
      .then((res) => {
        if (cancelled) return
        setLoaded(res)
        setDraft(res.state)
      })
      .catch((err) => !cancelled && setLoadError(err instanceof Error ? err.message : 'kurallar yüklenemedi'))
    return () => {
      cancelled = true
    }
  }, [load])

  const saved = loaded?.state ?? null
  const changes = useMemo(() => (saved && draft ? changedKeys(saved, draft) : new Set<string>()), [saved, draft])
  const problems = useMemo(() => (draft ? hostProblems(draft) : []), [draft])

  if (loadError) return <div className="error-banner">{loadError}</div>
  if (!loaded || !saved || !draft) return <div className="muted">Yükleniyor…</div>

  const edit = (next: HostRuleState) => {
    setNotice(false)
    setDraft(next)
  }
  const toggle = (key: string, on: boolean) =>
    setOpen((prev) => {
      const next = new Set(prev)
      if (on) next.add(key)
      else next.delete(key)
      return next
    })

  async function save() {
    if (!loaded || !saved || !draft) return
    const plan = scopeSavePlan(saved, draft, loaded.ids)
    const failedMetrics = new Set<MetricType>()
    let statusFailed = false
    const errors: string[] = []
    const message = (err: unknown) => (err instanceof Error ? err.message : 'kaydedilemedi')
    setSaving(true)
    setSaveError(null)
    // Server'da toplu eşik ucu yok: değişen her eşik sırayla gönderilir; biri hata verirse diğerleri yine kaydedilir.
    for (const op of plan.thresholds) {
      try {
        if (op.op === 'create') {
          await thresholdsApi.create({ organization_id: organizationId, metric_type: op.metric, ...op.body, duration_seconds: op.body.duration_seconds ?? undefined })
        } else if (op.op === 'update') {
          await thresholdsApi.update(op.id, op.body)
        } else {
          await thresholdsApi.remove(op.id)
        }
      } catch (err) {
        failedMetrics.add(op.metric)
        errors.push(`${metricInfo(op.metric).label}: ${message(err)}`)
      }
    }
    if (plan.status) {
      try {
        await statusRulesApi.set(organizationId ?? null, plan.status)
      } catch (err) {
        statusFailed = true
        errors.push(`Durum kuralları: ${message(err)}`)
      }
    }
    try {
      const fresh = await load()
      setLoaded(fresh)
      setDraft(keepFailedScope(fresh.state, draft, failedMetrics, statusFailed))
      const ok = failedMetrics.size === 0 && !statusFailed
      if (ok) setOpen(new Set())
      setNotice(ok)
      setSaveError(errors.length > 0 ? errors.join(' · ') : null)
    } catch (err) {
      errors.push(`Kurallar yeniden okunamadı: ${message(err)}`)
      setSaveError(errors.join(' · '))
    } finally {
      setSaving(false)
    }
  }

  const ownText = system ? 'Sistem' : 'Bu organizasyon'
  const rowsOf = (id: TopicId) => topicItems(topicInfo(id), scopeKind).map((item) => ({ item, row: hostRow(item, draft) }))
  const menu: TopicMenuItem[] = TOPICS.map((t) => {
    const items = topicItems(t, scopeKind)
    const count = ruleCount(items, (item) => hostRow(item, draft).on)
    return { id: t.id, title: t.title, desc: t.desc, on: count.on, total: count.total, changed: topicHasChanges(items, changes) }
  })
  const summary = hostSummary(TOPICS.flatMap((t) => rowsOf(t.id).map((r) => r.row)))
  const changedTopics = TOPICS.filter((t) => topicHasChanges(t.items, changes)).map((t) => t.title)

  function renderRow({ item, row }: { item: TopicItem; row: RuleRowView }) {
    if (!saved || !draft) return null
    const key = itemKey(item)
    const changed = changes.has(key)
    const editingOpen = open.has(key) || changed
    const buttons = (own: boolean, editing: boolean) =>
      canEdit ? (
        <RuleButtons
          own={own}
          editing={editing}
          changed={changed}
          disabled={saving}
          system={system}
          onRevert={() => {
            edit(revertItem(item, saved, draft))
            toggle(key, false)
          }}
          onEdit={() => toggle(key, true)}
          onInherit={() => edit(inheritItem(item, draft))}
          onCustomize={() => {
            edit(customizeItem(item, draft))
            toggle(key, true)
          }}
        />
      ) : undefined
    const sourceText = (from: string | undefined) => (row.source === 'own' ? ownText : inheritedText(row, from))

    if (item.kind === 'threshold') {
      const m = item.metric
      const d = draft.thresholds[m]
      const own = d.mode === 'custom'
      const editing = canEdit && own && editingOpen
      const set = (next: typeof d) => edit({ ...draft, thresholds: { ...draft.thresholds, [m]: next } })
      return (
        <RuleRow
          key={key}
          row={row}
          changed={changed}
          editor={editing ? <ThresholdLevelFields metric={m} draft={d} onChange={set} disabled={saving} /> : undefined}
          durationEditor={editing ? <ThresholdDurationField metric={m} draft={d} onChange={set} disabled={saving} /> : undefined}
          error={editing ? validateDraft(m, d) : null}
          sourceText={sourceText(loaded?.sources.thresholds[m])}
          actions={buttons(own, editing)}
        />
      )
    }
    if (item.kind === 'status') {
      const r = item.rule
      const d = draft.status[r]
      const own = d.level !== ''
      const editing = canEdit && own && editingOpen
      const set = (next: typeof d) => edit({ ...draft, status: { ...draft.status, [r]: next } })
      return (
        <RuleRow
          key={key}
          row={row}
          changed={changed}
          editor={editing ? <StatusLevelField rule={r} draft={d} onChange={set} disabled={saving} allowOff={!system} /> : undefined}
          durationEditor={editing ? <StatusDurationField rule={r} draft={d} onChange={set} disabled={saving} /> : undefined}
          error={editing ? validateRuleDraft(r, d) : null}
          sourceText={sourceText(loaded?.sources.status[r])}
          actions={buttons(own, editing)}
        />
      )
    }
    return <RuleRow key={key} row={row} />
  }

  return (
    <div className="stack-col">
      <div className="card rule-summary">
        {system ? (
          <>
            <strong>Sistem varsayılanında {summary.on} kural tanımlı.</strong>
            <span className="muted">
              Kendi değeri olmayan her organizasyon ve sunucu bunları kullanır; hiçbir kapsamda tanımlanmayan kural kapalıdır.
              {!canEdit && ' Genel kuralları yalnızca süper admin değiştirebilir.'}
            </span>
          </>
        ) : (
          <>
            <strong>
              {target.kind === 'org' && target.organization.name} için {summary.on} kural etkin; {summary.own} tanesi bu organizasyona özel.
            </strong>
            <span className="muted">
              Tanımlanmayan değer üst şirketten, o da yoksa sistemden devralınır. Alt organizasyonlar ve sunucular bu değerleri devralır; bir
              sunucunun kendi değeri her zaman önceliklidir.
            </span>
          </>
        )}
        {notice && changes.size === 0 && (
          <span className="save-note" style={{ margin: '6px 0 0' }}>
            <Check size={14} strokeWidth={2.2} />
            Kurallar kaydedildi.
          </span>
        )}
      </div>
      <RuleTopicLayout menu={menu} active={topic} onSelect={onTopic}>
        {rowsOf(topic).map(renderRow)}
      </RuleTopicLayout>
      <RuleSaveBar
        count={changes.size}
        topics={changedTopics}
        saving={saving}
        error={saveError}
        blocked={problems.length > 0 ? `Düzeltilmesi gereken değer var: ${problems[0]}` : undefined}
        onSave={save}
        onDiscard={() => {
          setDraft(saved)
          setOpen(new Set())
          setSaveError(null)
        }}
      />
    </div>
  )
}
