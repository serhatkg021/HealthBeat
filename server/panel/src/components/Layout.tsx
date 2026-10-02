import { useEffect, useState } from 'react'
import { NavLink, Outlet, useLocation } from 'react-router-dom'
import { Activity, Bell, Building2, ChevronDown, LayoutDashboard, Menu, Server, Settings, Users, Wrench, X, type LucideIcon } from 'lucide-react'
import { useAuth } from '../auth/AuthContext'
import { SETTINGS_PATH, TOOLS_PATH, groupHasActive, inSettingsArea, inToolsArea, navigation, type NavGroup, type NavItem } from '../navigation'
import { pageTitle } from '../pageTitle'
import { versionInfo } from '../versionInfo'
import { AlertCounters } from './AlertCounters'
import { HeaderSlotContext } from './headerSlot'
import { ProfileMenu } from './ProfileMenu'
import { SystemTicker } from './SystemTicker'
import { useServerVersion } from './useAgentPolicy'
import { useDocumentTitle } from './useDocumentTitle'
import { useOpenAlertCounts } from './useOpenAlertCounts'
import { useScrollStrips } from './useScrollStrips'
import { useSystemNotices } from './useSystemNotices'

const ICONS: Record<string, LucideIcon> = {
  ozet: LayoutDashboard,
  sunucularim: Server,
  alertler: Bell,
  organizasyonlar: Building2,
  kullanicilar: Users,
}

const COLLAPSED_KEY = 'healthbeat_nav_collapsed'

function loadCollapsed(): string[] {
  try {
    const parsed: unknown = JSON.parse(localStorage.getItem(COLLAPSED_KEY) ?? '[]')
    return Array.isArray(parsed) ? parsed.filter((x): x is string => typeof x === 'string') : []
  } catch {
    return []
  }
}

function SideLink({ item }: { item: NavItem }) {
  const Icon = ICONS[item.id]
  return (
    <NavLink to={item.to} end={item.end} className={({ isActive }) => `sidebar-link${isActive ? ' active' : ''}`}>
      {Icon && <Icon size={16} strokeWidth={1.75} />}
      {item.label}
    </NavLink>
  )
}

// Açılır menü grubu. Kapalı tercih tarayıcıda saklanır; grubun bir sayfası açıkken grup açık görünür.
function SideGroup({ group, collapsed, onToggle }: { group: NavGroup; collapsed: boolean; onToggle: () => void }) {
  const active = groupHasActive(group, useLocation().pathname)
  const open = !collapsed || active
  const listId = `nav-group-${group.id}`
  return (
    <div className="sidebar-section">
      <button type="button" className="sidebar-group" aria-expanded={open} aria-controls={listId} onClick={onToggle}>
        {group.label}
        <ChevronDown size={14} strokeWidth={2} aria-hidden="true" />
      </button>
      <div id={listId} className="sidebar-group-items" hidden={!open}>
        {group.items.map((item) => (
          <SideLink key={item.id} item={item} />
        ))}
      </div>
    </div>
  )
}

export function Layout() {
  const { user, can } = useAuth()
  const { pathname } = useLocation()
  const [navOpen, setNavOpen] = useState(false)
  const [collapsed, setCollapsed] = useState(loadCollapsed)
  const [titleSlot, setTitleSlot] = useState<HTMLElement | null>(null)
  // Detay sayfaları (sunucu, organizasyon) başlığı kendileri verir; burada yalnızca sabit rotalar.
  useDocumentTitle(pageTitle(pathname))
  useScrollStrips()
  const version = versionInfo(typeof __PANEL_VERSION__ === 'string' ? __PANEL_VERSION__ : 'dev', useServerVersion())
  const counts = useOpenAlertCounts(!!user && can('alert.view') && can('dashboard.view'))
  const notices = useSystemNotices(!!user && can('settings.view'), version.mismatch ? version.title : null)

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

  const nav = navigation(can)

  function toggleGroup(id: string) {
    setCollapsed((prev) => {
      const next = prev.includes(id) ? prev.filter((x) => x !== id) : [...prev, id]
      localStorage.setItem(COLLAPSED_KEY, JSON.stringify(next))
      return next
    })
  }

  return (
    <div className="app-shell">
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

        {nav.top.map((item) => (
          <SideLink key={item.id} item={item} />
        ))}
        {nav.groups.map((group) => (
          <SideGroup key={group.id} group={group} collapsed={collapsed.includes(group.id)} onToggle={() => toggleGroup(group.id)} />
        ))}

        {(nav.tools.length > 0 || nav.settings.length > 0) && (
          <div className="sidebar-footer">
            {nav.tools.length > 0 && (
              <NavLink to={TOOLS_PATH} className={`sidebar-link${inToolsArea(pathname) ? ' active' : ''}`}>
                <Wrench size={16} strokeWidth={1.75} />
                Sistem Araçları
              </NavLink>
            )}
            {nav.settings.length > 0 && (
              <NavLink to={SETTINGS_PATH} className={`sidebar-link${inSettingsArea(pathname) ? ' active' : ''}`}>
                <Settings size={16} strokeWidth={1.75} />
                Ayarlar
              </NavLink>
            )}
          </div>
        )}
      </nav>

      <div className="shell-body">
        <header className="topbar">
          <button
            type="button"
            className="icon-btn nav-toggle"
            aria-label="Menüyü aç"
            aria-expanded={navOpen}
            aria-controls="sidebar"
            onClick={() => setNavOpen(true)}
          >
            <Menu size={20} strokeWidth={1.75} />
          </button>
          <div className="topbar-title" ref={setTitleSlot} />
          {counts && <AlertCounters counts={counts} />}
          <ProfileMenu />
        </header>

        <main className="main">
          <HeaderSlotContext.Provider value={titleSlot}>
            <Outlet />
          </HeaderSlotContext.Provider>
        </main>

        <footer className="statusbar">
          <span className={`version-note${version.mismatch ? ' mismatch' : ''}`} title={version.title}>
            {version.label}
          </span>
          <SystemTicker notices={notices} />
        </footer>
      </div>
    </div>
  )
}
