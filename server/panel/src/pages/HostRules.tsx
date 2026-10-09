import { useEffect, useMemo, useState } from 'react'
import { Link } from 'react-router-dom'
import { Check } from 'lucide-react'
import { hostsApi } from '../api/endpoints'
import { useAuth } from '../auth/AuthContext'
import type { DiskUsage, OverviewHost, SubjectMetricType } from '../types/api'
import {
  changedKeys,
  customizeItem,
  hostProblems,
  hostRow,
  hostRuleState,
  hostSavePlan,
  hostSummary,
  inheritItem,
  keepFailed,
  revertItem,
  type HostRuleState,
  type RuleRowView,
  type SavePart,
} from './hostRuleRows'
import { DiskSelectionFields, StatusDurationField, StatusLevelField, ThresholdDurationField, ThresholdLevelFields } from './RuleEditors'
import { RuleRow, RuleSaveBar, RuleTopicLayout, SubjectChips, type TopicMenuItem } from './RuleTopics'
import { TOPICS, itemKey, ruleCount, topicHasChanges, topicInfo, topicItems, type TopicId, type TopicItem } from './ruleTopics'
import { validateRuleDraft } from './statusRules'
import { SubjectThresholdFields, type SubjectKind, type SubjectNote } from './SubjectThresholdFields'
import { diskIONames, serverLevels, temperatureNotes, validateDraft, type MountDrafts } from './thresholds'

interface Suggestions {
  mounts: string[]
  containers: string[]
  subjects: Record<SubjectMetricType, string[]>
  // Sensörlerin donanım sınırları ("Öneriyi kullan").
  sensorNotes: Record<string, SubjectNote>
}

const NO_SUGGESTIONS: Suggestions = { mounts: [], containers: [], subjects: { disk_latency: [], temperature: [], service_restart: [] }, sensorNotes: {} }

// Eşiğin konuya özel değerlerinin düzenleyici türü (mount, container ya da protokol 4 konusu).
function subjectKind(item: TopicItem): SubjectKind | null {
  if (item.kind !== 'threshold') return null
  if (item.metric === 'disk') return 'mount'
  if (item.metric === 'docker_restart') return 'container'
  if (item.metric === 'disk_latency' || item.metric === 'temperature' || item.metric === 'service_restart') return item.metric
  return null
}

// Bir türün konuya özel taslakları, önerileri ve onları taslağa yazan işlev.
function subjectParts(kind: SubjectKind, draft: HostRuleState, suggestions: Suggestions) {
  if (kind === 'mount') return { drafts: draft.mounts, suggestions: suggestions.mounts, apply: (next: MountDrafts): HostRuleState => ({ ...draft, mounts: next }) }
  if (kind === 'container') return { drafts: draft.containers, suggestions: suggestions.containers, apply: (next: MountDrafts): HostRuleState => ({ ...draft, containers: next }) }
  return {
    drafts: draft.subjects[kind],
    suggestions: suggestions.subjects[kind],
    apply: (next: MountDrafts): HostRuleState => ({ ...draft, subjects: { ...draft.subjects, [kind]: next } }),
  }
}

async function loadHost(hostId: string): Promise<{ state: HostRuleState; reported: DiskUsage[] }> {
  // Disk seçimi ve servisler okunamazsa satırları "bilinmiyor" der; eşikler ve durum kuralları zorunludur.
  const [thresholds, status, disks, services] = await Promise.all([
    hostsApi.thresholds(hostId),
    hostsApi.statusRules(hostId),
    hostsApi.diskAlerts(hostId).catch(() => null),
    hostsApi.services(hostId).catch(() => null),
  ])
  return { state: hostRuleState(thresholds, status, disks, services), reported: disks?.reported ?? [] }
}

