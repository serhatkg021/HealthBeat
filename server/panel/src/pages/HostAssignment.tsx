import { useEffect, useState } from 'react'
import { hostsApi, usersApi } from '../api/endpoints'
import type { Host, Organization, User } from '../types/api'
import {
  groupState,
  sameSelection,
  selectedCount,
  serializeSelection,
  toggleHost,
  toggleGroup,
} from './assignment'
import { Save, Server } from 'lucide-react'

interface LoadedGroup {
  org: Organization
  hosts: Host[] | null // null = bu organizasyonun sunucuları yüklenemedi
}

// Bir operatörün hangi sunucuları görebileceği. docs/MIMARI.md bölüm 4'teki iki
// seçeneği de sunar: bir organizasyonun tüm sunucuları birden ya da sunucular tek tek.
// Kaydetmek operatörün tüm atamasını değiştirir.
export function HostAssignment({
  user,
  orgs: orgsNow,
  onClose,
}: {
  user: User
  orgs: Organization[]
  onClose: () => void
}) {
  // Üst bileşen her yeniden yüklendiğinde (ör. başka bir kullanıcı oluşturduktan sonra) YENİ bir
  // dizi verir. Buna tepki vermek bu paneli yeniden yükler ve sürmekte olan düzenlemeleri
  // sessizce atardı; bu yüzden liste panel açıldığı anda sabitlenir.
  const [orgs] = useState(orgsNow)
  const [groups, setGroups] = useState<LoadedGroup[] | null>(null)
  const [initial, setInitial] = useState<Set<string> | null>(null)
  const [selected, setSelected] = useState<Set<string>>(new Set())
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState<string | null>(null)

  useEffect(() => {
    let cancelled = false // yavaş bir yanıt daha yeni bir yanıtın üzerine yazmamalı
    async function load() {
      try {
        const [assigned, lists] = await Promise.all([
          usersApi.hosts(user.id),
          Promise.all(
            orgs.map(async (org): Promise<LoadedGroup> => {
              try {
                const hosts = await hostsApi.listByOrganization(org.id)
                return { org, hosts: [...hosts].sort((a, b) => a.title.localeCompare(b.title)) }
              } catch {
                return { org, hosts: null }
              }
            }),
          ),
        ])
        if (cancelled) return
        const ids = new Set(assigned.host_ids)
        setInitial(ids)
        setSelected(new Set(ids))
        setGroups([...lists].sort((a, b) => a.org.name.localeCompare(b.org.name)))
      } catch (err) {
        if (!cancelled) setError(err instanceof Error ? err.message : 'mevcut atama yüklenemedi')
      }
    }
    void load()
    return () => {
      cancelled = true
    }
  }, [user.id, orgs])

  const anyGroupFailed = groups?.some((g) => g.hosts === null) ?? false
  const dirty = initial !== null && !sameSelection(initial, selected)
  // Kaydetmek tüm atamayı değiştirir; bu yüzden yalnızca hiçbir şey yüklenemeden kalmadığında
  // güvenlidir: aksi halde gösteremediğimiz sunucular "seçimi kaldırılmış" sayılabilir.
  const canSave = initial !== null && groups !== null && !anyGroupFailed && dirty && !saving

  async function save() {
    setSaving(true)
    setError(null)
    try {
      await usersApi.setHosts(user.id, serializeSelection(selected))
      onClose()
    } catch (err) {
      setError(err instanceof Error ? err.message : 'atama kaydedilemedi')
      setSaving(false)
    }
  }

  return (
    <div className="card form-card assign-card">
      <h2 className="card-title">
        <Server size={16} strokeWidth={1.75} />
        Sunucu ataması
      </h2>
      <p className="card-desc">
        Operatör yalnızca seçili sunucuları görür. "Tümünü seç" o organizasyonun <em>şu anki</em> sunucularını
        işaretler; sonradan eklenen sunucular otomatik atanmaz.
      </p>
      {error && <div className="error-banner">{error}</div>}
      {groups === null && !error && <div className="muted">Yükleniyor…</div>}
      {anyGroupFailed && (
        <div className="error-banner">
          Bazı organizasyonların sunucuları yüklenemedi. Mevcut atamayı yanlışlıkla silmemek için kaydetme kapalı;
          sayfayı yenileyip tekrar deneyin.
        </div>
      )}

      {groups?.map(({ org, hosts }) => {
        if (hosts === null) {
          return (
            <div key={org.id} className="assign-group">
              <strong>{org.name}</strong> <span className="muted">— sunucular yüklenemedi</span>
            </div>
          )
        }
        const group = { hosts }
        const state = groupState(group, selected)
        return (
          <fieldset key={org.id} className="assign-group">
            <label className="check-row assign-org">
              <input
                type="checkbox"
                disabled={hosts.length === 0}
                checked={state === 'all'}
                ref={(el) => {
                  if (el) el.indeterminate = state === 'some'
                }}
                onChange={(e) => setSelected((prev) => toggleGroup(prev, group, e.target.checked))}
              />
              {org.name}
              <span className="muted assign-count">
                ({selectedCount(group, selected)}/{hosts.length}) — tümünü seç
              </span>
            </label>
            {hosts.length === 0 && (
              <div className="muted indent">
                Bu organizasyonda sunucu yok.
              </div>
            )}
            {hosts.map((c) => (
              <label key={c.id} className="check-row indent">
                <input
                  type="checkbox"
                  checked={selected.has(c.id)}
                  onChange={(e) => setSelected((prev) => toggleHost(prev, c.id, e.target.checked))}
                />
                {c.title} <span className="muted mono">{c.ip}</span>
              </label>
            ))}
          </fieldset>
        )
      })}

      <div className="row" style={{ marginTop: 14 }}>
        <button className="btn btn-primary" onClick={save} disabled={!canSave}>
          <Save size={15} strokeWidth={1.9} />
          {saving ? 'Kaydediliyor…' : 'Kaydet'}
        </button>
        <button className="btn" onClick={onClose} disabled={saving}>
          Vazgeç
        </button>
        {groups && <span className="muted">{selected.size} sunucu seçili</span>}
      </div>
    </div>
  )
}
