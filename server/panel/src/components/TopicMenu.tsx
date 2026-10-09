import { useEffect, type KeyboardEvent } from 'react'
import { nextTab } from '../tabs'

export interface TopicMenuEntry {
  id: string
  title: string
  // Sağdaki kısa değer ("2/3", "%23") ve üzerine gelince görünen açıklaması.
  meta?: string
  metaTitle?: string
  // Üzerine gelince görünen açıklama (konunun neyi kapsadığı; panelde ayrıca başlık yok).
  hint?: string
  // Konudaki bir durumu bildiren nokta (kaydedilmemiş değişiklik, açık alert); label ekran okuyucu ve araç ipucu içindir.
  dot?: { label: string; tone?: 'accent' | 'warning' | 'critical' }
}

// Sayfa içi dikey konu menüsü (Alert kuralları, Performans): sekme gibi çalışır, seçili konunun paneli `panelId`'dir. Ok
// tuşları, Home ve End ile gezilir; dar ekranda menü yatay şerittir ve iki yönün okları da çalışır.
export function TopicMenu<T extends string>({
  items,
  active,
  onSelect,
  panelId,
  label = 'Konular',
}: {
  items: (TopicMenuEntry & { id: T })[]
  active: T
  onSelect: (id: T) => void
  panelId: string
  label?: string
}) {
  const ids = items.map((m) => m.id)

  // Dar ekranda menü yatay şerittir: adresten gelen konu şeridin dışında kalmasın.
  useEffect(() => {
    document.getElementById(`konu-${active}`)?.scrollIntoView({ block: 'nearest', inline: 'nearest' })
  }, [active])

  // Hedef, odaktaki sekmeden hesaplanır (henüz çizilmemiş seçimden değil): art arda basılan oklar kaybolmaz.
  function onKeyDown(e: KeyboardEvent<HTMLButtonElement>, from: T) {
    const target = nextTab(ids, from, e.key, 'vertical') ?? nextTab(ids, from, e.key)
    if (target === null) return
    e.preventDefault()
    onSelect(target as T)
    document.getElementById(`konu-${target}`)?.focus()
  }

  return (
    <div className="topic-menu" role="tablist" aria-orientation="vertical" aria-label={label}>
      {items.map((m) => (
        <button
          key={m.id}
          id={`konu-${m.id}`}
          type="button"
          role="tab"
          aria-selected={m.id === active}
          aria-controls={panelId}
          tabIndex={m.id === active ? 0 : -1}
          className={`topic-tab${m.id === active ? ' active' : ''}`}
          title={m.hint}
          onClick={() => onSelect(m.id)}
          onKeyDown={(e) => onKeyDown(e, m.id)}
        >
          <span className="topic-tab-title">{m.title}</span>
          <span className="topic-tab-meta">
            {m.dot && <span className={`topic-tab-dot${m.dot.tone && m.dot.tone !== 'accent' ? ` ${m.dot.tone}` : ''}`} title={m.dot.label} aria-label={m.dot.label} />}
            {m.meta && (
              <span className="topic-tab-count" title={m.metaTitle}>
                {m.meta}
              </span>
            )}
          </span>
        </button>
      ))}
    </div>
  )
}
