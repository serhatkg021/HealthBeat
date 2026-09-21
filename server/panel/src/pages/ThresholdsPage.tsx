import { useEffect, useState } from 'react'
import { thresholdsApi } from '../api/endpoints'
import type { MetricType, ThresholdConfig } from '../types/api'
import { useAuth } from '../auth/AuthContext'
import { METRICS, validateDraft } from './thresholds'
import { PageHeader } from '../components/PageHeader'
import { Info } from 'lucide-react'

interface RowDraft {
  warning: string
  critical: string
}

const toDraft = (t?: ThresholdConfig): RowDraft => ({ warning: t ? String(t.warning_level) : '', critical: t ? String(t.critical_level) : '' })

// Varsayılan eşikler: metrik başına tam bir satır; kendi değeri olmayan her sunucu tarafından
// kullanılır. Bir sunucunun kendi değerleri onun sayfasında (ya da eklenirken) ayarlanır.
export function ThresholdsPage() {
  const { user } = useAuth()
  const canEdit = user?.role === 'super_admin' // global eşikler server'da yalnızca super_admin içindir
  const [defaults, setDefaults] = useState<Partial<Record<MetricType, ThresholdConfig>>>({})
  const [drafts, setDrafts] = useState<Record<MetricType, RowDraft>>({
    cpu: toDraft(),
    ram: toDraft(),
    disk: toDraft(),
    docker_restart: toDraft(),
  })
  const [error, setError] = useState<string | null>(null)
  const [busy, setBusy] = useState<MetricType | null>(null)
  const [confirmRemove, setConfirmRemove] = useState<MetricType | null>(null)

  function load() {
    thresholdsApi
      .list()
      .then((list) => {
        const next: Partial<Record<MetricType, ThresholdConfig>> = {}
        // Yalnızca global satırlar burada gösterilir; organizasyon varsayılanları organizasyonun sayfasında,
        // sunucuya özel eşikler sunucunun sayfasında yönetilir.
        for (const t of list) if (!t.organization_id) next[t.metric_type] = t
        setDefaults(next)
        setDrafts({
          cpu: toDraft(next.cpu),
          ram: toDraft(next.ram),
          disk: toDraft(next.disk),
          docker_restart: toDraft(next.docker_restart),
        })
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
      const levels = { warning_level: Number(draft.warning), critical_level: Number(draft.critical) }
      const existing = defaults[type]
      if (existing) await thresholdsApi.update(existing.id, levels)
      else await thresholdsApi.create({ metric_type: type, ...levels })
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
      <PageHeader
        title="Eşikler"
        subtitle="Kendi değeri olmayan tüm sunucuların kullandığı varsayılan alert eşikleri"
      />
      <div className="notice notice-info">
        <div className="notice-title">
          <Info size={16} strokeWidth={1.9} />
          Nasıl çalışır?
        </div>
        Bunlar genel varsayılanlardır: bir organizasyon kendi değerini tanımlamadıkça (organizasyon sayfası › Eşikler) ya da bir
        sunucu kendi değerini seçmedikçe (sunucu sayfası › Ayarlar › Eşikler) herkes bunu kullanır. Organizasyon değerleri alt
        organizasyonlara miras kalır. Varsayılanı olmayan bir metrik, özel değeri olmayan sunucularda alert üretmez.
      </div>
      {error && <div className="error-banner">{error}</div>}

      <div className="card table-card">
        <table className="stack thresholds-table">
          <thead>
            <tr>
              <th>Metrik</th>
              <th>Uyarı</th>
              <th>Kritik</th>
              {canEdit && <th className="actions" />}
            </tr>
          </thead>
          <tbody>
            {METRICS.map((m) => {
              const existing = defaults[m.type]
              const draft = drafts[m.type]
              const changed = existing
                ? draft.warning !== String(existing.warning_level) || draft.critical !== String(existing.critical_level)
                : draft.warning.trim() !== '' || draft.critical.trim() !== ''
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
                    </>
                  ) : (
                    <td colSpan={2} className="muted" data-label="Değer">
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
