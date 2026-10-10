import { useEffect, useRef, useState } from 'react'
import { Link } from 'react-router-dom'
import { ChevronDown, KeyRound, LogOut, UserRound } from 'lucide-react'
import { useAuth } from '../auth/AuthContext'
import { roleLabel } from '../labels'
import { ThemeChoices } from './ThemeToggle'

// Üst çubuğun sağındaki hesap düğmesi; tıklanınca hesap bilgisi, profil, şifre değiştirme ve çıkış açılır. Dışarı tıklamak ya da
// Escape kapatır.
export function ProfileMenu() {
  const { user, logout } = useAuth()
  const [open, setOpen] = useState(false)
  const root = useRef<HTMLDivElement>(null)

  useEffect(() => {
    if (!open) return
    const onDown = (e: MouseEvent) => {
      if (!root.current?.contains(e.target as Node)) setOpen(false)
    }
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') setOpen(false)
    }
    document.addEventListener('mousedown', onDown)
    document.addEventListener('keydown', onKey)
    return () => {
      document.removeEventListener('mousedown', onDown)
      document.removeEventListener('keydown', onKey)
    }
  }, [open])

  if (!user) return null
  const name = user.full_name?.trim() || user.email

  return (
    <div className="profile-menu" ref={root}>
      <button
        type="button"
        className="profile-button"
        aria-haspopup="menu"
        aria-expanded={open}
        aria-label={`Hesap: ${name}`}
        onClick={() => setOpen((o) => !o)}
      >
        <span className="avatar" aria-hidden="true">
          {name.charAt(0)}
        </span>
        <span className="profile-button-name">{name}</span>
        <ChevronDown size={14} strokeWidth={2} aria-hidden="true" />
      </button>
      {open && (
        <div className="profile-popover" role="menu">
          <div className="profile-popover-head">
            <div className="profile-popover-name" title={user.email}>
              {name}
            </div>
            {name !== user.email && <div className="profile-popover-meta">{user.email}</div>}
            <div className="profile-popover-meta">{roleLabel(user.role)}</div>
          </div>
          <div className="profile-theme">
            <span className="profile-popover-meta">Tema</span>
            <ThemeChoices />
          </div>
          <Link to="/profile" role="menuitem" className="profile-item" onClick={() => setOpen(false)}>
            <UserRound size={15} strokeWidth={1.75} />
            Profil
          </Link>
          <Link to="/change-password" role="menuitem" className="profile-item" onClick={() => setOpen(false)}>
            <KeyRound size={15} strokeWidth={1.75} />
            Şifre değiştir
          </Link>
          <button type="button" role="menuitem" className="profile-item" onClick={logout}>
            <LogOut size={15} strokeWidth={1.75} />
            Çıkış yap
          </button>
        </div>
      )}
    </div>
  )
}
