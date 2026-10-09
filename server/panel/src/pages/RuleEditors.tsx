import { useState } from 'react'
import type { DiskUsage, MetricType, StatusRule } from '../types/api'
import { DurationField } from './DurationField'
import { addMounts, buildRows, setMount, summarize, type State as DiskState } from './diskSelection'
import { ruleInfo, type RuleChoice, type RuleDraft } from './statusRules'
import { metricInfo, type Draft } from './thresholds'

// Alert kuralları satırlarının düzenleme alanları: eşik seviyeleri, durum kuralı seviyesi ve disk seçimi. Değerin ve
// sürenin alanları ayrıdır; satır onları kendi sütunlarına yerleştirir.

export function ThresholdLevelFields({ metric, draft, onChange, disabled }: { metric: MetricType; draft: Draft; onChange: (next: Draft) => void; disabled: boolean }) {
  const info = metricInfo(metric)
  return (
    <span className="rule-edit">
      <label className="level-field">
        <span className="muted">uyarı</span>
        <input
          className="level-input rule-level-input"
          inputMode="decimal"
          aria-label={`${info.label} uyarı seviyesi`}
          value={draft.warning}
          disabled={disabled}
          onChange={(e) => onChange({ ...draft, warning: e.target.value })}
        />
        <span className="muted">{info.unit}</span>
      </label>
      <label className="level-field">
        <span className="muted">kritik</span>
        <input
          className="level-input rule-level-input"
          inputMode="decimal"
          aria-label={`${info.label} kritik seviyesi`}
          value={draft.critical}
          disabled={disabled}
          onChange={(e) => onChange({ ...draft, critical: e.target.value })}
        />
        <span className="muted">{info.unit}</span>
      </label>
    </span>
  )
}

// Süre alanı yalnızca süre alan türlerde; diğerleri anlık değerlendirilir.
export function ThresholdDurationField({ metric, draft, onChange, disabled }: { metric: MetricType; draft: Draft; onChange: (next: Draft) => void; disabled: boolean }) {
  const info = metricInfo(metric)
  if (!info.duration) return <span className="muted">anlık</span>
  return <DurationField label={`${info.label} süresi`} value={draft.duration} disabled={disabled} onChange={(duration) => onChange({ ...draft, duration })} />
}

const LEVELS: { value: Exclude<RuleChoice, ''>; label: string }[] = [
  { value: 'off', label: 'Kapalı' },
  { value: 'info', label: 'Bilgi' },
  { value: 'warning', label: 'Uyarı' },
  { value: 'critical', label: 'Kritik' },
]

export function StatusLevelField({ rule, draft, onChange, disabled }: { rule: StatusRule; draft: RuleDraft; onChange: (next: RuleDraft) => void; disabled: boolean }) {
  return (
    <select aria-label={`${ruleInfo(rule).label} seviyesi`} value={draft.level} disabled={disabled} onChange={(e) => onChange({ ...draft, level: e.target.value as RuleChoice })}>
      {LEVELS.map((l) => (
        <option key={l.value} value={l.value}>
          {l.label}
        </option>
      ))}
    </select>
  )
}

export function StatusDurationField({ rule, draft, onChange, disabled }: { rule: StatusRule; draft: RuleDraft; onChange: (next: RuleDraft) => void; disabled: boolean }) {
  const info = ruleInfo(rule)
  if (!info.duration) return <span className="muted">anlık</span>
  if (draft.level === 'off' || draft.level === '') return <span className="muted">—</span>
  return (
    <span title={info.duration}>
      <DurationField label={`${info.label} süresi`} value={draft.duration} disabled={disabled} onChange={(duration) => onChange({ ...draft, duration })} />
    </span>
  )
}

// Hangi mount'ların doluluk alert'i üreteceği: raporlanan tümü ya da yalnızca seçilenler (raporlanmayan bir yol elle de
// eklenebilir).
export function DiskSelectionFields({
  hostId,
  state,
  reported,
  onChange,
  disabled,
}: {
  hostId: string
  state: DiskState
  reported: DiskUsage[]
  onChange: (next: DiskState) => void
  disabled: boolean
}) {
  const [typed, setTyped] = useState('')
  const [error, setError] = useState<string | null>(null)
  const rows = buildRows(reported, state.selected)
  const name = `disk-mode-${hostId}`

  function addTyped() {
    const result = addMounts(state, typed)
    setError(result.error)
    if (!result.error) {
      onChange(result.state)
      setTyped('')
    }
  }

  return (
    <div className="rule-panel">
      <label className="check-row">
        <input type="radio" name={name} checked={state.mode === 'all'} disabled={disabled} onChange={() => onChange({ ...state, mode: 'all' })} />
        Raporlanan tüm mount’lar
      </label>
      <label className="check-row">
        <input type="radio" name={name} checked={state.mode === 'selected'} disabled={disabled} onChange={() => onChange({ ...state, mode: 'selected' })} />
        Yalnızca seçtiklerim
      </label>
      {state.mode === 'selected' && (
        <div className="indent">
          {rows.length === 0 && <div className="muted">Agent henüz disk raporlamadı. Mount yolunu aşağıdan elle ekleyebilirsiniz.</div>}
          {rows.map((r) => (
            <label key={r.mount} className="check-row">
              <input type="checkbox" checked={r.selected} disabled={disabled} onChange={(e) => onChange(setMount(state, r.mount, e.target.checked))} />
              <code>{r.mount}</code>
              <span className="muted">{r.usedPct !== null ? `%${r.usedPct.toFixed(1)} dolu` : 'raporlanmıyor'}</span>
            </label>
          ))}
          {!disabled && (
            <div className="row row-tight" style={{ marginTop: 8 }}>
              <input
                aria-label="Mount yolu ekle"
                placeholder="/mnt/yedek, /srv/veri"
                value={typed}
                style={{ flex: 1 }}
                onChange={(e) => {
                  setTyped(e.target.value)
                  setError(null)
                }}
                onKeyDown={(e) => {
                  if (e.key === 'Enter') {
                    e.preventDefault()
                    addTyped()
                  }
                }}
              />
              <button className="btn" type="button" onClick={addTyped} disabled={typed.trim() === ''}>
                Ekle
              </button>
            </div>
          )}
          {error && <div className="field-error flush">{error}</div>}
        </div>
      )}
      <p className="form-hint" style={{ margin: '8px 0 0' }}>
        {summarize(state, rows)}
      </p>
    </div>
  )
}
