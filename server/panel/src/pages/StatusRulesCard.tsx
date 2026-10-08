import { useCallback, useEffect, useMemo, useState } from 'react'
import { Check, ListChecks, Save } from 'lucide-react'
import { hostsApi, statusRulesApi } from '../api/endpoints'
import type { Organization, StatusRule, StatusRuleSetting } from '../types/api'
import { DurationField } from './DurationField'
import { durationSeconds, durationText } from './duration'
import { parentMap } from './orgTree'
import {
  STATUS_RULES,
  hostDrafts,
  inheritedForOrg,
  isRulesDirty,
  ruleChanges,
  scopeDrafts,
  settingText,
  validateRuleDraft,
  validateRuleDrafts,
  type RuleChoice,
  type RuleDrafts,
} from './statusRules'

export type StatusRuleScope = { kind: 'system' } | { kind: 'org'; organization: Organization; orgs: Organization[] } | { kind: 'host'; hostId: string }

// Devralınan değer ve nereden geldiği (organizasyon ve sunucu kapsamında).
interface Inherited {
  setting: StatusRuleSetting | null
  source: string
}

const LEVELS: { value: RuleChoice; label: string }[] = [
  { value: 'off', label: 'Kapalı' },
  { value: 'info', label: 'Bilgi' },
  { value: 'warning', label: 'Uyarı' },
  { value: 'critical', label: 'Kritik' },
]

