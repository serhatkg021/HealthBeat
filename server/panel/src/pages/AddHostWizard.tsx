import { useEffect, useState } from 'react'
import { hostsApi, organizationsApi, thresholdsApi } from '../api/endpoints'
import { parentMap } from './orgTree'
import type { Host, HostMode } from '../types/api'
import { ThresholdFields } from './ThresholdFields'
import {
  buildCreateRequest,
  emptyWizardDraft,
  selectedMounts,
  STEPS,
  summarize,
  validateAll,
  validateStep,
  type DiskMode,
  type StepId,
  type WizardDraft,
} from './hostWizard'
import { effectiveDefaults, type Defaults } from './thresholds'
import { ArrowLeft, ArrowRight, Check, Pencil } from 'lucide-react'

// Bir sunucuyu adım adım ekler — sunucu, diskler, eşikler — ve aynı başlıklar altında
// gruplanmış bir özetle biter. Özetteki "Kaydet"e kadar hiçbir şey kaydedilmez; bir hatayı
// düzeltmek için oradan herhangi bir bölüm yeniden açılabilir.
export function AddHostWizard({
  organizationId,
  onCreated,
  onCancel,
}: {
  organizationId: string
  onCreated: (host: Host) => void
  onCancel: () => void
}) {
  const [draft, setDraft] = useState<WizardDraft>(emptyWizardDraft)
  const [stepIndex, setStepIndex] = useState(0)
  const [returnToSummary, setReturnToSummary] = useState(false) // özetten düzeltmek için bir adım açıldı
  const [stepErrors, setStepErrors] = useState<string[]>([])
  const [defaults, setDefaults] = useState<Defaults>({})
  const [saving, setSaving] = useState(false)
  const [saveError, setSaveError] = useState<string | null>(null)

  useEffect(() => {
    // Varsayılan, üst şirket zincirinden miras alınabilir: zincir için organizasyon listesi de gerekir.
    Promise.all([thresholdsApi.list(), organizationsApi.list().catch(() => [])])
      .then(([list, orgs]) => setDefaults(effectiveDefaults(list, organizationId, parentMap(orgs))))
      .catch(() => undefined) // sihirbaz yine çalışır; "Varsayılan" o zaman değer göstermez
  }, [organizationId])

  const step = STEPS[stepIndex]
  const summaryIndex = STEPS.length - 1
  const update = (patch: Partial<WizardDraft>) => {
    setStepErrors([])
    setDraft((d) => ({ ...d, ...patch }))
  }

  function goTo(index: number) {
    setStepErrors([])
    setSaveError(null)
    setStepIndex(index)
  }

  function next() {
    const errors = validateStep(step.id, draft)
    if (errors.length > 0) {
      setStepErrors(errors)
      return
    }
    if (returnToSummary) {
      setReturnToSummary(false)
      goTo(summaryIndex)
    } else {
      goTo(stepIndex + 1)
    }
  }

  function edit(id: StepId) {
    setReturnToSummary(true)
    goTo(STEPS.findIndex((s) => s.id === id))
  }

  const problems = validateAll(draft)
  const hasProblems = Object.keys(problems).length > 0

  async function save() {
    if (hasProblems) return
    setSaving(true)
    setSaveError(null)
    try {
      onCreated(await hostsApi.create(buildCreateRequest(organizationId, draft)))
    } catch (err) {
      setSaveError(err instanceof Error ? err.message : 'sunucu eklenemedi')
      setSaving(false)
    }
  }

  return (
    <div className="card wizard-card">
      <ol className="stepper" aria-label="Adımlar">
        {STEPS.map((s, i) => (
          <li key={s.id} className={i === stepIndex ? 'active' : i < stepIndex ? 'done' : undefined} aria-current={i === stepIndex ? 'step' : undefined}>
            <span className="step-no">{i < stepIndex ? <Check size={12} strokeWidth={3} /> : i + 1}</span>
            {s.title}
          </li>
        ))}
      </ol>

      {step.id === 'server' && <ServerStep draft={draft} update={update} />}
      {step.id === 'disks' && <DisksStep draft={draft} update={update} />}
      {step.id === 'thresholds' && (
        <div>
          <p className="form-hint" style={{ margin: '0 0 14px' }}>
            Varsayılan değerler seçili gelir; bir metriği yalnızca bu sunucu için değiştirmek istiyorsanız “Özel değer”i
            seçin. Varsayılanlar Eşikler sayfasında yönetilir.
          </p>
          <ThresholdFields
            idPrefix="wizard"
            drafts={draft.thresholds}
            defaults={defaults}
            onChange={(thresholds) => update({ thresholds })}
            mounts={{
              drafts: draft.mountThresholds,
              onChange: (mountThresholds) => update({ mountThresholds }),
              suggestions: selectedMounts(draft),
            }}
            containers={{
              drafts: draft.containerThresholds,
              onChange: (containerThresholds) => update({ containerThresholds }),
              suggestions: [], // sunucu henüz rapor vermediği için container'lar bilinmiyor; adı yazılır
            }}
          />
        </div>
      )}
      {step.id === 'summary' && (
        <div>
          <p className="form-hint" style={{ margin: '0 0 14px' }}>
            Kaydetmeden önce kontrol edin. Bir yanlışlık görürseniz ilgili başlığın “Düzenle” düğmesiyle o adıma dönün.
          </p>
          {summarize(draft, defaults).map((section) => {
            const errors = problems[section.step]
            return (
              <section key={section.step} className={`summary-section${errors ? ' invalid' : ''}`}>
                <div className="summary-head">
                  <h3>{section.title}</h3>
                  <button className="btn btn-sm" onClick={() => edit(section.step)} disabled={saving}>
                    <Pencil size={13} strokeWidth={1.9} />
                    Düzenle
                  </button>
                </div>
                <ul>
                  {section.lines.map((line) => (
                    <li key={line}>{line}</li>
                  ))}
                </ul>
                {errors && (
                  <div className="field-error flush">
                    {errors.map((e) => (
                      <div key={e}>{e}</div>
                    ))}
                  </div>
                )}
              </section>
            )
          })}
          {saveError && <div className="error-banner">{saveError}</div>}
        </div>
      )}

      {stepErrors.length > 0 && (
        <div className="error-banner" role="alert" style={{ marginTop: 12, marginBottom: 0 }}>
          {stepErrors.map((e) => (
            <div key={e}>{e}</div>
          ))}
        </div>
      )}

      <div className="wizard-actions">
        <button className="btn" onClick={onCancel} disabled={saving}>
          Vazgeç
        </button>
        <span className="spacer" />
        {stepIndex > 0 && !returnToSummary && (
          <button className="btn" onClick={() => goTo(stepIndex - 1)} disabled={saving}>
            <ArrowLeft size={15} strokeWidth={1.9} />
            Geri
          </button>
        )}
        {step.id !== 'summary' && (
          <button className="btn btn-primary" onClick={next}>
            {returnToSummary ? 'Özete dön' : 'İleri'}
            <ArrowRight size={15} strokeWidth={1.9} />
          </button>
        )}
        {step.id === 'summary' && (
          <button className="btn btn-primary" onClick={save} disabled={saving || hasProblems}>
            <Check size={15} strokeWidth={2} />
            {saving ? 'Kaydediliyor…' : 'Kaydet'}
          </button>
        )}
      </div>
    </div>
  )
}

