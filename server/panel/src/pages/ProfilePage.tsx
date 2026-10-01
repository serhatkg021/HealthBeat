import { useState, type FormEvent } from 'react'
import { Link } from 'react-router-dom'
import { KeyRound, Save, UserRound } from 'lucide-react'
import { useAuth } from '../auth/AuthContext'
import { PageHeader } from '../components/PageHeader'
import { roleLabel } from '../labels'
import { MAX_FULL_NAME, MAX_PHONE, profilePatch, validateProfile } from './profile'

const when = (iso?: string) => (iso ? new Date(iso).toLocaleString() : '—')

// Kişinin kendi profili: görünen ad ve telefon düzenlenir; e-posta ve rolü yalnızca bir yönetici değiştirir.
export function ProfilePage() {
  const { user, updateProfile } = useAuth()
  const [fullName, setFullName] = useState(user?.full_name ?? '')
  const [phone, setPhone] = useState(user?.phone ?? '')
  const [saving, setSaving] = useState(false)
  const [saved, setSaved] = useState(false)
  const [error, setError] = useState<string | null>(null)

  if (!user) return null
  const patch = profilePatch(user, fullName, phone)

  async function handleSubmit(e: FormEvent) {
    e.preventDefault()
    if (!patch) return
    const problem = validateProfile(fullName, phone)
    if (problem) {
      setError(problem)
      return
    }
    setSaving(true)
    setError(null)
    setSaved(false)
    try {
      const me = await updateProfile(patch)
      setFullName(me.full_name ?? '')
      setPhone(me.phone ?? '')
      setSaved(true)
    } catch (err) {
      setError(err instanceof Error ? err.message : 'profil güncellenemedi')
    } finally {
      setSaving(false)
    }
  }

  return (
    <div className="page-readable">
      <PageHeader title="Profil" subtitle="Hesap bilgileriniz; ad ve telefonu kendiniz değiştirebilirsiniz" />
      {error && <div className="error-banner">{error}</div>}

      <form className="card form-card" onSubmit={handleSubmit}>
        <h2 className="card-title">
          <UserRound size={16} strokeWidth={1.75} />
          Bilgilerim
        </h2>
        <div className="form-row">
          <label htmlFor="profile-name">Ad soyad</label>
          <input
            id="profile-name"
            value={fullName}
            onChange={(e) => { setFullName(e.target.value); setSaved(false) }}
            maxLength={MAX_FULL_NAME}
            autoComplete="name"
          />
        </div>
        <div className="form-row">
          <label htmlFor="profile-phone">Telefon</label>
          <input
            id="profile-phone"
            value={phone}
            onChange={(e) => { setPhone(e.target.value); setSaved(false) }}
            inputMode="tel"
            maxLength={MAX_PHONE}
            autoComplete="tel"
          />
        </div>
        <div className="form-actions">
          <button className="btn btn-primary" type="submit" disabled={saving || !patch}>
            <Save size={15} strokeWidth={1.75} />
            {saving ? 'Kaydediliyor…' : 'Kaydet'}
          </button>
          {saved && <span className="muted save-note">Kaydedildi.</span>}
        </div>
      </form>

      <div className="card">
        <h2 className="card-title">Hesap</h2>
        <dl className="profile-facts">
          <div>
            <dt>E-posta</dt>
            <dd>{user.email}</dd>
          </div>
          <div>
            <dt>Rol</dt>
            <dd>{roleLabel(user.role)}</dd>
          </div>
          <div>
            <dt>Son giriş</dt>
            <dd className="tnum">{when(user.last_login_at)}</dd>
          </div>
          <div>
            <dt>Hesap oluşturulma</dt>
            <dd className="tnum">{when(user.created_at)}</dd>
          </div>
        </dl>
        <p className="form-hint">E-posta ve rolü yalnızca bir yönetici değiştirebilir.</p>
        <Link to="/change-password" className="btn">
          <KeyRound size={15} strokeWidth={1.75} />
          Şifre değiştir
        </Link>
      </div>
    </div>
  )
}
