import { useState } from 'react'
import {
  addMount,
  parseContainerToAdd,
  parseMountToAdd,
  parseSubjectToAdd,
  removeMount,
  validateContainerDraft,
  validateMountDraft,
  validateSubjectDraft,
  type MountDraft,
  type MountDrafts,
} from './thresholds'
import { InfoTip } from '../components/InfoTip'
import type { SubjectMetricType, ThresholdLevels } from '../types/api'
import { DurationField } from './DurationField'
import { emptyDuration } from './duration'
import { Plus, Trash2, Wand2 } from 'lucide-react'

export type SubjectKind = 'mount' | 'container' | SubjectMetricType

interface KindConfig {
  title: string
  help: string
  none: string
  placeholder: string
  inputLabel: string
  addButton: string
  unit: string
  // Satırın kendi süresi var (protokol 4 türleri).
  duration: boolean
  validate: (d: MountDraft) => string | null
  parse: (text: string, existing: MountDrafts) => { mount: string } | { error: string }
}

// Bir alt-öğenin (mount, container, disk, sensör ya da servis) kendi eşiği olabilir ("/" 70/90 ama "/storage" 90/95).
// Listedeki öğeler kendi değerini kullanır, diğerleri yukarıdaki sunucu değerini izler; satırı kaldırmak öğeyi sunucu
// değerine döndürür.
const KINDS: Record<SubjectKind, KindConfig> = {
  mount: {
    title: 'Mount’a özel değerler',
    help: 'Bir mount’un kendi değeri varsa o mount için yukarıdaki disk değeri yerine o kullanılır (ör. / → 70/90, /storage → 90/95). Listede olmayan mount’lar yukarıdaki değeri izler. Kendi değeri olan bir mount, raporlardan 3 ardışık raporda kaybolursa “disk kayboldu” alert’i açılır.',
    none: 'Henüz mount’a özel değer yok.',
    placeholder: '/storage',
    inputLabel: 'Mount yolu',
    addButton: 'Mount ekle',
    unit: '%',
    duration: false,
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
    duration: false,
    validate: validateContainerDraft,
    parse: parseContainerToAdd,
  },
  disk_latency: {
    title: 'Diske özel değerler',
    help: 'Bir diskin kendi değeri varsa o disk için yukarıdaki değer yerine o kullanılır (ör. yavaş bir HDD için daha yüksek sınır). Listede olmayan diskler yukarıdaki değeri izler.',
    none: 'Henüz diske özel değer yok.',
    placeholder: 'sdb',
    inputLabel: 'Disk adı',
    addButton: 'Disk ekle',
    unit: 'ms',
    duration: true,
    validate: (d) => validateSubjectDraft('disk_latency', d),
    parse: (text, existing) => parseSubjectToAdd('disk_latency', text, existing),
  },
  temperature: {
    title: 'Sensöre özel değerler',
    help: 'Bir sensörün kendi değeri varsa o sensör için yukarıdaki değer yerine o kullanılır. Donanımın bildirdiği sınır biliniyorsa sensörün yanında görünür; “Öneriyi kullan” onu alanlara yazar.',
    none: 'Henüz sensöre özel değer yok.',
    placeholder: 'coretemp/Package id 0',
    inputLabel: 'Sensör adı',
    addButton: 'Sensör ekle',
    unit: '°C',
    duration: true,
    validate: (d) => validateSubjectDraft('temperature', d),
    parse: (text, existing) => parseSubjectToAdd('temperature', text, existing),
  },
  service_restart: {
    title: 'Servise özel değerler',
    help: 'İzlenen bir servisin kendi değeri varsa o servis için yukarıdaki değer yerine o kullanılır. Yalnızca izlenen servisler değerlendirilir.',
    none: 'Henüz servise özel değer yok.',
    placeholder: 'nginx.service',
    inputLabel: 'Servis adı',
    addButton: 'Servis ekle',
    unit: 'kez',
    duration: true,
    validate: (d) => validateSubjectDraft('service_restart', d),
    parse: (text, existing) => parseSubjectToAdd('service_restart', text, existing),
  },
}

// Bir öneriye eşlik eden bilgi: kısa açıklama ve varsa kullanılabilecek seviyeler (sensörün donanım sınırı gibi).
export interface SubjectNote {
  text: string
  levels?: ThresholdLevels
}

export function SubjectThresholdFields({
  kind,
  drafts,
  onChange,
  suggestions,
  notes = {},
  baseLevels,
  disabled = false,
}: {
  kind: SubjectKind
  drafts: MountDrafts
  onChange: (next: MountDrafts) => void
  suggestions: string[] // bildiğimiz öğeler (sunucunun raporladığı / seçilenler)
  notes?: Record<string, SubjectNote>
  baseLevels: ThresholdLevels | undefined // yeni satırın başlangıç değeri
  disabled?: boolean
}) {
  const cfg = KINDS[kind]
  const [typed, setTyped] = useState('')
  const [error, setError] = useState<string | null>(null)
  const mounts = Object.keys(drafts).sort()
  const available = suggestions.filter((m) => !(m in drafts))

  function add(mount: string) {
    onChange(addMount(drafts, mount, baseLevels, cfg.duration))
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
      <div className="subject-title">
        {cfg.title}
        <InfoTip label={cfg.title}>{cfg.help}</InfoTip>
      </div>

      {mounts.map((mount) => {
        const err = cfg.validate(drafts[mount])
        const note = notes[mount]
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
              {cfg.duration && (
                <label>
                  Süre
                  <DurationField
                    label={`${mount} süresi`}
                    value={drafts[mount].duration ?? emptyDuration()}
                    disabled={disabled}
                    onChange={(duration) => set(mount, { duration })}
                  />
                </label>
              )}
              {!disabled && (
                <button type="button" className="btn btn-sm btn-danger" onClick={() => onChange(removeMount(drafts, mount))}>
                  <Trash2 size={13} strokeWidth={1.9} />
                  Kaldır
                </button>
              )}
            </div>
            {note && (
              <div className="subject-note">
                <span className="form-hint">{note.text}</span>
                {note.levels && !disabled && (
                  <button
                    type="button"
                    className="btn btn-sm"
                    onClick={() => set(mount, { warning: String(note.levels!.warning_level), critical: String(note.levels!.critical_level) })}
                  >
                    <Wand2 size={13} strokeWidth={1.9} />
                    Öneriyi kullan
                  </button>
                )}
              </div>
            )}
            {err && <div className="field-error flush">{err}</div>}
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
                <button key={m} type="button" className="btn btn-sm" onClick={() => add(m)} title={notes[m]?.text}>
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
          {error && <div className="field-error flush">{error}</div>}
        </div>
      )}
    </div>
  )
}
