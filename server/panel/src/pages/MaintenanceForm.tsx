import { useEffect, useMemo, useState, type FormEvent } from 'react'
import { Save } from 'lucide-react'
import { ApiError } from '../api/client'
import { maintenanceApi } from '../api/endpoints'
import { FieldLabel } from '../components/FieldLabel'
import type { ServerClock } from '../components/serverClock'
import type { MaintenanceOccurrence, MaintenanceWindow, Organization, OverviewHost } from '../types/api'
import {
  DURATION_UNITS,
  MAX_REPEAT,
  MONTH_LAST,
  MONTH_WEEKS,
  RECURRENCES,
  WEEKDAYS,
  toInput,
  upcomingLabel,
  type DurationUnit,
  type MaintenanceDraft,
} from './maintenance'
import { MaintenanceScopePicker } from './MaintenanceScopePicker'
import { OCCURRENCE_LIMIT, OccurrenceGrid } from './OccurrenceGrid'

const UNIT_LABEL = { daily: 'günde', weekly: 'haftada', monthly: 'ayda' } as const

// Bakım penceresi formu (yeni ve düzenleme). Saatler kurulumun saat dilimindedir; çeviriyi server yapar. Form değiştikçe
// "sonraki tekrarlar" server'dan istenir (yazmayı bekleyip bir kez). Alan hataları server'dan gelir ve ilgili alanın
// altında gösterilir.
export function MaintenanceForm({
  initial,
  editingId,
  clock,
  orgs,
  hosts,
  onSaved,
  onCancel,
}: {
  initial: MaintenanceDraft
  editingId?: string
  clock: ServerClock | null
  orgs: Organization[]
  hosts: OverviewHost[]
  onSaved: (w: MaintenanceWindow) => void
  onCancel: () => void
}) {
  const [draft, setDraft] = useState(initial)
  const [errors, setErrors] = useState<Record<string, string>>({})
  const [error, setError] = useState<string | null>(null)
  const [saving, setSaving] = useState(false)
  const [preview, setPreview] = useState<{ upcoming: MaintenanceOccurrence[] } | { error: string } | null>(null)
  const set = (patch: Partial<MaintenanceDraft>) => setDraft((d) => ({ ...d, ...patch }))

  // Önizleme açıklama ve kapsamdan bağımsızdır: yalnızca zamana ilişkin alanlar değişince istenir.
  const timing = useMemo(() => JSON.stringify({ ...toInput(draft), title: 'önizleme', host_ids: [], organization_ids: [] }), [draft])
  useEffect(() => {
    let alive = true
    const t = setTimeout(() => {
      maintenanceApi
        .preview(JSON.parse(timing))
        .then((p) => alive && setPreview({ upcoming: p.upcoming }))
        .catch((err) => alive && setPreview({ error: err instanceof Error ? err.message : 'önizleme alınamadı' }))
    }, 400)
    return () => {
      alive = false
      clearTimeout(t)
    }
  }, [timing])

  async function submit(e: FormEvent) {
    e.preventDefault()
    setSaving(true)
    setErrors({})
    setError(null)
    try {
      const input = toInput(draft)
      onSaved(editingId ? await maintenanceApi.update(editingId, input) : await maintenanceApi.create(input))
    } catch (err) {
      if (err instanceof ApiError && err.fields) setErrors(err.fields)
      else setError(err instanceof Error ? err.message : 'kaydedilemedi')
    } finally {
      setSaving(false)
    }
  }

  const fieldError = (...names: string[]) => {
    const msg = names.map((n) => errors[n]).find(Boolean)
    return msg ? <p className="field-error flush">{msg}</p> : null
  }
  const recurring = draft.recurrence !== 'once'
  const zone = clock ? `${clock.timezone} (${clock.offset})` : null

  return (
    <form className="card form-card maintenance-form" onSubmit={submit}>
      {error && <div className="error-banner">{error}</div>}

      <div className="form-row">
        <FieldLabel htmlFor="mw-title" label="Açıklama" hint="Listede ve denetim kaydında görünür (ör. “PostgreSQL yükseltmesi”)." />
        <input id="mw-title" value={draft.title} maxLength={200} onChange={(e) => set({ title: e.target.value })} />
        {fieldError('title')}
      </div>

      <div className="form-row">
        <span className="form-label">Tekrar</span>
        <div className="segmented" role="group" aria-label="Tekrar">
          {RECURRENCES.map((r) => (
            <button key={r.value} type="button" aria-pressed={draft.recurrence === r.value} onClick={() => set({ recurrence: r.value })}>
              {r.label}
            </button>
          ))}
        </div>
        {fieldError('recurrence')}
      </div>

      {!recurring && (
        <div className="form-grid-2">
          <div className="form-row">
            <FieldLabel htmlFor="mw-start" label="Başlangıç" hint={zone ? `Saat ${zone} saatine göredir.` : undefined} />
            <input id="mw-start" type="datetime-local" value={draft.startsLocal} onChange={(e) => set({ startsLocal: e.target.value })} />
            {fieldError('starts_local')}
          </div>
          <div className="form-row">
            <FieldLabel htmlFor="mw-end" label="Bitiş" />
            <input id="mw-end" type="datetime-local" value={draft.endsLocal} onChange={(e) => set({ endsLocal: e.target.value })} />
            {fieldError('ends_local')}
          </div>
        </div>
      )}

      {recurring && draft.recurrence !== 'once' && (
        <>
          <div className="form-row">
            <span className="form-label">Sıklık</span>
            <div className="row row-tight">
              <span>Her</span>
              <select
                aria-label="Aralık"
                value={draft.repeatEvery}
                onChange={(e) => set({ repeatEvery: e.target.value })}
              >
                {Array.from({ length: MAX_REPEAT[draft.recurrence] }, (_, i) => String(i + 1)).map((n) => (
                  <option key={n} value={n}>
                    {n}
                  </option>
                ))}
              </select>
              <span>{UNIT_LABEL[draft.recurrence]} bir</span>
            </div>
            {fieldError('repeat_every')}
          </div>

          {draft.recurrence === 'weekly' && (
            <div className="form-row">
              <span className="form-label">Günler</span>
              <div className="segmented weekday-picker" role="group" aria-label="Günler">
                {WEEKDAYS.map((d) => {
                  const on = draft.weekdays.includes(d.value)
                  return (
                    <button
                      key={d.value}
                      type="button"
                      title={d.long}
                      aria-pressed={on}
                      onClick={() => set({ weekdays: on ? draft.weekdays.filter((x) => x !== d.value) : [...draft.weekdays, d.value] })}
                    >
                      {d.short}
                    </button>
                  )
                })}
              </div>
              {fieldError('weekdays')}
            </div>
          )}

          {draft.recurrence === 'monthly' && (
            <div className="form-row">
              <span className="form-label">Ayın hangi günü</span>
              <label className="check-row">
                <input type="radio" name="mw-month" checked={draft.monthMode === 'day'} onChange={() => set({ monthMode: 'day' })} />
                Ayın
                <select aria-label="Ayın günü" value={draft.monthDay} disabled={draft.monthMode !== 'day'} onChange={(e) => set({ monthDay: e.target.value })}>
                  {Array.from({ length: 28 }, (_, i) => String(i + 1)).map((n) => (
                    <option key={n} value={n}>
                      {n}.
                    </option>
                  ))}
                  <option value={String(MONTH_LAST)}>son</option>
                </select>
                günü
              </label>
              <label className="check-row">
                <input type="radio" name="mw-month" checked={draft.monthMode === 'weekday'} onChange={() => set({ monthMode: 'weekday' })} />
                Ayın
                <select aria-label="Kaçıncı" value={draft.monthWeek} disabled={draft.monthMode !== 'weekday'} onChange={(e) => set({ monthWeek: e.target.value })}>
                  {MONTH_WEEKS.map((w) => (
                    <option key={w.value} value={String(w.value)}>
                      {w.label}
                    </option>
                  ))}
                </select>
                <select aria-label="Haftanın günü" value={draft.monthWeekday} disabled={draft.monthMode !== 'weekday'} onChange={(e) => set({ monthWeekday: e.target.value })}>
                  {WEEKDAYS.map((d) => (
                    <option key={d.value} value={String(d.value)}>
                      {d.long}
                    </option>
                  ))}
                </select>
              </label>
              {fieldError('month_day', 'month_week', 'month_weekday')}
            </div>
          )}

          <div className="form-grid-2">
            <div className="form-row">
              <FieldLabel htmlFor="mw-time" label="Başlangıç saati" hint={zone ? `Saat ${zone} saatine göredir.` : undefined} />
              <input id="mw-time" type="time" value={draft.startTime} onChange={(e) => set({ startTime: e.target.value })} />
              {fieldError('start_time')}
            </div>
            <div className="form-row">
              <FieldLabel
                htmlFor="mw-duration"
                label="Süre"
                hint={draft.recurrence === 'daily' ? 'Günlük tekrarda en çok 24 saat.' : 'En çok 7 gün. Gece yarısını geçebilir.'}
              />
              <div className="row row-tight">
                <input
                  id="mw-duration"
                  className="input-narrow"
                  inputMode="decimal"
                  value={draft.durationValue}
                  onChange={(e) => set({ durationValue: e.target.value })}
                />
                <select aria-label="Süre birimi" value={draft.durationUnit} onChange={(e) => set({ durationUnit: e.target.value as DurationUnit })}>
                  {DURATION_UNITS.map((u) => (
                    <option key={u.value} value={u.value}>
                      {u.label}
                    </option>
                  ))}
                </select>
              </div>
              {fieldError('duration_minutes')}
            </div>
            <div className="form-row">
              <FieldLabel
                htmlFor="mw-from"
                label="İlk tekrar (başlangıç)"
                hint="Tekrarlar bu tarihten sayılır; geçmiş bir tarih seçilirse geçmiş tekrarlar yok sayılır."
              />
              <input id="mw-from" type="date" value={draft.validFrom} onChange={(e) => set({ validFrom: e.target.value })} />
              {fieldError('valid_from')}
            </div>
            <div className="form-row">
              <FieldLabel htmlFor="mw-until" label="Bitiş tarihi" hint="İsteğe bağlı. Bu gün başlayan tekrar tam süresince sürer." />
              <input id="mw-until" type="date" value={draft.validUntil} onChange={(e) => set({ validUntil: e.target.value })} />
              {fieldError('valid_until')}
            </div>
          </div>
        </>
      )}

      <div className="form-row">
        <FieldLabel
          htmlFor="mw-scope"
          label="Kapsam"
          hint="Organizasyonun tamamı yalnızca ona doğrudan bağlı sunucuları kapsar; alt organizasyonlar ayrıca seçilir. Sonradan eklenen sunucular da kapsama girer."
        />
        <MaintenanceScopePicker
          orgs={orgs}
          hosts={hosts}
          hostIds={draft.hostIds}
          orgIds={draft.orgIds}
          onChange={(hostIds, orgIds) => set({ hostIds, orgIds })}
        />
        {fieldError('scope')}
      </div>

      <div className="maintenance-preview">
        <span className="form-label">
          {upcomingLabel(preview && 'upcoming' in preview ? Math.min(preview.upcoming.length, OCCURRENCE_LIMIT) : 0)}
        </span>
        {preview === null && <span className="muted">Hesaplanıyor…</span>}
        {preview && 'error' in preview && <span className="muted">{preview.error}</span>}
        {preview && 'upcoming' in preview && <OccurrenceGrid occurrences={preview.upcoming} />}
      </div>

      <div className="form-actions">
        <button className="btn btn-primary" type="submit" disabled={saving}>
          <Save size={15} strokeWidth={1.9} />
          {saving ? 'Kaydediliyor…' : 'Kaydet'}
        </button>
        <button className="btn" type="button" disabled={saving} onClick={onCancel}>
          Vazgeç
        </button>
        {clock && (
          <span className="muted" title={zone ?? undefined}>
            Saatler server saatine göredir ({clock.short})
          </span>
        )}
      </div>
    </form>
  )
}
