import { useEffect, useId, useRef, useState, type ReactNode } from 'react'

// Başlığın yanındaki "i" düğmesi: açıklamayı başlığın altında yer kaplamak yerine tıklanınca küçük bir kutuda gösterir.
// Dışına tıklamak ya da Esc kapatır; Esc odağı düğmeye geri verir.
export function InfoTip({ label, children }: { label: string; children: ReactNode }) {
  const [open, setOpen] = useState(false)
  const root = useRef<HTMLSpanElement>(null)
  const button = useRef<HTMLButtonElement>(null)
  const id = useId()

  useEffect(() => {
    if (!open) return
    const onDown = (e: MouseEvent) => {
      if (root.current && !root.current.contains(e.target as Node)) setOpen(false)
    }
    const onKey = (e: KeyboardEvent) => {
      if (e.key !== 'Escape') return
      // Açık kutu varken Esc önce onu kapatır (ör. büyütülmüş grafik penceresi açık kalır).
      e.stopPropagation()
      setOpen(false)
      button.current?.focus()
    }
    document.addEventListener('mousedown', onDown)
    document.addEventListener('keydown', onKey, true)
    return () => {
      document.removeEventListener('mousedown', onDown)
      document.removeEventListener('keydown', onKey, true)
    }
  }, [open])

  return (
    <span className="info-tip" ref={root}>
      <button
        ref={button}
        type="button"
        className="info-tip-button"
        aria-label={`${label}: açıklama`}
        aria-expanded={open}
        aria-controls={id}
        onClick={() => setOpen((v) => !v)}
      >
        {/* Yalnızca "i" işareti: düğmenin kendisi yuvarlak olduğundan simgenin ikinci bir halkası olmaz. */}
        <svg aria-hidden="true" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.6" strokeLinecap="round" strokeLinejoin="round">
          <circle cx="12.5" cy="5.5" r="2.2" fill="currentColor" stroke="none" />
          <path d="M9.5 10.5h3.5V19" />
          <path d="M9 19h7.5" />
        </svg>
      </button>
      {open && (
        <span id={id} role="note" className="info-tip-popover">
          {children}
        </span>
      )}
    </span>
  )
}
