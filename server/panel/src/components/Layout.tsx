import { useEffect, useState } from 'react'
import { NavLink, Outlet, useLocation } from 'react-router-dom'
import {
  Activity,
  Bell,
  Building2,
  KeyRound,
  LayoutDashboard,
  LogOut,
  Menu,
  ScrollText,
  Server,
  SlidersHorizontal,
  Users,
  X,
  type LucideIcon,
} from 'lucide-react'
import { useAuth } from '../auth/AuthContext'
import { pageTitle } from '../pageTitle'
import { versionInfo } from '../versionInfo'
import { useServerVersion } from './useAgentPolicy'
import { useDocumentTitle } from './useDocumentTitle'

const ROLE_LABELS: Record<string, string> = {
  super_admin: 'Süper Admin',
  org_admin: 'Organizasyon Admin',
  operator: 'Operatör',
}

function SideLink({ to, icon: Icon, end, children }: { to: string; icon: LucideIcon; end?: boolean; children: string }) {
  return (
    <NavLink to={to} end={end} className={({ isActive }) => `sidebar-link${isActive ? ' active' : ''}`}>
      <Icon size={16} strokeWidth={1.75} />
      {children}
    </NavLink>
  )
}

export function Layout() {
  const { user, logout } = useAuth()
  const [navOpen, setNavOpen] = useState(false)
  // Detay sayfaları (sunucu, organizasyon) başlığı kendileri verir; burada yalnızca sabit rotalar.
  useDocumentTitle(pageTitle(useLocation().pathname))
  const version = versionInfo(typeof __PANEL_VERSION__ === 'string' ? __PANEL_VERSION__ : 'dev', useServerVersion())

  useEffect(() => {
    document.body.classList.toggle('nav-open', navOpen)
    if (!navOpen) return
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') setNavOpen(false)
    }
    window.addEventListener('keydown', onKey)
    return () => {
      window.removeEventListener('keydown', onKey)
      document.body.classList.remove('nav-open')
    }
  }, [navOpen])

  if (!user) return null

  const canSeeOrganizations = user.role === 'super_admin' || user.role === 'org_admin'
  const canSeeUsers = user.role === 'super_admin'

  return (
    <div className="app-shell">
      <header className="topbar">
        <button
          type="button"
          className="icon-btn"
          aria-label="Menüyü aç"
          aria-expanded={navOpen}
          aria-controls="sidebar"
          onClick={() => setNavOpen(true)}
        >
          <Menu size={20} strokeWidth={1.75} />
        </button>
        <div className="brand">
          <span className="brand-mark">
            <Activity size={16} strokeWidth={2.25} />
          </span>
          HealthBeat
        </div>
      </header>
      <div className={`scrim${navOpen ? ' open' : ''}`} onClick={() => setNavOpen(false)} aria-hidden="true" />

      <nav
        id="sidebar"
        className={`sidebar${navOpen ? ' open' : ''}`}
        aria-label="Ana menü"
        onClick={(e) => {
          if ((e.target as HTMLElement).closest('a')) setNavOpen(false)
        }}
      >
        <div className="brand" style={{ justifyContent: 'space-between' }}>
          <span style={{ display: 'flex', alignItems: 'center', gap: 10 }}>
            <span className="brand-mark">
              <Activity size={16} strokeWidth={2.25} />
            </span>
            HealthBeat
          </span>
          {navOpen && (
            <button type="button" className="icon-btn" aria-label="Menüyü kapat" onClick={() => setNavOpen(false)}>
              <X size={18} strokeWidth={1.75} />
            </button>
          )}
        </div>

        <div className="sidebar-group">İzleme</div>
        <SideLink to="/" end icon={LayoutDashboard}>
          Özet
        </SideLink>
        {!canSeeOrganizations && (
          <SideLink to="/my-hosts" icon={Server}>
            Sunucularım
          </SideLink>
        )}
        <SideLink to="/alerts" icon={Bell}>
          Alert'ler
        </SideLink>

        <div className="sidebar-group">Yönetim</div>
        {canSeeOrganizations && (
          <SideLink to="/organizations" icon={Building2}>
            Organizasyonlar
          </SideLink>
        )}
        <SideLink to="/thresholds" icon={SlidersHorizontal}>
          Eşikler
        </SideLink>
        {canSeeUsers && (
          <SideLink to="/users" icon={Users}>
            Kullanıcılar
          </SideLink>
        )}
        {canSeeUsers && (
          <SideLink to="/audit" icon={ScrollText}>
            Denetim Kaydı
          </SideLink>
        )}

        <div className="sidebar-footer">
          <div className={`version-note${version.mismatch ? ' mismatch' : ''}`} title={version.title}>
            <span>Panel {version.panel}</span>
            <span aria-hidden="true">·</span>
            <span>Server {version.server}</span>
            {version.mismatch && <span className="visually-hidden"> — {version.title}</span>}
          </div>
          <div className="user-chip">
            <span className="avatar" aria-hidden="true">
              {user.email.charAt(0)}
            </span>
            <div className="user-chip-text">
              <div className="user-chip-email" title={user.email}>
                {user.email}
              </div>
              <div className="user-chip-role">{ROLE_LABELS[user.role] ?? user.role}</div>
            </div>
          </div>
          <SideLink to="/change-password" icon={KeyRound}>
            Şifre değiştir
          </SideLink>
          <button type="button" className="sidebar-link sidebar-button" onClick={logout}>
            <LogOut size={16} strokeWidth={1.75} />
            Çıkış yap
          </button>
        </div>
      </nav>

      <main className="main">
        <Outlet />
      </main>
    </div>
  )
}
