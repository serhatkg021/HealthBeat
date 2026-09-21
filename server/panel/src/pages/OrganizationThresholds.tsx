import { useCallback, useEffect, useMemo, useState } from 'react'
import { Info } from 'lucide-react'
import { thresholdsApi } from '../api/endpoints'
import type { MetricType, Organization, ThresholdConfig } from '../types/api'
import { METRICS, defaultSource, validateDraft } from './thresholds'
import { parentMap } from './orgTree'

interface RowDraft {
  warning: string
  critical: string
}

const toDraft = (t?: ThresholdConfig): RowDraft => ({ warning: t ? String(t.warning_level) : '', critical: t ? String(t.critical_level) : '' })

// Bir organizasyonun varsayılan eşikleri. Değer tanımlanmadıysa üst şirketten (en yakın olandan), o da yoksa genel
// varsayılandan miras alınır; alt organizasyonlar ve sunucular da buradan miras alır. Sunucuya özel değer hepsini ezer.
export function OrganizationThresholds({ organization, orgs, canEdit }: { organization: Organization; orgs: Organization[]; canEdit: boolean }) {
  const [list, setList] = useState<ThresholdConfig[] | null>(null)
  const [drafts, setDrafts] = useState<Record<MetricType, RowDraft>>({ cpu: toDraft(), ram: toDraft(), disk: toDraft(), docker_restart: toDraft() })
  const [error, setError] = useState<string | null>(null)
  const [busy, setBusy] = useState<MetricType | null>(null)
  const parents = useMemo(() => parentMap(orgs), [orgs])
  const nameOf = (id?: string) => orgs.find((o) => o.id === id)?.name ?? 'üst şirket'

  const load = useCallback(() => {
    thresholdsApi
      .list()
      .then((all) => {
        setList(all)
        const own = (m: MetricType) => all.find((t) => t.metric_type === m && t.organization_id === organization.id)
        setDrafts({ cpu: toDraft(own('cpu')), ram: toDraft(own('ram')), disk: toDraft(own('disk')), docker_restart: toDraft(own('docker_restart')) })
      })
      .catch((err) => setError(err instanceof Error ? err.message : 'eşikler yüklenemedi'))
  }, [organization.id])

  useEffect(load, [load])

  async function save(type: MetricType, existing?: ThresholdConfig) {
    const draft = drafts[type]
    const problem = validateDraft(type, { mode: 'custom', ...draft })
    if (problem) {
      setError(`${METRICS.find((m) => m.type === type)!.label}: ${problem}`)
      return
    }
    setBusy(type)
    setError(null)
    try {
      const levels = { warning_level: Number(draft.warning), critical_level: Number(draft.critical) }
      if (existing) await thresholdsApi.update(existing.id, levels)
      else await thresholdsApi.create({ organization_id: organization.id, metric_type: type, ...levels })
      load()
    } catch (err) {
      setError(err instanceof Error ? err.message : 'eşik kaydedilemedi')
    } finally {
      setBusy(null)
    }
  }

  async function remove(existing: ThresholdConfig) {
    setBusy(existing.metric_type)
    setError(null)
    try {
      await thresholdsApi.remove(existing.id)
      load()
    } catch (err) {
      setError(err instanceof Error ? err.message : 'eşik silinemedi')
    } finally {
      setBusy(null)
    }
  }

  const rows = list ?? []
  return (
    <div className="page-readable">
      <div className="notice notice-info">
        <div className="notice-title">
          <Info size={16} strokeWidth={1.9} />
          Nasıl çalışır?
        </div>
        Burada tanımladığınız değer bu organizasyon ve altındaki tüm organizasyonların sunucuları için geçerlidir. Tanımlamazsanız değer
        en yakın üst şirketten, o da yoksa genel varsayılandan miras alınır. Bir sunucunun kendi değeri her zaman önceliklidir.
      </div>
      {error && <div className="error-banner">{error}</div>}
      <div className="card table-card">
        <table className="stack thresholds-table">
          <thead>
            <tr>
              <th>Metrik</th>
              <th>Uyarı</th>
              <th>Kritik</th>
              <th>Kaynak</th>
              {canEdit && <th className="actions" />}
            </tr>
          </thead>
          <tbody>
            {METRICS.map((m) => {
              const own = rows.find((t) => t.metric_type === m.type && t.organization_id === organization.id)
              const inherited = defaultSource(rows, organization.id, parents, m.type)
              const draft = drafts[m.type]
              const changed = own
                ? draft.warning !== String(own.warning_level) || draft.critical !== String(own.critical_level)
                : draft.warning.trim() !== '' || draft.critical.trim() !== ''
              // Kendi değeri yokken yer tutucu olarak miras alınan değer görünür.
              const fallback = inherited?.levels
              const source = own
                ? 'Bu organizasyon'
                : inherited
                  ? inherited.fromOrganizationId
                    ? `Miras: ${nameOf(inherited.fromOrganizationId)}`
                    : 'Miras: genel varsayılan'
                  : 'Tanımlı değil — alert üretilmez'
              return (
                <tr key={m.type}>
                  <td className="primary">{m.label}</td>
                  {canEdit ? (
                    <>
                      <td data-label="Uyarı">
                        <span className="level-field">
                          <input
                            aria-label={`${m.label} uyarı seviyesi`}
                            className="level-input"
                            inputMode="decimal"
                            value={draft.warning}
                            placeholder={fallback ? String(fallback.warning_level) : '—'}
                            onChange={(e) => setDrafts({ ...drafts, [m.type]: { ...draft, warning: e.target.value } })}
                          />
                          <span className="muted">{m.unit}</span>
                        </span>
                      </td>
                      <td data-label="Kritik">
                        <span className="level-field">
                          <input
                            aria-label={`${m.label} kritik seviyesi`}
                            className="level-input"
                            inputMode="decimal"
                            value={draft.critical}
                            placeholder={fallback ? String(fallback.critical_level) : '—'}
                            onChange={(e) => setDrafts({ ...drafts, [m.type]: { ...draft, critical: e.target.value } })}
                          />
                          <span className="muted">{m.unit}</span>
                        </span>
                      </td>
                    </>
                  ) : (
                    <>
                      <td className="tnum" data-label="Uyarı">{own ?? fallback ? `${(own?.warning_level ?? fallback!.warning_level)} ${m.unit}` : '—'}</td>
                      <td className="tnum" data-label="Kritik">{own ?? fallback ? `${(own?.critical_level ?? fallback!.critical_level)} ${m.unit}` : '—'}</td>
                    </>
                  )}
                  <td className="muted" data-label="Kaynak">{source}</td>
                  {canEdit && (
                    <td className="actions">
                      <button className="btn btn-sm btn-primary" onClick={() => save(m.type, own)} disabled={busy !== null || !changed}>
                        {own ? 'Kaydet' : 'Tanımla'}
                      </button>{' '}
                      {own && (
                        <button className="btn btn-sm btn-danger" onClick={() => remove(own)} disabled={busy !== null} title="Kaldırınca değer üst şirketten miras alınır">
                          Miras al
                        </button>
                      )}
                    </td>
                  )}
                </tr>
              )
            })}
          </tbody>
        </table>
      </div>
    </div>
  )
}
