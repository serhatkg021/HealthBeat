import { useEffect, useState, type FormEvent } from 'react'
import { Link, Navigate } from 'react-router-dom'
import { Check, MailQuestion } from 'lucide-react'
import { authApi } from '../api/endpoints'
import { ApiError } from '../api/client'
import { useAuth } from '../auth/AuthContext'
import { useDocumentTitle } from '../components/useDocumentTitle'
import { plausibleEmail } from './passwordReset'

// "Şifremi unuttum": e-posta adresi girilir, server tek kullanımlık bir sıfırlama bağlantısı e-postalar. Yanıt her
// zaman aynıdır (hesabın var olup olmadığı dışarıdan anlaşılmaz). Kurulumda e-posta (SMTP ve PANEL_BASE_URL)
// yapılandırılmamışsa bunu açıkça söyler.
export function ForgotPasswordPage() {
  const { user } = useAuth()
  useDocumentTitle('Şifremi unuttum')
  const [email, setEmail] = useState('')
  const [enabled, setEnabled] = useState<boolean | null>(null) // null = henüz bilinmiyor
  const [sending, setSending] = useState(false)
  const [sent, setSent] = useState(false)
  const [error, setError] = useState<string | null>(null)

  useEffect(() => {
    let cancelled = false
    authApi
      .options()
      .then((o) => !cancelled && setEnabled(o.password_reset_enabled))
      .catch(() => !cancelled && setEnabled(true)) // öğrenilemezse formu göster; server yine de doğru yanıtlar
    return () => {
      cancelled = true
    }
  }, [])

  if (user) return <Navigate to="/" replace />

  async function handleSubmit(e: FormEvent) {
    e.preventDefault()
    if (!plausibleEmail(email)) {
      setError('Geçerli bir e-posta adresi girin.')
      return
    }
    setSending(true)
    setError(null)
    try {
      await authApi.forgotPassword(email.trim())
      setSent(true)
    } catch (err) {
      setError(
        err instanceof ApiError && err.status === 429
          ? 'Çok fazla deneme yapıldı; birkaç dakika sonra tekrar deneyin.'
          : err instanceof Error
            ? err.message
            : 'istek gönderilemedi',
      )
    } finally {
      setSending(false)
    }
  }

  return (
    <div className="login-shell">
      <div className="card login-card">
        <div className="login-brand">
          <span className="brand-mark">
            <MailQuestion size={20} strokeWidth={2} />
          </span>
          <h1>Şifremi unuttum</h1>
          {!sent && enabled !== false && <p>Hesabınızın e-posta adresini girin; şifre sıfırlama bağlantısı gönderelim.</p>}
        </div>

        {enabled === false && (
          <div className="notice notice-info" role="status">
            <div className="notice-title">E-posta ile sıfırlama kapalı</div>
            Bu kurulumda e-posta gönderimi yapılandırılmamış. Şifrenizi sıfırlaması için sistem yöneticinize başvurun. (Yönetici için:
            server’da <code>SMTP_HOST</code> ve <code>PANEL_BASE_URL</code> ayarlanmalı.)
          </div>
        )}

        {sent ? (
          <div className="save-note" role="status">
            <Check size={14} strokeWidth={2.2} />
            Bu e-posta adresiyle kayıtlı bir hesap varsa, şifre sıfırlama bağlantısı gönderildi. Bağlantı 30 dakika geçerlidir; gelen
            kutunuzu ve istenmeyen klasörünü kontrol edin.
          </div>
        ) : (
          enabled !== false && (
            <form onSubmit={handleSubmit}>
              {error && <div className="error-banner">{error}</div>}
              <div className="form-row">
                <label htmlFor="forgot-email">E-posta</label>
                <input
                  id="forgot-email"
                  type="email"
                  autoComplete="username"
                  value={email}
                  onChange={(e) => setEmail(e.target.value)}
                  required
                />
              </div>
              <button className="btn btn-primary btn-block" type="submit" disabled={sending}>
                {sending ? 'Gönderiliyor...' : 'Sıfırlama bağlantısı gönder'}
              </button>
            </form>
          )
        )}

        <div className="login-links">
          <Link to="/login">Girişe dön</Link>
        </div>
      </div>
    </div>
  )
}
