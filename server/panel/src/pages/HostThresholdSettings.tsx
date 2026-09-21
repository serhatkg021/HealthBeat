import { useEffect, useState } from 'react'
import { hostsApi } from '../api/endpoints'
import { ThresholdFields } from './ThresholdFields'
import {
  containerDraftsFromServer,
  defaultsFromServer,
  draftsFromServer,
  isDirty,
  isMountsDirty,
  mountDraftsFromServer,
  toMountOverrides,
  toOverrides,
  validateContainerDrafts,
  validateDrafts,
  validateMountDrafts,
  type Defaults,
  type Drafts,
  type MountDrafts,
} from './thresholds'
import { Check, Save, SlidersHorizontal } from 'lucide-react'

// Bir sunucunun eşikleri: metrik başına varsayılan (Eşikler sayfasında tanımlı) ya da kendi değerleri.
export function HostThresholdSettings({ hostId, canEdit, onSaved }: { hostId: string; canEdit: boolean; onSaved?: () => void }) {
  const [saved, setSaved] = useState<Drafts | null>(null)
  const [draft, setDraft] = useState<Drafts | null>(null)
  const [savedMounts, setSavedMounts] = useState<MountDrafts>({})
  const [draftMounts, setDraftMounts] = useState<MountDrafts>({})
  const [suggestions, setSuggestions] = useState<string[]>([])
  const [savedContainers, setSavedContainers] = useState<MountDrafts>({})
  const [draftContainers, setDraftContainers] = useState<MountDrafts>({})
  const [containerSuggestions, setContainerSuggestions] = useState<string[]>([])
  const [defaults, setDefaults] = useState<Defaults>({})
  const [error, setError] = useState<string | null>(null)
  const [notice, setNotice] = useState<string | null>(null)
  const [saving, setSaving] = useState(false)

  useEffect(() => {
    let cancelled = false
    hostsApi
      .thresholds(hostId)
      .then(({ thresholds, mount_thresholds, container_thresholds }) => {
        if (cancelled) return
        const drafts = draftsFromServer(thresholds)
        const mounts = mountDraftsFromServer(mount_thresholds)
        const containers = containerDraftsFromServer(container_thresholds)
        setSaved(drafts)
        setDraft(drafts)
        setSavedMounts(mounts)
        setDraftMounts(mounts)
        setSavedContainers(containers)
        setDraftContainers(containers)
        setDefaults(defaultsFromServer(thresholds))
      })
      .catch((err) => {
        if (!cancelled) setError(err instanceof Error ? err.message : 'eşikler yüklenemedi')
      })
    // Sunulmaya değer mount'lar: server'ın raporladıkları artı disk alert'i için seçilenler.
    hostsApi
      .diskAlerts(hostId)
      .then((d) => {
        if (!cancelled) setSuggestions([...new Set([...d.reported.map((r) => r.mount), ...d.custom_alert_mounts])].sort())
      })
      .catch(() => undefined)
    // Önerilecek container'lar: sunucunun son raporundaki container'lar.
    hostsApi
      .docker(hostId)
      .then((list) => {
        if (!cancelled) setContainerSuggestions([...new Set(list.map((c) => c.name))].sort())
      })
      .catch(() => undefined)
    return () => {
      cancelled = true
    }
  }, [hostId])

  if (!saved || !draft) {
    return (
      <div className="card settings-card settings-card-wide">
        <h2 className="card-title">
          <SlidersHorizontal size={16} strokeWidth={1.75} />
          Eşikler
        </h2>
        {error ? <div className="error-banner">{error}</div> : <div className="muted">Yükleniyor…</div>}
      </div>
    )
  }

  const dirty = isDirty(draft, saved) || isMountsDirty(draftMounts, savedMounts) || isMountsDirty(draftContainers, savedContainers)
  const problems = [...validateDrafts(draft), ...validateMountDrafts(draftMounts), ...validateContainerDrafts(draftContainers)]

  async function save() {
    if (!draft) return
    setSaving(true)
    setError(null)
    setNotice(null)
    try {
      const { thresholds, mount_thresholds, container_thresholds } = await hostsApi.setThresholds(
        hostId,
        toOverrides(draft),
        toMountOverrides(savedMounts, draftMounts),
        toMountOverrides(savedContainers, draftContainers),
      )
      const drafts = draftsFromServer(thresholds)
      const mounts = mountDraftsFromServer(mount_thresholds)
      const containers = containerDraftsFromServer(container_thresholds)
      setSavedContainers(containers)
      setDraftContainers(containers)
      setSaved(drafts)
      setDraft(drafts)
      setSavedMounts(mounts)
      setDraftMounts(mounts)
      setDefaults(defaultsFromServer(thresholds))
      setNotice('Eşikler kaydedildi.')
      onSaved?.() // Genel sekmesindeki çubuk renkleri yeni eşikleri izlesin.
    } catch (err) {
      setError(err instanceof Error ? err.message : 'eşikler kaydedilemedi')
    } finally {
      setSaving(false)
    }
  }

  return (
    <div className="card settings-card settings-card-wide">
      <h2 className="card-title">
        <SlidersHorizontal size={16} strokeWidth={1.75} />
        Eşikler
      </h2>
      <p className="card-desc">
        Her metrik için varsayılan değer ya da bu sunucuya özel değer. Varsayılan seçili olan metrikler, Eşikler
        sayfasındaki değer değişirse onu izler.
      </p>
      {error && <div className="error-banner">{error}</div>}
      {notice && !dirty && (
        <div className="save-note">
          <Check size={14} strokeWidth={2.2} />
          {notice}
        </div>
      )}
      <ThresholdFields
        idPrefix={`host-${hostId}`}
        drafts={draft}
        defaults={defaults}
        onChange={(next) => {
          setNotice(null)
          setDraft(next)
        }}
        containers={{ drafts: draftContainers, onChange: (c) => { setNotice(null); setDraftContainers(c) }, suggestions: containerSuggestions }}
        mounts={{ drafts: draftMounts, onChange: (m) => { setNotice(null); setDraftMounts(m) }, suggestions }}
        disabled={!canEdit || saving}
      />
      {canEdit && (
        <div className="row">
          <button className="btn btn-primary" onClick={save} disabled={!dirty || saving || problems.length > 0}>
            <Save size={15} strokeWidth={1.9} />
            {saving ? 'Kaydediliyor…' : 'Kaydet'}
          </button>
          {dirty && (
            <button className="btn" onClick={() => { setDraft(saved); setDraftMounts(savedMounts); setDraftContainers(savedContainers) }} disabled={saving}>
              Vazgeç
            </button>
          )}
          {dirty && problems.length > 0 && <span className="muted">Düzeltilmesi gereken değer var.</span>}
        </div>
      )}
    </div>
  )
}
