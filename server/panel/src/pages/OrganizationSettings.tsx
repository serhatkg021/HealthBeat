import { useState, type FormEvent } from 'react'
import { useNavigate } from 'react-router-dom'
import { Save, Trash2, TriangleAlert } from 'lucide-react'
import { organizationsApi } from '../api/endpoints'
import type { Organization } from '../types/api'
import { parentChoices } from './orgTree'

// Organizasyonun adı, adresi ve ağaçtaki yeri; silme. Ağacı yalnızca süper admin değiştirir (server ile aynı kural).
export function OrganizationSettings({ organization, orgs, onChanged }: { organization: Organization; orgs: Organization[]; onChanged: () => void }) {
  const navigate = useNavigate()
  const [name, setName] = useState(organization.name)
  const [address, setAddress] = useState(organization.address ?? '')
  const [parent, setParent] = useState(organization.parent_organization_id ?? '')
  const [error, setError] = useState<string | null>(null)
  const [saved, setSaved] = useState(false)
  const [busy, setBusy] = useState(false)

  async function handleSave(e: FormEvent) {
    e.preventDefault()
    setBusy(true)
    setError(null)
    setSaved(false)
    try {
      await organizationsApi.update(organization.id, {
        name,
        address,
        // Yalnızca değiştiyse gönderilir: değişmeyen üst şirket için "taşıma" isteği (ve denetim kaydı) oluşmasın.
        ...(parent !== (organization.parent_organization_id ?? '') ? { parent_organization_id: parent === '' ? null : parent } : {}),
      })
      setSaved(true)
      onChanged()
    } catch (err) {
      setError(err instanceof Error ? err.message : 'kaydedilemedi')
    } finally {
      setBusy(false)
    }
  }

  async function handleDelete() {
    if (!confirm(`${organization.name} silinsin mi? Alt organizasyonu ya da sunucusu olan bir organizasyon silinemez.`)) return
    try {
      await organizationsApi.remove(organization.id)
      navigate('/organizations')
    } catch (err) {
      setError(err instanceof Error ? err.message : 'silinemedi')
    }
  }

  return (
    <div className="page-readable">
      {error && <div className="error-banner">{error}</div>}
      <form className="card form-card" onSubmit={handleSave}>
        <div className="form-row">
          <label htmlFor="os-name">Ad</label>
          <input id="os-name" value={name} onChange={(e) => setName(e.target.value)} required />
        </div>
        <div className="form-row">
          <label htmlFor="os-address">Adres</label>
          <textarea id="os-address" rows={2} value={address} onChange={(e) => setAddress(e.target.value)} maxLength={1000} />
        </div>
        <div className="form-row">
          <label htmlFor="os-parent">Üst şirket</label>
          <select id="os-parent" value={parent} onChange={(e) => setParent(e.target.value)}>
            <option value="">Yok (kök organizasyon)</option>
            {parentChoices(orgs, organization.id).map((o) => (
              <option key={o.id} value={o.id}>
                {o.name}
              </option>
            ))}
          </select>
          <p className="form-hint">Kendisi ve altındaki organizasyonlar seçilemez (ağaçta döngü olmaz).</p>
        </div>
        <button className="btn btn-primary" type="submit" disabled={busy}>
          <Save size={15} strokeWidth={1.9} />
          Kaydet
        </button>
        {saved && <span className="muted"> Kaydedildi.</span>}
      </form>

      <div className="card form-card card-danger">
        <h2 className="card-title">
          <TriangleAlert size={16} strokeWidth={1.9} />
          Tehlikeli bölge
        </h2>
        <p className="card-desc">Organizasyonu silmek iletişim kişilerini, bildirim kurallarını ve eşiklerini de siler; alt organizasyonu ya da sunucusu varsa silinemez.</p>
        <button className="btn btn-danger" type="button" onClick={handleDelete}>
          <Trash2 size={15} strokeWidth={1.9} />
          Organizasyonu sil
        </button>
      </div>
    </div>
  )
}
