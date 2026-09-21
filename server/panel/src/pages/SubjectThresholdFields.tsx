import { useState } from 'react'
import {
  addMount,
  parseContainerToAdd,
  parseMountToAdd,
  removeMount,
  validateContainerDraft,
  validateMountDraft,
  type MountDraft,
  type MountDrafts,
} from './thresholds'
import type { ThresholdLevels } from '../types/api'
import { Plus, Trash2 } from 'lucide-react'

export type SubjectKind = 'mount' | 'container'

// Bir alt-öğenin (mount ya da container) kendi eşiği olabilir ("/" 70/90 ama "/storage" 90/95).
// Listedeki öğeler kendi değerini kullanır, diğerleri yukarıdaki sunucu değerini izler; satırı
// kaldırmak öğeyi sunucu değerine döndürür.
const KINDS = {
  mount: {
    title: 'Mount’a özel değerler',
    help: 'Bir mount’un kendi değeri varsa o mount için yukarıdaki disk değeri yerine o kullanılır (ör. / → 70/90, /storage → 90/95). Listede olmayan mount’lar yukarıdaki değeri izler. Kendi değeri olan bir mount, raporlardan 3 ardışık raporda kaybolursa “disk kayboldu” alert’i açılır.',
    none: 'Henüz mount’a özel değer yok.',
    placeholder: '/storage',
    inputLabel: 'Mount yolu',
    addButton: 'Mount ekle',
    unit: '%',
    validate: validateMountDraft,
    parse: parseMountToAdd,
  },
  container: {
    title: 'Container’a özel değerler',
    help: 'Bir container’ın kendi değeri varsa o container için yukarıdaki docker restart değeri yerine o kullanılır (ör. web → 3/10, batch → 50/100). Listede olmayan container’lar yukarıdaki değeri izler.',
    none: 'Henüz container’a özel değer yok.',
    placeholder: 'batch',
    inputLabel: 'Container adı',
    addButton: 'Container ekle',
    unit: 'restart',
    validate: validateContainerDraft,
    parse: parseContainerToAdd,
  },
} as const

export function SubjectThresholdFields({
  kind,
  drafts,
  onChange,
  suggestions,
  baseLevels,
  disabled = false,
}: {
  kind: SubjectKind
  drafts: MountDrafts
  onChange: (next: MountDrafts) => void
  suggestions: string[] // bildiğimiz öğeler (sunucunun raporladığı / seçilenler)
  baseLevels: ThresholdLevels | undefined // yeni satırın başlangıç değeri
  disabled?: boolean
}) {
  const cfg = KINDS[kind]
  const [typed, setTyped] = useState('')
  const [error, setError] = useState<string | null>(null)
  const mounts = Object.keys(drafts).sort()
  const available = suggestions.filter((m) => !(m in drafts))

  function add(mount: string) {
    onChange(addMount(drafts, mount, baseLevels))
  }

  function addTyped() {
    const parsed = cfg.parse(typed, drafts)
    if ('error' in parsed) {
      setError(parsed.error)
      return
    }
    setError(null)
    setTyped('')
    add(parsed.mount)
  }

  const set = (mount: string, patch: Partial<MountDraft>) => onChange({ ...drafts, [mount]: { ...drafts[mount], ...patch } })

  return (
    <div className="subject-block">
      <div className="subject-title">{cfg.title}</div>
      <p className="form-hint" style={{ margin: '2px 0 10px' }}>
        {cfg.help}
      </p>

      {mounts.map((mount) => {
        const err = cfg.validate(drafts[mount])
        return (
          <div key={mount} className="subject-row">
            <div className="threshold-inputs subject-inputs">
              <code className="subject-name">{mount}</code>
              <label>
                Uyarı ({cfg.unit})
                <input
                  inputMode="decimal"
                  aria-label={`${mount} uyarı seviyesi`}
                  value={drafts[mount].warning}
                  disabled={disabled}
                  onChange={(e) => set(mount, { warning: e.target.value })}
                />
              </label>
              <label>
                Kritik ({cfg.unit})
                <input
                  inputMode="decimal"
                  aria-label={`${mount} kritik seviyesi`}
                  value={drafts[mount].critical}
                  disabled={disabled}
                  onChange={(e) => set(mount, { critical: e.target.value })}
                />
              </label>
              {!disabled && (
                <button type="button" className="btn btn-sm btn-danger" onClick={() => onChange(removeMount(drafts, mount))}>
                  <Trash2 size={13} strokeWidth={1.9} />
                  Kaldır
                </button>
              )}
            </div>
            {err && (
              <div className="field-error flush">
                {err}
              </div>
            )}
          </div>
        )
      })}
      {mounts.length === 0 && (
        <p className="form-hint" style={{ margin: '0 0 8px' }}>
          {cfg.none}
        </p>
      )}

      {!disabled && (
        <div>
          {available.length > 0 && (
            <div className="row row-tight" style={{ marginBottom: 8 }}>
              <span className="form-hint">Ekle:</span>
              {available.map((m) => (
                <button key={m} type="button" className="btn btn-sm" onClick={() => add(m)}>
                  <Plus size={13} strokeWidth={2} />
                  <span className="mono">{m}</span>
                </button>
              ))}
            </div>
          )}
          <div className="row row-tight row-nowrap">
            <input
              aria-label={cfg.inputLabel}
              placeholder={cfg.placeholder}
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
            <button type="button" className="btn" onClick={addTyped}>
              <Plus size={14} strokeWidth={2} />
              {cfg.addButton}
            </button>
          </div>
          {error && (
            <div className="field-error flush">
              {error}
            </div>
          )}
        </div>
      )}
    </div>
  )
}
