import { useEffect, useId, useLayoutEffect, useRef, useState, type ReactNode } from 'react'
import { MoreHorizontal, type LucideIcon } from 'lucide-react'

export interface RowMenuItem {
  label: string
  icon?: LucideIcon
  danger?: boolean
  // separated, öğeden önce ince bir ayraç çizer (ör. tehlikeli işlemleri ayırmak için).
  separated?: boolean
  onSelect: () => void
}

// Tablo satırının işlemleri: "⋯" düğmesi küçük bir menü açar. Menü ekrana sabitlenir (position: fixed): tablo kartı
// taşanı kestiği için son satırlarda da görünür. Dışarı tıklamak, Esc, kaydırma ya da pencere boyutu değişimi kapatır.
export function RowMenu({ label, items }: { label: string; items: RowMenuItem[] }) {
  const [open, setOpen] = useState(false)
  const [pos, setPos] = useState<{ top: number; right: number } | null>(null)
  const button = useRef<HTMLButtonElement>(null)
  const menu = useRef<HTMLDivElement>(null)
  const id = useId()

  useLayoutEffect(() => {
    if (!open || !button.current) return
    const r = button.current.getBoundingClientRect()
    const height = menu.current?.offsetHeight ?? 0
    // Aşağıda yer yoksa düğmenin üstüne açılır.
    const below = r.bottom + 4 + height <= window.innerHeight
    setPos({ top: below ? r.bottom + 4 : Math.max(r.top - 4 - height, 8), right: window.innerWidth - r.right })
  }, [open])

  useEffect(() => {
    if (!open) return
    const close = () => setOpen(false)
    const onDown = (e: MouseEvent) => {
      if (!menu.current?.contains(e.target as Node) && !button.current?.contains(e.target as Node)) close()
    }
    const onKey = (e: KeyboardEvent) => {
      if (e.key !== 'Escape') return
      close()
      button.current?.focus()
    }
    document.addEventListener('mousedown', onDown)
    document.addEventListener('keydown', onKey)
    window.addEventListener('scroll', close, true)
    window.addEventListener('resize', close)
    return () => {
      document.removeEventListener('mousedown', onDown)
      document.removeEventListener('keydown', onKey)
      window.removeEventListener('scroll', close, true)
      window.removeEventListener('resize', close)
    }
  }, [open])

  if (items.length === 0) return null
  return (
    <>
      <button
        ref={button}
        type="button"
        className="icon-btn row-menu-button"
        aria-label={`${label}: işlemler`}
        aria-haspopup="menu"
        aria-expanded={open}
        aria-controls={open ? id : undefined}
        onClick={() => {
          setPos(null)
          setOpen((v) => !v)
        }}
      >
        <MoreHorizontal size={17} />
      </button>
      {open && (
        <div
          id={id}
          ref={menu}
          role="menu"
          className="row-menu"
          style={pos ? { top: pos.top, right: pos.right } : { visibility: 'hidden', top: 0, right: 0 }}
        >
          {items.map((item) => (
            <MenuEntry
              key={item.label}
              item={item}
              onSelect={() => {
                setOpen(false)
                item.onSelect()
              }}
            />
          ))}
        </div>
      )}
    </>
  )
}

function MenuEntry({ item, onSelect }: { item: RowMenuItem; onSelect: () => void }): ReactNode {
  const Icon = item.icon
  return (
    <>
      {item.separated && <div className="row-menu-separator" role="separator" />}
      <button type="button" role="menuitem" className={`row-menu-item${item.danger ? ' danger' : ''}`} onClick={onSelect}>
        {Icon && <Icon size={15} strokeWidth={1.9} />}
        {item.label}
      </button>
    </>
  )
}
