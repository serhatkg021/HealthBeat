// Ctrl+K araması için saf mantık: aranabilir öğelerin (sunucular, organizasyonlar, sayfalar) kurulması ve yazılan metne
// göre gruplu süzülmesi. React içermez; Node'un çalıştırıcısıyla birim test edilir.
import { TOOLS_PATH, addHostPath, notificationsPath, type Navigation } from '../navigation.ts'
import { filterOptions } from './searchSelect.ts'

export type PaletteGroup = 'Sunucular' | 'Organizasyonlar' | 'Sayfalar'

export interface PaletteItem {
  // Gruplar arasında benzersiz (React anahtarı ve vurgulama için).
  key: string
  group: PaletteGroup
  label: string
  hint?: string
  to: string
  keywords?: string
}

// Grup başına en çok bu kadar sonuç gösterilir (uzun listede kaydırmak yerine daha çok yazmak için).
export const GROUP_LIMIT: Record<PaletteGroup, number> = { Sunucular: 8, Organizasyonlar: 5, Sayfalar: 8 }

export const GROUP_ORDER: PaletteGroup[] = ['Sunucular', 'Organizasyonlar', 'Sayfalar']

// Sayfalar: menüdeki her öğe, Sistem Araçları ve Bildirim sekmeleri, Ayarlar ve (izni olana) sunucu ekleme. Yalnızca
// kullanıcının menüde görebildikleri (navigation zaten izne göre süzülüdür).
export function pageItems(nav: Navigation, canAddHost: boolean): PaletteItem[] {
  const items: PaletteItem[] = []
  for (const g of nav.groups) {
    for (const it of g.items) items.push({ key: `page:${it.id}`, group: 'Sayfalar', label: it.label, hint: g.label, to: it.to })
  }
  for (const t of nav.notifications) {
    items.push({ key: `page:bildirim-${t.id}`, group: 'Sayfalar', label: t.label, hint: 'Bildirim', to: notificationsPath(t.id) })
  }
  for (const t of nav.tools) {
    items.push({ key: `page:arac-${t.id}`, group: 'Sayfalar', label: t.label, hint: 'Sistem Araçları', to: `${TOOLS_PATH}?sekme=${t.id}` })
  }
  if (nav.settings.length > 0) items.push({ key: 'page:ayarlar', group: 'Sayfalar', label: 'Ayarlar', to: '/settings' })
  if (canAddHost) items.push({ key: 'page:sunucu-ekle', group: 'Sayfalar', label: 'Sunucu ekle', hint: 'Sunucular', to: addHostPath() })
  items.push({ key: 'page:profil', group: 'Sayfalar', label: 'Profil', to: '/profile' })
  return items
}

// Yazılan metne uyan öğeler, grup sırasıyla ve grup başına sınırlı. Boş metinde yalnızca sayfalar gösterilir (sunucu ve
// organizasyon listesi uzun olabilir; onlar yazınca gelir).
export function searchPalette(items: PaletteItem[], query: string): PaletteItem[] {
  const blank = query.trim() === ''
  const out: PaletteItem[] = []
  for (const group of GROUP_ORDER) {
    if (blank && group !== 'Sayfalar') continue
    const inGroup = items.filter((i) => i.group === group).map((i) => ({ ...i, value: i.key }))
    for (const match of filterOptions(inGroup, query).slice(0, GROUP_LIMIT[group])) {
      out.push(items.find((i) => i.key === match.value)!)
    }
  }
  return out
}
