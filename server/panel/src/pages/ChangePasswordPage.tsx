import { useState, type FormEvent } from 'react'
import { Navigate, useNavigate } from 'react-router-dom'
import { useAuth } from '../auth/AuthContext'
import { useDocumentTitle } from '../components/useDocumentTitle'
import { validateNewPassword } from './password'
import { KeyRound } from 'lucide-react'

// Kenar çubuğundan herkes tarafından ulaşılır ve şifresi başka bir şeyden önce değiştirilmesi
// gereken hesaplar için (bkz. App.tsx'teki RequireAuth) zorunlu tutulur — ör. ilk yönetici.
// Bilerek kenar çubuğu olmadan çizilir.
export function ChangePasswordPage() {
  const { user, changePassword, logout } = useAuth()
  useDocumentTitle('Şifre değiştir')
  const navigate = useNavigate()
  const [current, setCurrent] = useState('')
  const [next, setNext] = useState('')
  const [confirm, setConfirm] = useState('')
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState<string | null>(null)

  if (!user) return <Navigate to="/login" replace />
  const forced = user.must_change_password === true

  async function handleSubmit(e: FormEvent) {
    e.preventDefault()
    const problem = validateNewPassword(current, next, confirm)
    if (problem) {
      setError(problem)
      return
    }
    setSaving(true)
    setError(null)
    try {
      await changePassword(current, next)
      navigate('/', { replace: true })
    } catch (err) {
      setError(err instanceof Error ? err.message : 'şifre değiştirilemedi')
      setSaving(false)
    }
  }

  return (
    <div className="login-shell">
      <form className="card login-card" onSubmit={handleSubmit}>
        <div className="login-brand">
          <span className="brand-mark">
            <KeyRound size={20} strokeWidth={2} />
          </span>
          <h1>{forced ? 'Yeni şifre belirleyin' : 'Şifre değiştir'}</h1>
          {forced && <p>Bu hesap için geçici bir şifre kullanıldı. Devam etmeden önce kendi şifrenizi belirleyin.</p>}
        </div>
        {error && <div className="error-banner">{error}</div>}
        <div className="form-row">
          <label htmlFor="current-password">Mevcut şifre</label>
          <input
            id="current-password"
            type="password"
            autoComplete="current-password"
            value={current}
            onChange={(e) => setCurrent(e.target.value)}
            required
          />
        </div>
        <div className="form-row">
          <label htmlFor="new-password">Yeni şifre (en az 12 karakter)</label>
          <input
            id="new-password"
            type="password"
            autoComplete="new-password"
            value={next}
            onChange={(e) => setNext(e.target.value)}
            required
          />
        </div>
        <div className="form-row">
          <label htmlFor="confirm-password">Yeni şifre (tekrar)</label>
          <input
            id="confirm-password"
            type="password"
            autoComplete="new-password"
            value={confirm}
            onChange={(e) => setConfirm(e.target.value)}
            required
          />
        </div>
        <div className="form-actions">
          <button className="btn btn-primary" type="submit" disabled={saving}>
            {saving ? 'Kaydediliyor…' : 'Şifreyi değiştir'}
          </button>
          {forced ? (
            <button className="btn" type="button" onClick={logout} disabled={saving}>
              Çıkış yap
            </button>
          ) : (
            <button className="btn" type="button" onClick={() => navigate('/')} disabled={saving}>
              Vazgeç
            </button>
          )}
        </div>
      </form>
    </div>
  )
}
