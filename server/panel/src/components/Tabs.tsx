import type { KeyboardEvent, ReactNode } from 'react'
import type { LucideIcon } from 'lucide-react'
import { nextTab } from '../tabs'

export interface TabItem {
  id: string
  label: string
  // Etiketin yanında küçük bir sayı (ör. listedeki kayıt sayısı).
  badge?: number
  icon?: LucideIcon
}

export function Tabs({
  items,
  active,
  onChange,
  label,
}: {
  items: TabItem[]
  active: string
  onChange: (id: string) => void
  label: string
}) {
  const ids = items.map((i) => i.id)

  function onKeyDown(e: KeyboardEvent<HTMLButtonElement>) {
    const target = nextTab(ids, active, e.key)
    if (target === null) return
    e.preventDefault()
    onChange(target)
    document.getElementById(`tab-${target}`)?.focus()
  }

  return (
    <div className="tabs" role="tablist" aria-label={label}>
      {items.map((item) => (
        <button
          key={item.id}
          id={`tab-${item.id}`}
          role="tab"
          type="button"
          aria-selected={item.id === active}
          aria-controls={`panel-${item.id}`}
          tabIndex={item.id === active ? 0 : -1}
          className={`tab${item.id === active ? ' active' : ''}`}
          onClick={() => onChange(item.id)}
          onKeyDown={onKeyDown}
        >
          {item.icon && <item.icon size={15} strokeWidth={1.75} />}
          {item.label}
          {item.badge !== undefined && <span className="tab-badge">{item.badge}</span>}
        </button>
      ))}
    </div>
  )
}

// Sekmenin içeriği. keepMounted, etkin olmayan sekmeyi gizli olarak ayakta tutar; yarım kalmış
// düzenlemeler (form, taslak) sekme değiştirilince kaybolmasın diye.
export function TabPanel({
  id,
  active,
  keepMounted = false,
  children,
}: {
  id: string
  active: string
  keepMounted?: boolean
  children: ReactNode
}) {
  const isActive = id === active
  if (!isActive && !keepMounted) return null
  return (
    <div role="tabpanel" id={`panel-${id}`} aria-labelledby={`tab-${id}`} hidden={!isActive}>
      {children}
    </div>
  )
}
