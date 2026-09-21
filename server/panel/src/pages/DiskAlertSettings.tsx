import { useEffect, useState } from 'react'
import { hostsApi } from '../api/endpoints'
import type { DiskAlertSettings as Settings } from '../types/api'
import {
  addMounts,
  buildRows,
  fromServer,
  isDirty,
  setMount,
  summarize,
  toServer,
  type State,
} from './diskSelection'
import { HardDrive, RefreshCw, Save } from 'lucide-react'

// Bir sunucunun hangi disklerinin alert üretebileceği. Agent (grafikler için) çok sayıda mount
// raporlayabilir; yalnızca işaretlenenler alert üretir ve artık işaretli olmayan bir mount'un
// alert'i server bir sonraki raporlayışında kendiliğinden kapanır.
export function DiskAlertSettings({ hostId, canEdit }: { hostId: string; canEdit: boolean }) {
  const [settings, setSettings] = useState<Settings | null>(null)
  const [saved, setSaved] = useState<State | null>(null)
  const [draft, setDraft] = useState<State | null>(null)
  const [typed, setTyped] = useState('')
  const [error, setError] = useState<string | null>(null)
  const [saving, setSaving] = useState(false)
  const [reloadTick, setReloadTick] = useState(0)

  useEffect(() => {
    let cancelled = false // yavaş bir yanıt daha yeni bir yanıtın üzerine yazmamalı
    hostsApi
      .diskAlerts(hostId)
      .then((s) => {
        if (cancelled) return
        setSettings(s)
        const state = fromServer(s.all_mounts_alert, s.custom_alert_mounts)
        setSaved(state)
        // Raporlanan mount listesini yenilemek sürmekte olan düzenlemeleri atmamalı.
        setDraft((current) => current ?? state)
      })
      .catch((err) => {
        if (!cancelled) setError(err instanceof Error ? err.message : 'disk ayarları yüklenemedi')
      })
    return () => {
      cancelled = true
    }
  }, [hostId, reloadTick])

  if (!settings || !saved || !draft) {
    return (
      <div className="card settings-card">
        <h2 className="card-title">
          <HardDrive size={16} strokeWidth={1.75} />
          Disk alert’leri
        </h2>
        {error ? <div className="error-banner">{error}</div> : <div className="muted">Yükleniyor…</div>}
      </div>
    )
  }

  const rows = buildRows(settings.reported, draft.selected)
  const dirty = isDirty(draft, saved)

  function addTyped() {
    if (!draft) return
    const result = addMounts(draft, typed)
    setError(result.error)
    if (!result.error) {
      setDraft(result.state)
      setTyped('')
    }
  }

  async function save() {
    if (!draft) return
    setSaving(true)
    setError(null)
    try {
      const updated = await hostsApi.setDiskAlerts(hostId, toServer(draft))
      setSettings(updated)
      const state = fromServer(updated.all_mounts_alert, updated.custom_alert_mounts)
      setSaved(state)
      setDraft(state)
    } catch (err) {
      setError(err instanceof Error ? err.message : 'kaydedilemedi')
    } finally {
      setSaving(false)
    }
  }

  return (
    <div className="card settings-card">
      <h2 className="card-title">
        <HardDrive size={16} strokeWidth={1.75} />
        Disk alert’leri
      </h2>
      {error && <div className="error-banner">{error}</div>}

      <label className="check-row">
        <input
          type="radio"
          name="disk-mode"
          checked={draft.mode === 'all'}
          disabled={!canEdit}
          onChange={() => setDraft({ ...draft, mode: 'all' })}
        />
        Raporlanan tüm diskler
      </label>
      <label className="check-row">
        <input
          type="radio"
          name="disk-mode"
          checked={draft.mode === 'selected'}
          disabled={!canEdit}
          onChange={() => setDraft({ ...draft, mode: 'selected' })}
        />
        Yalnızca seçtiklerim
      </label>

      {draft.mode === 'selected' && (
        <div className="indent">
          {rows.length === 0 && (
            <div className="muted">Agent henüz disk raporlamadı. Aşağıdan mount yolunu elle ekleyebilirsiniz.</div>
          )}
          {rows.map((r) => (
            <label key={r.mount} className="check-row">
              <input
                type="checkbox"
                checked={r.selected}
                disabled={!canEdit}
                onChange={(e) => setDraft(setMount(draft, r.mount, e.target.checked))}
              />
              <code>{r.mount}</code>
              {r.usedPct !== null ? (
                <span className="muted">%{r.usedPct.toFixed(1)} dolu</span>
              ) : (
                <span className="muted">raporlanmıyor</span>
              )}
            </label>
          ))}
          {canEdit && (
            <div className="row row-tight" style={{ marginTop: 8 }}>
              <input
                aria-label="Mount yolu ekle"
                placeholder="/mnt/yedek, /srv/veri"
                value={typed}
                onChange={(e) => setTyped(e.target.value)}
                onKeyDown={(e) => {
                  if (e.key === 'Enter') {
                    e.preventDefault()
                    addTyped()
                  }
                }}
                style={{ flex: 1 }}
              />
              <button className="btn" type="button" onClick={addTyped} disabled={typed.trim() === ''}>
                Ekle
              </button>
            </div>
          )}
        </div>
      )}

      <p className="form-hint" style={{ margin: '12px 0' }}>
        {summarize(draft, rows)} Seçilmeyen disklerin açık alert’leri bir sonraki raporda kapanır.
      </p>

      <div className="row">
        {canEdit && (
          <button className="btn btn-primary" onClick={save} disabled={!dirty || saving}>
            <Save size={15} strokeWidth={1.9} />
            {saving ? 'Kaydediliyor…' : 'Kaydet'}
          </button>
        )}
        {canEdit && dirty && (
          <button className="btn" onClick={() => setDraft(saved)} disabled={saving}>
            Vazgeç
          </button>
        )}
        <button className="btn" onClick={() => setReloadTick((n) => n + 1)} disabled={saving} title="Agent'ın son raporladığı diskleri yeniden oku">
          <RefreshCw size={15} strokeWidth={1.9} />
          Diskleri yenile
        </button>
      </div>
    </div>
  )
}
