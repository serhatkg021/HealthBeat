import { useEffect, useRef, type KeyboardEvent, type ReactNode } from 'react'
import { X } from 'lucide-react'

const FOCUSABLE = 'a[href], button:not([disabled]), input:not([disabled]), select:not([disabled]), textarea:not([disabled]), [tabindex]:not([tabindex="-1"])'

// Ortalanmış diyalog (bir kartın "Detay" işlevi gibi ikincil, geniş içerik için — ör. bir
// grafiğin tam geçmişi). Klavye erişimi Drawer ile aynı: açılınca odak panele geçer, Tab
// panelin içinde döner, Esc kapatır ve odak açan öğeye geri verilir; sayfa kaydırması kilitlenir.
// size="sm" kısa formlar ve onay soruları içindir (dar pencere). Esc, odak panelin dışına düşmüş olsa da (ör. odaktaki
// düğme ekrandan kalktığında) kapatır.
export function Modal({
  open,
  title,
  onClose,
  size,
  children,
}: {
  open: boolean
  title: string
  onClose: () => void
  size?: 'sm'
  children: ReactNode
}) {
  const panel = useRef<HTMLDivElement>(null)
  // En güncel onClose (çoğu çağıran her çizimde yeni bir işlev verir; dinleyici yeniden kurulmasın).
  const closeRef = useRef(onClose)
  useEffect(() => {
    closeRef.current = onClose
  })

  // Odak panelin içindeyken Esc'yi panelin kendi onKeyDown'ı yakalar (ve yayılımı durdurur); buraya yalnızca odak
  // panelin dışındayken gelen Esc ulaşır.
  useEffect(() => {
    if (!open) return
    const onKey = (e: globalThis.KeyboardEvent) => {
      if (e.key === 'Escape' && !e.defaultPrevented) closeRef.current()
    }
    document.addEventListener('keydown', onKey)
    return () => document.removeEventListener('keydown', onKey)
  }, [open])

  useEffect(() => {
    if (!open) return
    const opener = document.activeElement as HTMLElement | null
    document.body.classList.add('scroll-lock')
    panel.current?.focus()
    return () => {
      document.body.classList.remove('scroll-lock')
      opener?.focus?.()
    }
  }, [open])

  if (!open) return null

  function onKeyDown(e: KeyboardEvent<HTMLDivElement>) {
    if (e.key === 'Escape') {
      e.stopPropagation()
      onClose()
      return
    }
    if (e.key !== 'Tab' || !panel.current) return
    const items = [...panel.current.querySelectorAll<HTMLElement>(FOCUSABLE)]
    if (items.length === 0) return
    const first = items[0]
    const last = items[items.length - 1]
    if (e.shiftKey && (document.activeElement === first || document.activeElement === panel.current)) {
      e.preventDefault()
      last.focus()
    } else if (!e.shiftKey && document.activeElement === last) {
      e.preventDefault()
      first.focus()
    }
  }

  return (
    <div className="modal-root">
      <div className="modal-scrim" onClick={onClose} aria-hidden="true" />
      <div
        ref={panel}
        className={size === 'sm' ? 'modal modal-sm' : 'modal'}
        role="dialog"
        aria-modal="true"
        aria-labelledby="modal-title"
        tabIndex={-1}
        onKeyDown={onKeyDown}
      >
        <div className="modal-head">
          <h2 id="modal-title">{title}</h2>
          <button type="button" className="icon-btn" aria-label="Kapat" onClick={onClose}>
            <X size={18} strokeWidth={1.75} />
          </button>
        </div>
        <div className="modal-body">{children}</div>
      </div>
    </div>
  )
}
