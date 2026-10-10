import { useMemo, useRef, useState, type FormEvent } from 'react'
import { Save, Trash2, TriangleAlert } from 'lucide-react'
import { organizationsApi } from '../api/endpoints'
import { useAuth } from '../auth/AuthContext'
import { Modal } from '../components/Modal'
import { SearchSelect } from '../components/SearchSelect'
import type { SelectOption } from '../components/searchSelect'
import { FieldLabel } from '../components/FieldLabel'
import type { Organization } from '../types/api'
import { buildTree, parentChoices } from './orgTree'

// Kök organizasyonu seçen değer (SearchSelect boş değeri "seçilmemiş" sayar).
const ROOT = '__root__'

// Organizasyonun adı, adresi ve ağaçtaki yeri (organization.update); silme (organization.delete). Organizasyon listesindeki
// ve organizasyon sayfasındaki çark simgesiyle açılan pencerede. Silme pencerenin içinde ikinci bir onay ister.
export function OrganizationSettingsModal({
  organization,
  orgs,
  onClose,
  onSaved,
  onDeleted,
}: {
  // null = pencere kapalı.
  organization: Organization | null
  orgs: Organization[]
  onClose: () => void
  onSaved: () => void
  onDeleted: () => void
}) {
  return (
    <Modal open={organization !== null} size="sm" title={organization ? `${organization.name} · ayarlar` : 'Organizasyon ayarları'} onClose={onClose}>
      {organization && (
        <SettingsForm key={organization.id} organization={organization} orgs={orgs} onClose={onClose} onSaved={onSaved} onDeleted={onDeleted} />
      )}
    </Modal>
  )
}

function SettingsForm({
  organization,
  orgs,
  onClose,
  onSaved,
  onDeleted,
}: {
  organization: Organization
  orgs: Organization[]
  onClose: () => void
  onSaved: () => void
  onDeleted: () => void
}) {
  const { can } = useAuth()
  const [name, setName] = useState(organization.name)
  const [address, setAddress] = useState(organization.address ?? '')
  const [parent, setParent] = useState(organization.parent_organization_id ?? ROOT)
  const [error, setError] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)
  const [confirmDelete, setConfirmDelete] = useState(false)
  // Silme reddedilince onay düğmeleri kalkar; odak pencerenin dışına düşmesin diye silme düğmesine döner.
  const deleteButton = useRef<HTMLButtonElement>(null)

  // Kendisi ve altındaki organizasyonlar seçilemez (ağaçta döngü olmaz); seçenekler ağaç sırasında, girintili.
  const parentOptions = useMemo<SelectOption[]>(() => {
    const allowed = new Set(parentChoices(orgs, organization.id).map((o) => o.id))
    return [
      { value: ROOT, label: 'Yok (kök organizasyon)' },
      ...buildTree(orgs)
        .filter((r) => allowed.has(r.org.id))
        .map((r) => ({ value: r.org.id, label: r.org.name, depth: r.depth, keywords: r.path.join(' ') })),
    ]
  }, [orgs, organization.id])

  async function handleSave(e: FormEvent) {
    e.preventDefault()
    setBusy(true)
    setError(null)
    const nextParent = parent === ROOT ? '' : parent
    try {
      await organizationsApi.update(organization.id, {
        name,
        address,
        // Yalnızca değiştiyse gönderilir: değişmeyen üst şirket için "taşıma" isteği (ve denetim kaydı) oluşmasın.
        ...(nextParent !== (organization.parent_organization_id ?? '') ? { parent_organization_id: nextParent === '' ? null : nextParent } : {}),
      })
      onSaved()
      onClose()
    } catch (err) {
      setError(err instanceof Error ? err.message : 'kaydedilemedi')
    } finally {
      setBusy(false)
    }
  }

  async function handleDelete() {
    setBusy(true)
    setError(null)
    try {
      await organizationsApi.remove(organization.id)
      onClose()
      onDeleted()
    } catch (err) {
      setError(err instanceof Error ? err.message : 'silinemedi')
      setConfirmDelete(false)
      requestAnimationFrame(() => deleteButton.current?.focus())
    } finally {
      setBusy(false)
    }
  }

  return (
    <div>
      {error && <div className="error-banner">{error}</div>}
      {can('organization.update') && (
        <form onSubmit={handleSave}>
          <div className="form-row">
            <label htmlFor="os-name">Ad</label>
            <input id="os-name" value={name} onChange={(e) => setName(e.target.value)} required />
          </div>
          <div className="form-row">
            <label htmlFor="os-address">Adres</label>
            <textarea id="os-address" rows={2} value={address} onChange={(e) => setAddress(e.target.value)} maxLength={1000} />
          </div>
          <div className="form-row">
            <FieldLabel htmlFor="os-parent" label="Üst şirket" hint="Kendisi ve altındaki organizasyonlar seçilemez (ağaçta döngü olmaz)." />
            <SearchSelect id="os-parent" label="Üst şirket" options={parentOptions} value={parent} onChange={setParent} placeholder="Üst şirket ara ya da seç…" />
          </div>
          <div className="form-actions">
            <button className="btn btn-primary" type="submit" disabled={busy}>
              <Save size={15} strokeWidth={1.9} />
              Kaydet
            </button>
            <button className="btn" type="button" onClick={onClose} disabled={busy}>
              Vazgeç
            </button>
          </div>
        </form>
      )}

      {can('organization.delete') && (
        <div className="danger-zone">
          <h3 className="danger-zone-title">
            <TriangleAlert size={15} strokeWidth={1.9} />
            Tehlikeli bölge
          </h3>
          <p className="card-desc">
            Organizasyonu silmek iletişim kişilerini, bildirim kurallarını ve eşiklerini de siler; alt organizasyonu ya da sunucusu varsa silinemez.
          </p>
          {confirmDelete ? (
            <div className="form-actions">
              <span className="muted">{organization.name} silinsin mi?</span>
              <button className="btn btn-danger" type="button" onClick={handleDelete} disabled={busy}>
                <Trash2 size={15} strokeWidth={1.9} />
                Evet, sil
              </button>
              <button className="btn" type="button" onClick={() => setConfirmDelete(false)} disabled={busy}>
                Vazgeç
              </button>
            </div>
          ) : (
            <button ref={deleteButton} className="btn btn-danger" type="button" onClick={() => setConfirmDelete(true)} disabled={busy}>
              <Trash2 size={15} strokeWidth={1.9} />
              Organizasyonu sil
            </button>
          )}
        </div>
      )}
    </div>
  )
}