// Alert kuralları sayfasının sunucu kapsamı: sunucunun bütün kuralları konuya göre. Eşikler, durum kuralları, disk seçimi
// ve izlenen servisler ayrı uçlardan okunur ve tek bir taslakta birleşir; değişiklikler konular arasında korunur ve alttaki
// tek çubukla birlikte kaydedilir. Sunucu değişince bileşen yeniden kurulur (key) ve kaydedilmemiş değişiklikler atılır.
export function HostRules({ hostId, host, topic, onTopic }: { hostId: string; host?: OverviewHost; topic: TopicId; onTopic: (id: TopicId) => void }) {
  const { can } = useAuth()
  const canEdit = can('threshold.edit')
  const canEditDisks = can('host.update')
  const [saved, setSaved] = useState<HostRuleState | null>(null)
  const [draft, setDraft] = useState<HostRuleState | null>(null)
  const [reported, setReported] = useState<DiskUsage[]>([])
  const [suggestions, setSuggestions] = useState<Suggestions>(NO_SUGGESTIONS)
  // Düzenleme alanı açık satırlar ve şeritler (öğe anahtarı; şeritte "şerit:" önekiyle).
  const [open, setOpen] = useState<Set<string>>(new Set())
  const [loadError, setLoadError] = useState<string | null>(null)
  const [saveError, setSaveError] = useState<string | null>(null)
  const [notice, setNotice] = useState(false)
  const [saving, setSaving] = useState(false)

  useEffect(() => {
    let cancelled = false
    loadHost(hostId)
      .then(({ state, reported }) => {
        if (cancelled) return
        setSaved(state)
        setDraft(state)
        setReported(reported)
        setSuggestions((s) => ({ ...s, mounts: [...new Set([...reported.map((r) => r.mount), ...(state.disks?.selected ?? [])])].sort() }))
      })
      .catch((err) => !cancelled && setLoadError(err instanceof Error ? err.message : 'kurallar yüklenemedi'))
    // Konuya özel değer eklerken sunulanlar: container'lar, G/Ç'si ölçülen diskler, sensörler (donanım sınırlarıyla) ve
    // izlenen servisler. Okunamazlarsa yalnızca öneri düğmeleri çıkmaz.
    const subject = (type: SubjectMetricType, names: string[]) => !cancelled && setSuggestions((s) => ({ ...s, subjects: { ...s.subjects, [type]: names } }))
    hostsApi
      .docker(hostId)
      .then((list) => !cancelled && setSuggestions((s) => ({ ...s, containers: [...new Set(list.map((c) => c.name))].sort() })))
      .catch(() => undefined)
    hostsApi
      .latestMetric(hostId)
      .then((p) => subject('disk_latency', diskIONames(p)))
      .catch(() => undefined)
    hostsApi
      .get(hostId)
      .then((h) => {
        const temps = h.system_state?.temperatures ?? []
        subject('temperature', [...new Set(temps.map((t) => t.sensor))].sort())
        if (!cancelled) setSuggestions((s) => ({ ...s, sensorNotes: temperatureNotes(temps) }))
      })
      .catch(() => undefined)
    hostsApi
      .services(hostId)
      .then((s) => subject('service_restart', [...s.watched].sort()))
      .catch(() => undefined)
    return () => {
      cancelled = true
    }
  }, [hostId])

  const changes = useMemo(() => (saved && draft ? changedKeys(saved, draft) : new Set<string>()), [saved, draft])
  const problems = useMemo(() => (draft ? hostProblems(draft) : []), [draft])

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

  if (loadError) return <div className="error-banner">{loadError}</div>
  if (!saved || !draft) return <div className="muted">Yükleniyor…</div>

  async function save() {
    if (!saved || !draft) return
    const plan = hostSavePlan(saved, draft)
    const failed = new Set<SavePart>()
    const errors: string[] = []
    const attempt = async (part: SavePart, label: string, run: () => Promise<unknown>) => {
      try {
        await run()
      } catch (err) {
        failed.add(part)
        errors.push(`${label}: ${err instanceof Error ? err.message : 'kaydedilemedi'}`)
      }
    }
    setSaving(true)
    setSaveError(null)
    // Sırayla: biri hata verirse diğerleri yine kaydedilir; kaydedilemeyen kısım taslakta kalır.
    const { thresholds, status, disks } = plan
    if (thresholds) await attempt('thresholds', 'Eşikler', () => hostsApi.setThresholds(hostId, thresholds.overrides, thresholds.parts))
    if (status) await attempt('status', 'Durum kuralları', () => hostsApi.setStatusRules(hostId, status))
    if (disks) await attempt('disks', 'Disk seçimi', () => hostsApi.setDiskAlerts(hostId, disks))
    try {
      const fresh = await loadHost(hostId)
      setSaved(fresh.state)
      setDraft(keepFailed(fresh.state, draft, failed))
      setReported(fresh.reported)
      if (failed.size === 0) setOpen(new Set())
      setNotice(failed.size === 0)
      setSaveError(errors.length > 0 ? errors.join(' · ') : null)
    } catch (err) {
      errors.push(`Kurallar yeniden okunamadı: ${err instanceof Error ? err.message : 'bilinmeyen hata'}`)
      setSaveError(errors.join(' · '))
    } finally {
      setSaving(false)
    }
  }

  const rowsOf = (id: TopicId) => topicItems(topicInfo(id), 'sunucu').map((item) => ({ item, row: hostRow(item, draft) }))
  const menu: TopicMenuItem[] = TOPICS.map((t) => {
    const items = topicItems(t, 'sunucu')
    const count = ruleCount(items, (item) => hostRow(item, draft).on)
    return { id: t.id, title: t.title, on: count.on, total: count.total, changed: topicHasChanges(items, changes) }
  })
  const summary = hostSummary(TOPICS.flatMap((t) => rowsOf(t.id).map((r) => r.row)))
  const info = topicInfo(topic)
  const changedTopics = TOPICS.filter((t) => topicHasChanges(t.items, changes)).map((t) => t.title)

  // Eşik ve durum satırlarının ortak düğmeleri: Geri al · Düzenle · Devral ya da Özelleştir.
  function ruleButtons(item: TopicItem, key: string, own: boolean, editing: boolean, changed: boolean) {
    if (!canEdit || !saved || !draft) return undefined
    return (
      <span className="row row-tight rule-buttons">
        {changed && (
          <button
            type="button"
            className="btn btn-sm btn-ghost"
            disabled={saving}
            onClick={() => {
              edit(revertItem(item, saved, draft))
              toggle(key, false)
            }}
          >
            Geri al
          </button>
        )}
        {own && !editing && (
          <button type="button" className="btn btn-sm" disabled={saving} onClick={() => toggle(key, true)}>
            Düzenle
          </button>
        )}
        {own ? (
          <button type="button" className="btn btn-sm" disabled={saving} onClick={() => edit(inheritItem(item, draft))}>
            Devral
          </button>
        ) : (
          <button
            type="button"
            className="btn btn-sm"
            disabled={saving}
            onClick={() => {
              edit(customizeItem(item, draft))
              toggle(key, true)
            }}
          >
            Özelleştir
          </button>
        )}
      </span>
    )
  }

  function renderRow({ item, row }: { item: TopicItem; row: RuleRowView }) {
    if (!saved || !draft) return null
    const key = itemKey(item)
    const changed = changes.has(key)
    const isOpen = open.has(key)

    if (item.kind === 'threshold') {
      const m = item.metric
      const d = draft.thresholds[m]
      const own = d.mode === 'custom'
      const setMetric = (next: typeof d) => edit({ ...draft, thresholds: { ...draft.thresholds, [m]: next } })
      const editing = canEdit && own && (isOpen || changed)
      const kind = subjectKind(item)
      const stripKey = `şerit:${key}`
      const parts = kind ? subjectParts(kind, draft, suggestions) : null
      return (
        <RuleRow
          key={key}
          row={row}
          changed={changed}
          editor={editing ? <ThresholdLevelFields metric={m} draft={d} onChange={setMetric} disabled={saving} /> : undefined}
          durationEditor={editing ? <ThresholdDurationField metric={m} draft={d} onChange={setMetric} disabled={saving} /> : undefined}
          error={editing ? validateDraft(m, d) : null}
          actions={ruleButtons(item, key, own, editing, changed)}
          extra={
            row.subjects &&
            kind &&
            parts &&
            (canEdit && open.has(stripKey) ? (
              <div className="rule-panel">
                <SubjectThresholdFields
                  kind={kind}
                  drafts={parts.drafts}
                  onChange={(next) => edit(parts.apply(next))}
                  suggestions={parts.suggestions}
                  notes={m === 'temperature' ? suggestions.sensorNotes : undefined}
                  baseLevels={serverLevels(m, d, draft.defaults)}
                  disabled={saving}
                />
                <button type="button" className="btn btn-sm" style={{ marginTop: 8 }} onClick={() => toggle(stripKey, false)}>
                  Kapat
                </button>
              </div>
            ) : (
              <SubjectChips
                label={row.subjects.label}
                items={row.subjects.items}
                action={
                  canEdit && (
                    <button type="button" className="btn btn-sm btn-ghost" disabled={saving} onClick={() => toggle(stripKey, true)}>
                      {row.subjects.items.length > 0 ? 'Düzenle' : 'Ekle'}
                    </button>
                  )
                }
              />
            ))
          }
        />
      )
    }

    if (item.kind === 'status') {
      const r = item.rule
      const d = draft.status[r]
      const own = d.level !== ''
      const setRule = (next: typeof d) => edit({ ...draft, status: { ...draft.status, [r]: next } })
      const editing = canEdit && own && (isOpen || changed)
      return (
        <RuleRow
          key={key}
          row={row}
          changed={changed}
          editor={editing ? <StatusLevelField rule={r} draft={d} onChange={setRule} disabled={saving} /> : undefined}
          durationEditor={editing ? <StatusDurationField rule={r} draft={d} onChange={setRule} disabled={saving} /> : undefined}
          error={editing ? validateRuleDraft(r, d) : null}
          actions={ruleButtons(item, key, own, editing, changed)}
        />
      )
    }

    if (item.kind === 'selection' && item.key === 'disk_mounts') {
      const disks = draft.disks
      const editable = canEditDisks && disks !== null
      return (
        <RuleRow
          key={key}
          row={row}
          changed={changed}
          actions={
            editable && (
              <span className="row row-tight rule-buttons">
                {changed && (
                  <button
                    type="button"
                    className="btn btn-sm btn-ghost"
                    disabled={saving}
                    onClick={() => {
                      edit(revertItem(item, saved, draft))
                      toggle(key, false)
                    }}
                  >
                    Geri al
                  </button>
                )}
                {!isOpen && (
                  <button type="button" className="btn btn-sm" disabled={saving} onClick={() => toggle(key, true)}>
                    Değiştir
                  </button>
                )}
              </span>
            )
          }
          extra={editable && isOpen ? <DiskSelectionFields hostId={hostId} state={disks} reported={reported} onChange={(next) => edit({ ...draft, disks: next })} disabled={saving} /> : undefined}
        />
      )
    }

    if (item.kind === 'selection') {
      return (
        <RuleRow
          key={key}
          row={row}
          actions={
            <Link className="btn btn-sm btn-ghost" to={`/hosts/${hostId}?sekme=servisler&tur=sistem`}>
              Servisler sekmesi →
            </Link>
          }
        />
      )
    }

    return <RuleRow key={key} row={row} />
  }

  return (
    <div className="stack-col">
      <div className="card rule-summary">
        <strong>
          {host && (
            <>
              <Link to={`/hosts/${hostId}`}>{host.title}</Link> için{' '}
            </>
          )}
          {summary.on} kural etkin; {summary.own} tanesi bu sunucuya özel.
        </strong>
        <span className="muted">
          Hiçbir kapsamda tanımlanmayan kural kapalıdır. “Devralındı”: değer sistemden ya da organizasyondan gelir; “Özelleştir” yalnızca bu
          sunucu için değer verir.
        </span>
        {notice && changes.size === 0 && (
          <span className="save-note" style={{ margin: '6px 0 0' }}>
            <Check size={14} strokeWidth={2.2} />
            Kurallar kaydedildi.
          </span>
        )}
      </div>
      <RuleTopicLayout menu={menu} active={topic} onSelect={onTopic} title={info.title} desc={info.desc}>
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