function ServerStep({ draft, update }: { draft: WizardDraft; update: (patch: Partial<WizardDraft>) => void }) {
  return (
    <div>
      <div className="form-row">
        <label htmlFor="w-title">Sunucu adı</label>
        <input id="w-title" value={draft.title} onChange={(e) => update({ title: e.target.value })} autoFocus />
      </div>
      <div className="form-row">
        <label htmlFor="w-ip">IP</label>
        <input id="w-ip" value={draft.ip} onChange={(e) => update({ ip: e.target.value })} />
      </div>
      <div className="form-row">
        <label htmlFor="w-mode">Mod</label>
        <select id="w-mode" value={draft.mode} onChange={(e) => update({ mode: e.target.value as HostMode })}>
          <option value="push">push</option>
          <option value="pull">pull</option>
        </select>
      </div>
      <div className="form-row">
        <label htmlFor="w-interval">Aralık (saniye)</label>
        <input id="w-interval" inputMode="numeric" value={draft.intervalSeconds} onChange={(e) => update({ intervalSeconds: e.target.value })} />
      </div>
      {draft.mode === 'pull' && (
        <>
          <div className="form-row">
            <label htmlFor="w-pull-port">Pull port</label>
            <input id="w-pull-port" inputMode="numeric" value={draft.pullPort} onChange={(e) => update({ pullPort: e.target.value })} />
          </div>
          <div className="form-row">
            <label htmlFor="w-pull-endpoint">Pull endpoint</label>
            <input id="w-pull-endpoint" value={draft.pullEndpoint} onChange={(e) => update({ pullEndpoint: e.target.value })} />
          </div>
        </>
      )}
    </div>
  )
}

function DisksStep({ draft, update }: { draft: WizardDraft; update: (patch: Partial<WizardDraft>) => void }) {
  const setMode = (diskMode: DiskMode) => update({ diskMode })
  return (
    <div>
      <p className="form-hint" style={{ margin: '0 0 14px' }}>
        Sunucu henüz rapor vermediği için diskler yolla girilir. Sonradan sunucu sayfasında, raporlanan disklerin
        arasından seçim yapabilirsiniz.
      </p>
      <label className="radio-row">
        <input type="radio" name="w-disk-mode" checked={draft.diskMode === 'all'} onChange={() => setMode('all')} />
        <span>Raporlanan tüm diskler için alert üret</span>
      </label>
      <label className="radio-row">
        <input type="radio" name="w-disk-mode" checked={draft.diskMode === 'selected'} onChange={() => setMode('selected')} />
        <span>Yalnızca seçtiğim diskler için alert üret</span>
      </label>
      {draft.diskMode === 'selected' && (
        <div className="form-row indent" style={{ marginTop: 8 }}>
          <label htmlFor="w-disk-mounts">Disk yolları (virgül ya da satır ile ayırın)</label>
          <input
            id="w-disk-mounts"
            placeholder="/, /data"
            value={draft.diskMounts}
            onChange={(e) => update({ diskMounts: e.target.value })}
          />
          <span className="form-hint">
            Boş bırakırsanız bu sunucu için disk alert’i kapalı olur.
          </span>
        </div>
      )}
    </div>
  )
}