// Durum kuralları: eşiği olmayan alert'lerin seviyesi ve süresi, kapsam başına bir tablo. Genel kapsamda tanımlanmayan
// kural kapalıdır; organizasyon ve sunucu kapsamında "Devral" üst kapsamın değerini izler, "Kapalı" onu bu kapsamda kapatır.
export function StatusRulesCard({ scope, canEdit }: { scope: StatusRuleScope; canEdit: boolean }) {
  const [saved, setSaved] = useState<RuleDrafts | null>(null)
  const [draft, setDraft] = useState<RuleDrafts | null>(null)
  const [inherited, setInherited] = useState<Partial<Record<StatusRule, Inherited>>>({})
  const [error, setError] = useState<string | null>(null)
  const [notice, setNotice] = useState<string | null>(null)
  const [saving, setSaving] = useState(false)

  const orgs = scope.kind === 'org' ? scope.orgs : undefined
  const parents = useMemo(() => parentMap(orgs ?? []), [orgs])
  const orgName = useCallback((id?: string) => orgs?.find((o) => o.id === id)?.name ?? 'üst şirket', [orgs])
  const key = scope.kind === 'system' ? 'system' : scope.kind === 'org' ? `org:${scope.organization.id}` : `host:${scope.hostId}`

  const load = useCallback(async () => {
    if (scope.kind === 'host') {
      const views = await hostsApi.statusRules(scope.hostId)
      const inh: Partial<Record<StatusRule, Inherited>> = {}
      // Server sunucu için değerin hangi kapsamdan geldiğini söylemez; ayrıntı organizasyon kapsamında görünür.
      for (const v of views) inh[v.rule] = { setting: v.default, source: '' }
      return { drafts: hostDrafts(views), inherited: inh }
    }
    const list = await statusRulesApi.list()
    if (scope.kind === 'system') {
      // Genel kapsamda "kapalı" ile "tanımsız" aynı şeydir: ikisi de tanımsız gösterilir.
      const drafts = scopeDrafts(list)
      for (const r of STATUS_RULES) if (drafts[r.rule].level === 'off') drafts[r.rule] = { ...drafts[r.rule], level: '' }
      return { drafts, inherited: {} }
    }
    const inh: Partial<Record<StatusRule, Inherited>> = {}
    for (const r of STATUS_RULES) {
      const found = inheritedForOrg(list, scope.organization.id, parents, r.rule)
      inh[r.rule] = found
        ? { setting: found.setting, source: found.fromOrganizationId ? orgName(found.fromOrganizationId) : 'genel' }
        : { setting: null, source: '' }
    }
    return { drafts: scopeDrafts(list, scope.organization.id), inherited: inh }
    // key kapsamı tek başına tanımlar; scope nesnesi her çizimde yeniden oluşabilir.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [key, parents, orgName])

  useEffect(() => {
    let cancelled = false
    setSaved(null)
    setDraft(null)
    setError(null)
    load()
      .then((res) => {
        if (cancelled) return
        setSaved(res.drafts)
        setDraft(res.drafts)
        setInherited(res.inherited)
      })
      .catch((err) => !cancelled && setError(err instanceof Error ? err.message : 'durum kuralları yüklenemedi'))
    return () => {
      cancelled = true
    }
  }, [load])

  async function save() {
    if (!saved || !draft) return
    setSaving(true)
    setError(null)
    setNotice(null)
    try {
      const changes = ruleChanges(saved, draft)
      if (scope.kind === 'host') await hostsApi.setStatusRules(scope.hostId, changes)
      else await statusRulesApi.set(scope.kind === 'org' ? scope.organization.id : null, changes)
      const res = await load()
      setSaved(res.drafts)
      setDraft(res.drafts)
      setInherited(res.inherited)
      setNotice('Durum kuralları kaydedildi.')
    } catch (err) {
      setError(err instanceof Error ? err.message : 'durum kuralları kaydedilemedi')
    } finally {
      setSaving(false)
    }
  }

  const system = scope.kind === 'system'
  const dirty = saved && draft ? isRulesDirty(saved, draft) : false
  const problems = draft ? validateRuleDrafts(draft) : []
  const inheritText = (rule: StatusRule) => {
    const inh = inherited[rule]
    return inh?.setting ? settingText(rule, inh.setting) : 'kapalı'
  }

  return (
    <div className="card table-card status-rules-card">
      <div className="card-title-row">
        <h2 className="card-title">
          <ListChecks size={16} strokeWidth={1.75} />
          Durum kuralları
        </h2>
      </div>
      <p className="card-desc">
        Eşiği olmayan, bir durumu bildiren alert’ler. Seviye ve süre burada seçilir; hiçbir kapsamda tanımlanmayan kural
        kapalıdır.
        {system
          ? ' Burada tanımlanan değeri, kendi değeri olmayan her organizasyon ve sunucu kullanır.'
          : ' “Devral” üst kapsamın değerini izler; “Kapalı”, üst kapsamda açık bir kuralı bu kapsamda kapatır.'}
      </p>
      {error && <div className="error-banner">{error}</div>}
      {notice && !dirty && (
        <div className="save-note">
          <Check size={14} strokeWidth={2.2} />
          {notice}
        </div>
      )}
      {!draft && !error && <div className="muted">Yükleniyor…</div>}
      {draft && (
        <table className="stack thresholds-table status-rules-table">
          <thead>
            <tr>
              <th>Kural</th>
              <th>Seviye</th>
              <th>Süre</th>
              {!system && <th>Kaynak</th>}
            </tr>
          </thead>
          <tbody>
            {STATUS_RULES.map((r) => {
              const d = draft[r.rule]
              const on = d.level !== '' && d.level !== 'off'
              const err = validateRuleDraft(r.rule, d)
              const set = (patch: Partial<typeof d>) => {
                setNotice(null)
                setDraft({ ...draft, [r.rule]: { ...d, ...patch } })
              }
              const inh = inherited[r.rule]
              return (
                <tr key={r.rule}>
                  <td className="primary">
                    <div>
                      {r.label}
                      <div className="form-hint metric-hint">{r.hint}</div>
                    </div>
                  </td>
                  <td data-label="Seviye">
                    {canEdit ? (
                      <select aria-label={`${r.label} seviyesi`} value={d.level} disabled={saving} onChange={(e) => set({ level: e.target.value as RuleChoice })}>
                        <option value="">{system ? 'Kapalı' : `Devral (${inheritText(r.rule)})`}</option>
                        {LEVELS.filter((l) => !system || l.value !== 'off').map((l) => (
                          <option key={l.value} value={l.value}>
                            {l.label}
                          </option>
                        ))}
                      </select>
                    ) : (
                      <span>{d.level === '' ? (system ? 'Kapalı' : `Devral (${inheritText(r.rule)})`) : settingText(r.rule, { level: d.level })}</span>
                    )}
                  </td>
                  <td data-label="Süre">
                    {!r.duration ? (
                      <span className="muted">anlık</span>
                    ) : on && canEdit ? (
                      <div>
                        <DurationField label={`${r.label} süresi`} value={d.duration} disabled={saving} onChange={(duration) => set({ duration })} />
                        <div className="form-hint metric-hint">{r.duration}</div>
                        {err && <div className="field-error flush">{err}</div>}
                      </div>
                    ) : on ? (
                      <span className="tnum">{durationText(true, durationSeconds(d.duration))}</span>
                    ) : (
                      <span className="muted">—</span>
                    )}
                  </td>
                  {!system && (
                    <td className="muted" data-label="Kaynak">
                      {d.level !== '' ? 'Bu kapsam' : inh?.setting ? (inh.source ? `Devralındı: ${inh.source}` : 'Devralındı') : 'Tanımlı değil'}
                    </td>
                  )}
                </tr>
              )
            })}
          </tbody>
        </table>
      )}
      {canEdit && draft && (
        <div className="row status-rules-actions">
          <button className="btn btn-primary" onClick={save} disabled={!dirty || saving || problems.length > 0}>
            <Save size={15} strokeWidth={1.9} />
            {saving ? 'Kaydediliyor…' : 'Kaydet'}
          </button>
          {dirty && (
            <button className="btn" onClick={() => setDraft(saved)} disabled={saving}>
              Vazgeç
            </button>
          )}
          {dirty && problems.length > 0 && <span className="muted">Düzeltilmesi gereken değer var.</span>}
        </div>
      )}
    </div>
  )
}
