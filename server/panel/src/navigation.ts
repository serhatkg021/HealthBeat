// Sol menünün, Ayarlar sayfasının ve Sistem Araçları sayfasının yapısı — saf mantık (ikonlar Layout'ta eşlenir). Menü,
// kullanıcının sorduğu soruya göre üç açılır gruptur: İzleme (ne oluyor?), Alert Yönetimi (ne zaman, kime haber
// verilir?) ve Yönetim; en altta sabit Sistem Araçları ve Ayarlar. Bu ikisi menüde tek bağlantıdır; içerikleri sayfanın
// içinde sekmelerle ayrılır. Her öğe yalnızca izni olan kullanıcıya görünür; hiç öğesi
// kalmayan grup (ve hiç sekmesi kalmayan Ayarlar ya da Sistem Araçları) gösterilmez.
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

// Ayarlar ya da Sistem Araçları sayfasının bir sekmesi.
export interface PageTab {
  id: string
  label: string
}

// Ayarlar sekmesi: description sekmenin altında tek satırlık açıklama olarak gösterilir.
export interface SettingsTab extends PageTab {
  description: string
}

export type ToolTab = PageTab

export interface Navigation {
  groups: NavGroup[]
  tools: ToolTab[]
  settings: SettingsTab[]
}

export const SETTINGS_PATH = '/settings'
export const TOOLS_PATH = '/system'
export const HOSTS_PATH = '/hosts'
export const ALERT_RULES_PATH = '/alert-rules'
export const MAINTENANCE_PATH = '/maintenance'
export const NOTIFICATIONS_PATH = '/notifications'

export function navigation(can: Can): Navigation {
  const item = (show: boolean, def: NavItem): NavItem[] => (show ? [def] : [])
  const tab = (show: boolean, def: SettingsTab): SettingsTab[] => (show ? [def] : [])
  // Organizasyonları göremeyen (operatör) kendisine atanmış sunucuları "Sunucularım"da görür. Sunucular sayfası
  // içeriği taşınınca ikisi tek sayfada birleşir.
  const groups = [
    {
      id: 'izleme',
      label: 'İzleme',
      items: [
        { id: 'ozet', to: '/', label: 'Özet', end: true },
        ...item(can('host.view') && can('organization.view'), { id: 'sunucular', to: HOSTS_PATH, label: 'Sunucular' }),
        ...item(!can('organization.view'), { id: 'sunucularim', to: '/my-hosts', label: 'Sunucularım' }),
        ...item(can('alert.view'), { id: 'alertler', to: '/alerts', label: 'Alert’ler' }),
      ],
    },
    {
      id: 'alert-yonetimi',
      label: 'Alert Yönetimi',
      items: [
        ...item(can('threshold.view'), { id: 'kurallar', to: ALERT_RULES_PATH, label: 'Alert kuralları' }),
        { id: 'bakim', to: MAINTENANCE_PATH, label: 'Bakım pencereleri' },
        ...item(can('notification.view') || can('settings.view'), { id: 'bildirim', to: NOTIFICATIONS_PATH, label: 'Bildirim' }),
      ],
    },
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
    ...(can('system.logs.view') ? [{ id: 'log', label: 'Log Analiz' }] : []),
  ]
  const settings = [
    ...tab(can('audit.view'), {
      id: 'denetim',
      label: 'Denetim Kaydı',
      description: 'Panelde ve API’de yapılan yönetim işlemlerinin geçmişi.',
    }),
    ...tab(can('settings.view'), {
      id: 'sistem',
      label: 'Sistem Ayarları',
      description: 'Sistem sahipleri, bildirim kanalları, agent sürümleri, saklama, oturum ve loglama.',
    }),
  ]
  return { groups, tools, settings }
}

const under = (pathname: string, to: string) => pathname === to || pathname.startsWith(`${to}/`)

// settingsTabPath, Ayarlar sayfasının bir sekmesinin adresidir (bağlantılar ve eski adreslerin yönlendirmesi için).
export const settingsTabPath = (id: string): string => `${SETTINGS_PATH}?sekme=${id}`

// Eski ayrı sayfaların adresleri (yer imleri, eski bağlantılar) artık Ayarlar'ın sekmeleridir.
export const LEGACY_SETTINGS_ROUTES: Record<string, string> = {
  '/audit': 'denetim',
  '/settings/system': 'sistem',
}

// Alert kurallarının kapsamı: sistem varsayılanı, bir organizasyon ya da bir sunucu (id yoksa seçilmemiş).
export type RuleScope = { kind: 'sistem' } | { kind: 'org'; id?: string } | { kind: 'sunucu'; id?: string }

// alertRulesPath, kurallar sayfasının o kapsam seçili adresidir (organizasyon/sunucu sayfalarından ve eski eşik
// adreslerinden gelen bağlantılar için).
export function alertRulesPath(scope: RuleScope = { kind: 'sistem' }): string {
  if (scope.kind === 'sistem') return ALERT_RULES_PATH
  const params = new URLSearchParams({ kapsam: scope.kind })
  if (scope.id) params.set('id', scope.id)
  return `${ALERT_RULES_PATH}?${params}`
}

export function inSettingsArea(pathname: string): boolean {
  return under(pathname, SETTINGS_PATH)
}

export function inToolsArea(pathname: string): boolean {
  return under(pathname, TOOLS_PATH)
}

// Grubun bir sayfası açıksa grup kapatılamaz görünmesin diye açık tutulur.
export function groupHasActive(group: NavGroup, pathname: string): boolean {
  return group.items.some((i) => (i.end ? pathname === i.to : under(pathname, i.to)))
}
