import { useEffect, useState } from 'react'
import { thresholdsApi } from '../api/endpoints'
import type { MetricType, ThresholdConfig } from '../types/api'
import { useAuth } from '../auth/AuthContext'
import { DURATION_HINT, METRICS, perMetric, rowChanged, rowDraft, rowPayload, validateDraft, type RowDraft } from './thresholds'
import { DurationField } from './DurationField'
import { durationText } from './duration'
import { Info } from 'lucide-react'

// Alert kuralları sayfasının "Sistem varsayılanı" kapsamı: metrik başına tam bir satır; kendi değeri olmayan her
// organizasyon ve sunucu tarafından kullanılır.
export function SystemThresholds() {
  const { user, can } = useAuth()
  // İzin yetmez, kapsam da gerekir: org_admin'in threshold.edit'i yalnızca kendi organizasyonları içindir; genel
  // (organizasyonsuz) eşikleri server yalnızca super_admin'e yazdırır.
  const canEdit = can('threshold.edit') && user?.role === 'super_admin'
  const [defaults, setDefaults] = useState<Partial<Record<MetricType, ThresholdConfig>>>({})
  const [drafts, setDrafts] = useState<Record<MetricType, RowDraft>>(() => perMetric(() => rowDraft()))
  const [error, setError] = useState<string | null>(null)
  const [busy, setBusy] = useState<MetricType | null>(null)
  const [confirmRemove, setConfirmRemove] = useState<MetricType | null>(null)

  function load() {
    thresholdsApi
      .list()
      .then((list) => {
        const next: Partial<Record<MetricType, ThresholdConfig>> = {}
        // Yalnızca global satırlar burada gösterilir; organizasyon varsayılanları ve sunucuya özel eşikler aynı sayfanın
        // kendi kapsamlarında yönetilir.
        for (const t of list) if (!t.organization_id) next[t.metric_type] = t
        setDefaults(next)
        setDrafts(perMetric((m) => rowDraft(next[m])))
      })
      .catch((err) => setError(err instanceof Error ? err.message : 'eşikler yüklenemedi'))
  }

  useEffect(load, [])

  async function save(type: MetricType) {
    const draft = drafts[type]
    const problem = validateDraft(type, { mode: 'custom', ...draft })
    if (problem) {
      setError(`${METRICS.find((m) => m.type === type)!.label}: ${problem}`)
      return
    }
    setBusy(type)
    setError(null)
    try {
      const body = rowPayload(type, draft)
      const existing = defaults[type]
      if (existing) await thresholdsApi.update(existing.id, body)
      else await thresholdsApi.create({ metric_type: type, ...body, duration_seconds: body.duration_seconds ?? undefined })
      load()
    } catch (err) {
      setError(err instanceof Error ? err.message : 'eşik kaydedilemedi')
    } finally {
      setBusy(null)
    }
  }

  async function remove(type: MetricType) {
    const existing = defaults[type]
    if (!existing) return
    setBusy(type)
    setError(null)
    try {
      await thresholdsApi.remove(existing.id)
      setConfirmRemove(null)
      load()
    } catch (err) {
      setError(err instanceof Error ? err.message : 'eşik silinemedi')
    } finally {
      setBusy(null)
    }
  }

  return (
    <div className="page-readable">
      <div className="notice notice-info">
        <div className="notice-title">
          <Info size={16} strokeWidth={1.9} />
          Nasıl çalışır?
        </div>
        Bunlar genel varsayılanlardır: bir organizasyon kendi değerini tanımlamadıkça
        {can('organization.view') && ' (Kapsam › Organizasyon)'} ya da bir sunucu kendi değerini seçmedikçe (Kapsam › Sunucu)
        herkes bunu kullanır. Organizasyon değerleri alt organizasyonlara miras
        kalır. Varsayılanı olmayan bir metrik, özel değeri olmayan sunucularda alert üretmez.
        <p className="notice-more">
          <strong>Süre</strong> (disk gecikmesi, sıcaklık, servis yeniden başlatma, saat farkı): {DURATION_HINT}
        </p>
      </div>
      {error && <div className="error-banner">{error}</div>}

      <div className="card table-card">
        <table className="stack thresholds-table">
          <thead>
            <tr>
              <th>Metrik</th>
              <th>Uyarı</th>
              <th>Kritik</th>
              <th>Süre</th>
              {canEdit && <th className="actions" />}
            </tr>
          </thead>
          <tbody>
            {METRICS.map((m) => {
              const existing = defaults[m.type]
              const draft = drafts[m.type]
              const changed = rowChanged(m.type, draft, existing)
              return (
                <tr key={m.type}>
                  <td className="primary">
                    <div>
                      {m.label}
                      {m.hint && <div className="form-hint metric-hint">{m.hint}</div>}
                    </div>
                  </td>
                  {canEdit ? (
                    <>
                      <td data-label="Uyarı">
                        <span className="level-field">
                          <input
                            aria-label={`${m.label} uyarı seviyesi`}
                            className="level-input"
                            inputMode="decimal"
                            value={draft.warning}
                            placeholder="—"
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
                            placeholder="—"
                            onChange={(e) => setDrafts({ ...drafts, [m.type]: { ...draft, critical: e.target.value } })}
                          />
                          <span className="muted">{m.unit}</span>
                        </span>
                      </td>
                      <td data-label="Süre">
                        {m.duration ? (
                          <DurationField
                            label={`${m.label} süresi`}
                            value={draft.duration}
                            onChange={(duration) => setDrafts({ ...drafts, [m.type]: { ...draft, duration } })}
                          />
                        ) : (
                          <span className="muted">anlık</span>
                        )}
                      </td>
                      <td className="actions">
                        <button className="btn btn-sm btn-primary" onClick={() => save(m.type)} disabled={busy !== null || !changed}>
                          {existing ? 'Kaydet' : 'Tanımla'}
                        </button>{' '}
                        {existing &&
                          (confirmRemove === m.type ? (
                            <>
                              <span className="muted">Emin misiniz?</span>{' '}
                              <button className="btn btn-sm btn-danger" onClick={() => remove(m.type)} disabled={busy !== null}>
                                Evet, kaldır
                              </button>{' '}
                              <button className="btn btn-sm" onClick={() => setConfirmRemove(null)}>
                                Vazgeç
                              </button>
                            </>
                          ) : (
                            <button className="btn btn-sm btn-danger" onClick={() => setConfirmRemove(m.type)} disabled={busy !== null}>
                              Kaldır
                            </button>
                          ))}
                      </td>
                    </>
                  ) : existing ? (
                    <>
                      <td className="tnum" data-label="Uyarı">
                        {existing.warning_level} {m.unit}
                      </td>
                      <td className="tnum" data-label="Kritik">
                        {existing.critical_level} {m.unit}
                      </td>
                      <td className={existing.duration_seconds ? 'tnum' : 'muted'} data-label="Süre">
                        {durationText(m.duration, existing.duration_seconds)}
                      </td>
                    </>
                  ) : (
                    <td colSpan={3} className="muted" data-label="Değer">
                      Tanımlı değil — alert üretilmez
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
