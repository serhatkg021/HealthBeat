// Sol menünün, Ayarlar sayfasının ve Sistem Araçları sayfasının yapısı — saf mantık (ikonlar Layout'ta eşlenir). Menü,
// kullanıcının sorduğu soruya göre üç açılır gruptur: İzleme (ne oluyor?), Alert Yönetimi (ne zaman, kime haber
// verilir?) ve Yönetim; en altta sabit Sistem Araçları ve Ayarlar. Bu ikisi menüde tek bağlantıdır; içerikleri sayfanın
// içinde sekmelerle ayrılır. Her öğe yalnızca izni olan kullanıcıya görünür; hiç öğesi
// kalmayan grup (ve hiç sekmesi kalmayan Ayarlar ya da Sistem Araçları) gösterilmez.
import type { Permission } from './auth/permissions.ts'
import type { TopicId } from './pages/ruleTopics.ts'

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

export type NotificationTab = PageTab & { id: NotificationTabId }
export type NotificationTabId = 'kanallar' | 'sahipler' | 'kurallar' | 'kisiler'

export interface Navigation {
  groups: NavGroup[]
  tools: ToolTab[]
  settings: SettingsTab[]
  notifications: NotificationTab[]
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
  // Bildirim sayfasının sekmeleri: kanallar ve sistem sahipleri kurulum genelidir (settings.view), kurallar ve iletişim
  // kişileri organizasyon/sunucu kapsamındadır. Hiç sekmesi kalmayan kullanıcı menüde Bildirim'i görmez.
  const notifications: NotificationTab[] = [
    ...(can('settings.view') ? [{ id: 'kanallar' as const, label: 'Kanallar' }] : []),
    ...(can('settings.view') ? [{ id: 'sahipler' as const, label: 'Sistem sahipleri' }] : []),
    ...(can('notification.view') ? [{ id: 'kurallar' as const, label: 'Bildirim kuralları' }] : []),
    ...(can('contact.view') ? [{ id: 'kisiler' as const, label: 'İletişim kişileri' }] : []),
  ]
  // Sunucular herkese tek sayfadır; liste yetkiye göre süzülü gelir (operatöre yalnızca atanmış sunucular).
  const groups = [
    {
      id: 'izleme',
      label: 'İzleme',
      items: [
        { id: 'ozet', to: '/', label: 'Özet', end: true },
        ...item(can('host.view'), { id: 'sunucular', to: HOSTS_PATH, label: 'Sunucular' }),
        ...item(can('alert.view'), { id: 'alertler', to: '/alerts', label: 'Alert’ler' }),
      ],
    },
    {
      id: 'alert-yonetimi',
      label: 'Alert Yönetimi',
      items: [
        ...item(can('threshold.view'), { id: 'kurallar', to: ALERT_RULES_PATH, label: 'Alert kuralları' }),
        { id: 'bakim', to: MAINTENANCE_PATH, label: 'Bakım pencereleri' },
        ...item(notifications.length > 0, { id: 'bildirim', to: NOTIFICATIONS_PATH, label: 'Bildirim' }),
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
  // Her aracın kendi izni vardır (system.*, denetim kaydı için audit.view; varsayılan olarak yalnızca süper admin).
  const tools = [
    ...(can('system.queue.view') ? [{ id: 'kuyruk', label: 'Kuyruk Durumu' }] : []),
    ...(can('system.cache.view') ? [{ id: 'cache', label: 'Cache Durumu' }] : []),
    ...(can('system.logs.view') ? [{ id: 'log', label: 'Log Analiz' }] : []),
    ...(can('audit.view') ? [{ id: 'denetim', label: 'Denetim Kaydı' }] : []),
  ]
  const settings = [
    ...tab(can('settings.view'), {
      id: 'sistem',
      label: 'Sistem Ayarları',
      description: 'Agent sürümleri, veri saklama, oturum ve hız sınırları, panel adresi ve loglama.',
    }),
  ]
  return { groups, tools, settings, notifications }
}

const under = (pathname: string, to: string) => pathname === to || pathname.startsWith(`${to}/`)

// Eski ayrı sayfaların adresleri (yer imleri, eski bağlantılar) artık Ayarlar'ın sekmeleridir.
export const LEGACY_SETTINGS_ROUTES: Record<string, string> = {
  '/settings/system': 'sistem',
}

// Denetim Kaydı Sistem Araçları'nın bir sekmesidir (eski /audit ve /settings?sekme=denetim adresleri buraya yönlenir).
export const AUDIT_PATH = `${TOOLS_PATH}?sekme=denetim`

// Alert kurallarının kapsamı: sistem varsayılanı, bir organizasyon ya da bir sunucu (id yoksa seçilmemiş).
export type RuleScope = { kind: 'sistem' } | { kind: 'org'; id?: string } | { kind: 'sunucu'; id?: string }

// alertRulesPath, kurallar sayfasının o kapsam (ve verildiyse o konu) seçili adresidir (organizasyon/sunucu
// sayfalarından ve eski eşik adreslerinden gelen bağlantılar için).
export function alertRulesPath(scope: RuleScope = { kind: 'sistem' }, topic?: TopicId): string {
  const params = new URLSearchParams()
  if (scope.kind !== 'sistem') {
    params.set('kapsam', scope.kind)
    if (scope.id) params.set('id', scope.id)
  }
  if (topic) params.set('konu', topic)
  const query = params.toString()
  return query ? `${ALERT_RULES_PATH}?${query}` : ALERT_RULES_PATH
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

// notificationsPath, Bildirim sayfasının bir sekmesinin (ve kurallar için kapsamın, kişiler için organizasyonun)
// adresidir; eski sekmelerden ve uyarı şeridinden gelen bağlantılar için.
export function notificationsPath(tab: NotificationTabId, scope?: { kind: 'org' | 'sunucu'; id?: string }): string {
  const params = new URLSearchParams({ sekme: tab })
  if (scope) {
    if (tab === 'kurallar') params.set('kapsam', scope.kind)
    if (scope.id) params.set('id', scope.id)
  }
  return `${NOTIFICATIONS_PATH}?${params}`
}

// ADD_HOST_ORG_PARAM, sihirbazın organizasyonunu taşıyan parametredir. Liste süzgecinin "org" parametresinden ayrıdır:
// ikisi aynı adreste bulunabilir ve karışmamalıdır.
export const ADD_HOST_ORG_PARAM = 'hedef'

// addHostPath, Sunucular sayfasının "Sunucu ekle" sekmesidir; organizasyon verilirse sihirbaz onunla açılır.
export function addHostPath(organizationId?: string): string {
  const params = new URLSearchParams({ sekme: 'ekle' })
  if (organizationId) params.set(ADD_HOST_ORG_PARAM, organizationId)
  return `${HOSTS_PATH}?${params}`
}
