import type { ReactNode } from 'react'
import { SubjectThresholdFields } from './SubjectThresholdFields'
import { formatLevels, METRICS, serverLevels, validateDraft, withMode, type Defaults, type Drafts, type Draft, type Mode, type MountDrafts } from './thresholds'

// Metrik başına bir blok: "Varsayılan" (Eşikler sayfasında tanımlı değeri izle) ya da "Özel
// değer" (bu sunucunun kendi uyarı/kritik değeri). Sunucu ekleme sihirbazı ve sunucu sayfası paylaşır.
export function ThresholdFields({
  idPrefix,
  drafts,
  defaults,
  onChange,
  mounts,
  containers,
  disabled = false,
}: {
  idPrefix: string
  drafts: Drafts
  defaults: Defaults
  onChange: (next: Drafts) => void
  // Mount başına disk eşikleri, Disk bloğunun içinde gösterilir.
  mounts?: { drafts: MountDrafts; onChange: (next: MountDrafts) => void; suggestions: string[] }
  // Container başına docker_restart eşikleri, Docker restart bloğunun içinde gösterilir.
  containers?: { drafts: MountDrafts; onChange: (next: MountDrafts) => void; suggestions: string[] }
  disabled?: boolean
}) {
  return (
    <div className="threshold-fields">
      {METRICS.map((m) => {
        const draft = drafts[m.type]
        const def = defaults[m.type]
        const error = validateDraft(m.type, draft)
        const setMode = (mode: Mode) => onChange(withMode(drafts, m.type, mode, defaults))
        const setField = (patch: Partial<Draft>) => onChange({ ...drafts, [m.type]: { ...draft, ...patch } })
        const name = `${idPrefix}-${m.type}`

        return (
          <fieldset key={m.type} className="threshold-row" disabled={disabled}>
            <legend>{m.label}</legend>
            {m.hint && (
              <p className="form-hint" style={{ margin: '0 0 6px' }}>
                {m.hint}
              </p>
            )}
            <Radio name={name} checked={draft.mode === 'default'} onSelect={() => setMode('default')} disabled={disabled}>
              Varsayılan{' '}
              <span className="muted">
                {def ? `(${formatLevels(m.type, def)})` : '(tanımlı değil — bu metrik için alert üretilmez)'}
              </span>
            </Radio>
            <Radio name={name} checked={draft.mode === 'custom'} onSelect={() => setMode('custom')} disabled={disabled}>
              Özel değer
            </Radio>
            {draft.mode === 'custom' && (
              <>
                <div className="threshold-inputs">
                  <label>
                    Uyarı ({m.unit})
                    <input
                      inputMode="decimal"
                      aria-label={`${m.label} uyarı seviyesi`}
                      value={draft.warning}
                      onChange={(e) => setField({ warning: e.target.value })}
                    />
                  </label>
                  <label>
                    Kritik ({m.unit})
                    <input
                      inputMode="decimal"
                      aria-label={`${m.label} kritik seviyesi`}
                      value={draft.critical}
                      onChange={(e) => setField({ critical: e.target.value })}
                    />
                  </label>
                </div>
                {error && <div className="field-error">{error}</div>}
              </>
            )}
            {m.type === 'disk' && mounts && (
              <SubjectThresholdFields
                kind="mount"
                drafts={mounts.drafts}
                onChange={mounts.onChange}
                suggestions={mounts.suggestions}
                baseLevels={serverLevels('disk', draft, defaults)}
                disabled={disabled}
              />
            )}
            {m.type === 'docker_restart' && containers && (
              <SubjectThresholdFields
                kind="container"
                drafts={containers.drafts}
                onChange={containers.onChange}
                suggestions={containers.suggestions}
                baseLevels={serverLevels('docker_restart', draft, defaults)}
                disabled={disabled}
              />
            )}
          </fieldset>
        )
      })}
    </div>
  )
}

function Radio({
  name,
  checked,
  onSelect,
  disabled,
  children,
}: {
  name: string
  checked: boolean
  onSelect: () => void
  disabled: boolean
  children: ReactNode
}) {
  return (
    <label className={`radio-row${disabled ? ' disabled' : ''}`}>
      <input type="radio" name={name} checked={checked} onChange={onSelect} disabled={disabled} />
      <span>{children}</span>
    </label>
  )
}
