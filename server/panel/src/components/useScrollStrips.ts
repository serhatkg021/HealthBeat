import { useEffect } from 'react'

// Dar ekranda tek satırda yana kayan menüler: sekmeler, bölüm menüleri, süzgeç düğmeleri.
const STRIPS = '.tabs, .settings-nav, .segmented'
const ACTIVE = '.active, [aria-selected="true"], [aria-pressed="true"]'
// Etkin öğe kenara yapışmasın; komşusunun ucu da görünsün ki menünün devam ettiği anlaşılsın.
const MARGIN = 28

function overflows(strip: HTMLElement): boolean {
  return strip.scrollWidth > strip.clientWidth + 1
}

// Menünün hangi kenarında görünmeyen öğe kaldığını işaretler; CSS o kenarı soldurur (bkz. index.css).
function mark(strip: HTMLElement): void {
  const max = strip.scrollWidth - strip.clientWidth
  strip.toggleAttribute('data-more-start', strip.scrollLeft > 1)
  strip.toggleAttribute('data-more-end', strip.scrollLeft < max - 1)
}

function reveal(strip: HTMLElement, smooth: boolean): void {
  const active = strip.querySelector<HTMLElement>(ACTIVE)
  if (!active || !overflows(strip)) return
  const s = strip.getBoundingClientRect()
  const a = active.getBoundingClientRect()
  let delta = 0
  if (a.left < s.left + MARGIN) delta = a.left - s.left - MARGIN
  else if (a.right > s.right - MARGIN) delta = a.right - s.right + MARGIN
  if (delta !== 0) strip.scrollBy({ left: delta, behavior: smooth ? 'smooth' : 'auto' })
}

// Yana kayan menülerin ortak davranışı; Layout bir kez kurar, sayfalar bir şey yapmaz:
// - etkin öğe (sayfa açılınca ve seçim değişince) görünür alana getirilir,
// - fare tekerleği menünün üstündeyken menüyü yana kaydırır (dokunmatik olmayan dar pencerede başka yolu yoktur),
// - görünmeyen öğe kalan kenar soldurulur.
export function useScrollStrips(): void {
  useEffect(() => {
    const seen = new WeakSet<HTMLElement>()
    let frame = 0

    const sync = () => {
      frame = 0
      for (const strip of document.querySelectorAll<HTMLElement>(STRIPS)) {
        if (!seen.has(strip) && strip.clientWidth > 0) {
          seen.add(strip)
          reveal(strip, false)
        }
        mark(strip)
      }
    }
    const schedule = () => {
      frame ||= requestAnimationFrame(sync)
    }

    const onWheel = (e: WheelEvent) => {
      const strip = (e.target as Element | null)?.closest<HTMLElement>(STRIPS)
      if (!strip || !overflows(strip) || Math.abs(e.deltaY) <= Math.abs(e.deltaX)) return
      const max = strip.scrollWidth - strip.clientWidth
      // Menü o yönde sonuna geldiyse tekerlek sayfayı kaydırmaya devam eder.
      if ((e.deltaY < 0 && strip.scrollLeft <= 0) || (e.deltaY > 0 && strip.scrollLeft >= max - 1)) return
      e.preventDefault()
      strip.scrollLeft += e.deltaY
    }
    const onScroll = (e: Event) => {
      if (e.target instanceof HTMLElement && e.target.matches(STRIPS)) mark(e.target)
    }
    // Seçim tıklamayla değişir; etkin sınıfı React bir sonraki çizimde verir.
    const onClick = (e: MouseEvent) => {
      const strip = (e.target as Element | null)?.closest<HTMLElement>(STRIPS)
      if (strip) requestAnimationFrame(() => requestAnimationFrame(() => reveal(strip, true)))
    }

    const observer = new MutationObserver(schedule)
    observer.observe(document.body, { childList: true, subtree: true })
    document.addEventListener('wheel', onWheel, { passive: false })
    document.addEventListener('scroll', onScroll, true)
    document.addEventListener('click', onClick)
    window.addEventListener('resize', schedule)
    schedule()
    return () => {
      observer.disconnect()
      document.removeEventListener('wheel', onWheel)
      document.removeEventListener('scroll', onScroll, true)
      document.removeEventListener('click', onClick)
      window.removeEventListener('resize', schedule)
      if (frame) cancelAnimationFrame(frame)
    }
  }, [])
}
