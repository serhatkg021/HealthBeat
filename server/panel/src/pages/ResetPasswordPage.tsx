import { useEffect, useState, type FormEvent } from 'react'
import { Link } from 'react-router-dom'
import { Check, KeyRound } from 'lucide-react'
import { authApi } from '../api/endpoints'
import { ApiError } from '../api/client'
import { useDocumentTitle } from '../components/useDocumentTitle'
import { tokenFromHash, validateResetPassword } from './passwordReset'

// E-postadaki bağlantının açtığı sayfa ("/reset-password#token=…"): yeni şifre seçilir. Token adres çubuğundan ve
// geçmişten kaldırılır (sayfa yeniden yüklenirse bağlantı e-postadan yeniden açılır). Başarıda tüm oturumlar
// server'da kapanır; kullanıcı yeni şifresiyle giriş yapar.
export function ResetPasswordPage() {
  useDocumentTitle('Yeni şifre')
  // Token yalnızca ilk render'da okunur; adres temizlendikten sonra da state'te kalır.
  const [token] = useState(() => tokenFromHash(window.location.hash))
  const [next, setNext] = useState('')
  const [confirm, setConfirm] = useState('')
  const [saving, setSaving] = useState(false)
  const [done, setDone] = useState(false)
  const [error, setError] = useState<string | null>(null)
  // Server "bağlantı geçersiz/süresi dolmuş" dediyse yeni bağlantı istemek dışında yapılacak bir şey yoktur.
  const [linkDead, setLinkDead] = useState(false)

  useEffect(() => {
    if (window.location.hash) window.history.replaceState(null, '', window.location.pathname)
  }, [])

  async function handleSubmit(e: FormEvent) {
    e.preventDefault()
    const problem = validateResetPassword(next, confirm)
    if (problem) {
      setError(problem)
      return
    }
    setSaving(true)
    setError(null)
    try {
      await authApi.resetPassword(token, next)
      setDone(true)
    } catch (err) {
      const message = err instanceof Error ? err.message : 'şifre değiştirilemedi'
      setError(message)
      if (err instanceof ApiError && err.code === 'reset_link_invalid') setLinkDead(true)
    } finally {
      setSaving(false)
    }
  }

  const invalidLink = token === '' || linkDead

  return (
    <div className="login-shell">
      <div className="card login-card">
        <div className="login-brand">
          <span className="brand-mark">
            <KeyRound size={20} strokeWidth={2} />
          </span>
          <h1>Yeni şifre belirleyin</h1>
        </div>

        {done ? (
          <>
            <div className="save-note" role="status">
              <Check size={14} strokeWidth={2.2} />
              Şifreniz değiştirildi. Yeni şifrenizle giriş yapabilirsiniz.
            </div>
            <Link className="btn btn-primary btn-block" to="/login">
              Giriş yap
            </Link>
          </>
        ) : invalidLink ? (
          <>
            <div className="error-banner" role="alert">
              {error ?? 'Sıfırlama bağlantısı geçersiz ya da eksik.'} Bağlantılar 30 dakika geçerlidir ve yalnızca bir kez kullanılabilir.
            </div>
            <Link className="btn btn-primary btn-block" to="/forgot-password">
              Yeni bağlantı iste
            </Link>
          </>
        ) : (
          <form onSubmit={handleSubmit}>
            {error && <div className="error-banner">{error}</div>}
            <div className="form-row">
              <label htmlFor="reset-new">Yeni şifre (en az 12 karakter)</label>
              <input
                id="reset-new"
                type="password"
                autoComplete="new-password"
                value={next}
                onChange={(e) => setNext(e.target.value)}
                required
              />
            </div>
            <div className="form-row">
              <label htmlFor="reset-confirm">Yeni şifre (tekrar)</label>
              <input
                id="reset-confirm"
                type="password"
                autoComplete="new-password"
                value={confirm}
                onChange={(e) => setConfirm(e.target.value)}
                required
              />
            </div>
            <button className="btn btn-primary btn-block" type="submit" disabled={saving}>
              {saving ? 'Kaydediliyor...' : 'Şifreyi değiştir'}
            </button>
          </form>
        )}

        {!done && (
          <div className="login-links">
            <Link to="/login">Girişe dön</Link>
          </div>
        )}
      </div>
    </div>
  )
}
