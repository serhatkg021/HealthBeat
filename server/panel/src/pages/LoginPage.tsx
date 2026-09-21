import { useState, type FormEvent } from 'react'
import { Link, Navigate } from 'react-router-dom'
import { Activity } from 'lucide-react'
import { useAuth } from '../auth/AuthContext'
import { useDocumentTitle } from '../components/useDocumentTitle'

export function LoginPage() {
  const { user, login, loading, error } = useAuth()
  useDocumentTitle('Giriş')
  const [email, setEmail] = useState('')
  const [password, setPassword] = useState('')

  if (user) return <Navigate to="/" replace />

  async function handleSubmit(e: FormEvent) {
    e.preventDefault()
    try {
      await login(email, password)
    } catch {
      // hata zaten auth context durumu üzerinden gösteriliyor
    }
  }

  return (
    <div className="login-shell">
      <form className="card login-card" onSubmit={handleSubmit}>
        <div className="login-brand">
          <span className="brand-mark">
            <Activity size={22} strokeWidth={2.25} />
          </span>
          <h1>HealthBeat</h1>
          <p>Sunucu izleme paneline giriş yapın</p>
        </div>
        {error && <div className="error-banner">{error}</div>}
        <div className="form-row">
          <label htmlFor="email">E-posta</label>
          <input
            id="email"
            type="email"
            autoComplete="username"
            value={email}
            onChange={(e) => setEmail(e.target.value)}
            required
          />
        </div>
        <div className="form-row">
          <label htmlFor="password">Şifre</label>
          <input
            id="password"
            type="password"
            autoComplete="current-password"
            value={password}
            onChange={(e) => setPassword(e.target.value)}
            required
          />
          <div className="login-forgot">
            <Link to="/forgot-password">Şifremi unuttum</Link>
          </div>
        </div>
        <button className="btn btn-primary btn-block" type="submit" disabled={loading}>
          {loading ? 'Giriş yapılıyor...' : 'Giriş yap'}
        </button>
      </form>
    </div>
  )
}
