import { useCallback, useEffect, useState, type FormEvent } from 'react'
import { Clock, Database, FileText, Globe, RotateCcw, Save, type LucideIcon } from 'lucide-react'
import { ApiError } from '../api/client'
import { settingsApi } from '../api/endpoints'
import { useAuth } from '../auth/AuthContext'
import { FieldLabel } from '../components/FieldLabel'
import { InfoTip } from '../components/InfoTip'
import { StatusBadge } from '../components/StatusBadge'
import { useTab } from '../components/useTab'
import type { SettingsField, SettingsResponse } from '../types/api'
import { SETTINGS_SECTIONS, buildPatch, defaultText, fieldMessage, toDisplay, type SectionDef } from './settingsForm'

const SECTION_PARAM = 'bolum'

const NAV: { id: string; label: string; icon: LucideIcon }[] = [
  { id: 'agent', label: 'Agent sürümleri', icon: Database },
  { id: 'saklama', label: 'Veri saklama', icon: Clock },
  { id: 'oturum', label: 'Oturum ve hız sınırları', icon: Clock },
  { id: 'panel', label: 'Panel adresi', icon: Globe },
  { id: 'log', label: 'Loglama', icon: FileText },
]

const message = (err: unknown, fallback: string) => (err instanceof Error ? err.message : fallback)
const when = (iso: string | null | undefined) => (iso ? new Date(iso).toLocaleString() : '—')

// Sistem ayarları: kurulum genelindeki, yeniden başlatmadan değişen ayarlar. Okumak settings.view, değiştirmek
// settings.manage ister (varsayılan olarak yalnızca süper admin).
export function SettingsPage() {
  const { can } = useAuth()
  const canEdit = can('settings.manage')
  const ids = NAV.map((n) => n.id)
  const [section, setSection] = useTab(ids, ids[0], SECTION_PARAM)
  const [settings, setSettings] = useState<SettingsResponse | null>(null)
  const [error, setError] = useState<string | null>(null)

  useEffect(() => {
    settingsApi
      .get()
      .then(setSettings)
      .catch((err) => setError(message(err, 'ayarlar yüklenemedi')))
  }, [])

  return (
    <div>
      {error && <div className="error-banner">{error}</div>}
      <div className="settings-layout">
        <nav className="settings-nav" aria-label="Ayar bölümleri">
          {NAV.map((n) => (
            <button
              key={n.id}
              type="button"
              className={`settings-nav-item${n.id === section ? ' active' : ''}`}
              aria-current={n.id === section ? 'page' : undefined}
              onClick={() => setSection(n.id)}
            >
              <n.icon size={15} strokeWidth={1.75} />
              {n.label}
            </button>
          ))}
        </nav>
        <div className="settings-content">
          {settings &&
            SETTINGS_SECTIONS.map((s) => (
              <div key={s.id} id={`settings-${s.id}`} hidden={section !== s.id} className="settings-panel">
                <GeneralSection section={s} data={settings} canEdit={canEdit} onSaved={setSettings} />
              </div>
            ))}
        </div>
      </div>
    </div>
  )
}

// ---------------------------------------------------------------- genel ayarlar

function GeneralSection({
  section,
  data,
  canEdit,
  onSaved,
}: {
  section: SectionDef
  data: SettingsResponse
  canEdit: boolean
  onSaved: (s: SettingsResponse) => void
}) {
  const initial = useCallback(
    () => Object.fromEntries(section.fields.map((f) => [f.field, toDisplay(f, data.values[f.field])])) as Partial<Record<SettingsField, string>>,
    [section, data],
  )
  const [draft, setDraft] = useState(initial)
  const [errors, setErrors] = useState<Partial<Record<string, string>>>({})
  const [error, setError] = useState<string | null>(null)
  const [saved, setSaved] = useState(false)
  const [busy, setBusy] = useState(false)

  // Kaydedilen (server'ın normalleştirdiği) değerler gelince form onları gösterir.
  const [shown, setShown] = useState(data)
  if (shown !== data) {
    setShown(data)
    setDraft(initial())
  }

  async function run(action: () => Promise<SettingsResponse>) {
    setBusy(true)
    setError(null)
    setSaved(false)
    try {
      onSaved(await action())
      setErrors({})
      setSaved(true)
    } catch (err) {
      if (err instanceof ApiError && err.fields) {
        const shownErrors: Record<string, string> = {}
        for (const [f, m] of Object.entries(err.fields)) shownErrors[f] = fieldMessage(section.fields.find((d) => d.field === f), m)
        setErrors(shownErrors)
        const first = Object.values(shownErrors)[0]
        setError(first ?? message(err, 'ayarlar kaydedilemedi'))
      } else {
        setError(message(err, 'ayarlar kaydedilemedi'))
      }
    } finally {
      setBusy(false)
    }
  }

  function handleSave(e: FormEvent) {
    e.preventDefault()
    const { patch, errors: bad } = buildPatch(section, data.values, draft)
    if (Object.keys(bad).length > 0) {
      setErrors(bad)
      return
    }
    if (Object.keys(patch).length === 0) {
      setSaved(true)
      return
    }
    void run(() => settingsApi.update(patch))
  }

  return (
    <form className="card form-card settings-card" onSubmit={handleSave}>
      <h2 className="card-title">
        {section.title}
        <InfoTip label={section.title}>{section.description}</InfoTip>
      </h2>
      {error && <div className="error-banner">{error}</div>}
      {section.fields.map((f) => {
        const changed = data.changed.includes(f.field)
        const id = `setting-${f.field}`
        return (
          <div className="form-row" key={f.field}>
            <FieldLabel
              htmlFor={id}
              label={f.label}
              hint={
                <>
                  {f.hint} <span className="muted">({defaultText(f, data.defaults[f.field])})</span>
                </>
              }
            >
              {changed && <StatusBadge tone="neutral">değiştirildi</StatusBadge>}
              {canEdit && changed && (
                <button type="button" className="link-btn" disabled={busy} onClick={() => run(() => settingsApi.reset([f.field]))}>
                  <RotateCcw size={12} strokeWidth={2} />
                  Varsayılana dön
                </button>
              )}
            </FieldLabel>
            <div className="input-with-unit">
              {f.kind === 'select' ? (
                <select id={id} value={draft[f.field] ?? ''} disabled={!canEdit} onChange={(e) => setDraft({ ...draft, [f.field]: e.target.value })}>
                  {f.options?.map((o) => (
                    <option key={o.value} value={o.value}>
                      {o.label}
                    </option>
                  ))}
                </select>
              ) : (
                <input
                  id={id}
                  value={draft[f.field] ?? ''}
                  disabled={!canEdit}
                  inputMode={f.kind === 'number' ? 'decimal' : undefined}
                  placeholder={f.placeholder}
                  onChange={(e) => setDraft({ ...draft, [f.field]: e.target.value })}
                />
              )}
              {f.unit && <span className="muted">{f.unit}</span>}
            </div>
            {errors[f.field] && <p className="field-error flush">{errors[f.field]}</p>}
          </div>
        )
      })}
      {canEdit && (
        <div className="form-actions">
          <button className="btn btn-primary" type="submit" disabled={busy}>
            <Save size={15} strokeWidth={1.9} />
            Kaydet
          </button>
          {saved && <span className="muted save-note">Kaydedildi.</span>}
        </div>
      )}
      {data.updated_by && <p className="form-hint">Son değişiklik: {data.updated_by.name}, {when(data.updated_at)}</p>}
    </form>
  )
}
