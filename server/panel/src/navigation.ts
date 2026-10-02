// Sol menünün, Ayarlar sayfasının ve Sistem Araçları sayfasının yapısı — saf mantık (ikonlar Layout'ta eşlenir). Menü:
// üstte doğrudan bağlantılar, altında açılır gruplar, en altta sabit Sistem Araçları ve Ayarlar. Her öğe yalnızca izni olan
// kullanıcıya görünür; hiç öğesi kalmayan grup (hiç kartı kalmayan Ayarlar, hiç sekmesi kalmayan Sistem Araçları) gösterilmez.
import type { Permission } from './auth/permissions.ts'

export type Can = (permission: Permission) => boolean

export interface NavItem {
  id: string
  to: string
  label: string
  // Yalnızca tam eşleşmede etkin sayılır (ör. "/").
  end?: boolean
}

export interface NavGroup {
  id: string
  label: string
  items: NavItem[]
}

export interface SettingsCard extends NavItem {
  description: string
}

// Sistem Araçları sayfasının bir sekmesi (menüde tek bağlantıdır; araçlar sayfanın içinde sekmelerle ayrılır).
export interface ToolTab {
  id: string
  label: string
}

export interface Navigation {
  top: NavItem[]
  groups: NavGroup[]
  tools: ToolTab[]
  settings: SettingsCard[]
}

export const SETTINGS_PATH = '/settings'
export const TOOLS_PATH = '/system'

export function navigation(can: Can): Navigation {
  const item = (show: boolean, def: NavItem): NavItem[] => (show ? [def] : [])
  const card = (show: boolean, def: SettingsCard): SettingsCard[] => (show ? [def] : [])
  // Organizasyonları göremeyen (operatör) kendisine atanmış sunucuları "Sunucularım"da görür.
  const top = [
    { id: 'ozet', to: '/', label: 'Özet', end: true },
    ...item(!can('organization.view'), { id: 'sunucularim', to: '/my-hosts', label: 'Sunucularım' }),
    ...item(can('alert.view'), { id: 'alertler', to: '/alerts', label: 'Alert’ler' }),
  ]
  const groups = [
    {
      id: 'yonetim',
      label: 'Yönetim',
      items: [
        ...item(can('organization.view'), { id: 'organizasyonlar', to: '/organizations', label: 'Organizasyonlar' }),
        ...item(can('user.view'), { id: 'kullanicilar', to: '/users', label: 'Kullanıcılar' }),
      ],
    },
  ].filter((g) => g.items.length > 0)
  // Her aracın kendi izni vardır (system.*; varsayılan olarak yalnızca süper admin).
  const tools = [
    ...(can('system.queue.view') ? [{ id: 'kuyruk', label: 'Kuyruk Durumu' }] : []),
    ...(can('system.cache.view') ? [{ id: 'cache', label: 'Cache Durumu' }] : []),
  ]
  const settings = [
    ...card(can('threshold.view'), {
      id: 'esikler',
      to: '/thresholds',
      label: 'Sistem Eşikleri',
      description: 'Kendi değeri olmayan tüm sunucuların kullandığı varsayılan alert eşikleri.',
    }),
    ...card(can('audit.view'), {
      id: 'denetim',
      to: '/audit',
      label: 'Denetim Kaydı',
      description: 'Panelde ve API’de yapılan yönetim işlemlerinin geçmişi.',
    }),
    ...card(can('settings.view'), {
      id: 'sistem',
      to: '/settings/system',
      label: 'Sistem Ayarları',
      description: 'Sistem sahipleri, bildirim kanalları, agent sürümleri, saklama, oturum ve loglama.',
    }),
  ]
  return { top, groups, tools, settings }
}

const under = (pathname: string, to: string) => pathname === to || pathname.startsWith(`${to}/`)

// Ayarlar bağlantısı, Ayarlar sayfasında ve oradan açılan sayfalarda (eşikler, denetim kaydı, sistem ayarları) etkindir.
export function inSettingsArea(pathname: string): boolean {
  return under(pathname, SETTINGS_PATH) || under(pathname, '/thresholds') || under(pathname, '/audit')
}

export function inToolsArea(pathname: string): boolean {
  return under(pathname, TOOLS_PATH)
}

// Grubun bir sayfası açıksa grup kapatılamaz görünmesin diye açık tutulur.
export function groupHasActive(group: NavGroup, pathname: string): boolean {
  return group.items.some((i) => (i.end ? pathname === i.to : under(pathname, i.to)))
}
