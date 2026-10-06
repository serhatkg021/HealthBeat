import { useEffect, useId, useMemo, useRef, useState, type KeyboardEvent } from 'react'
import { useNavigate } from 'react-router-dom'
import { Building2, CornerDownLeft, FileText, Search, Server, type LucideIcon } from 'lucide-react'
import { dashboardApi, organizationsApi } from '../api/endpoints'
import { useAuth } from '../auth/AuthContext'
import { navigation } from '../navigation'
import { GROUP_ORDER, pageItems, searchPalette, type PaletteGroup, type PaletteItem } from './commandPalette'
import { moveActive } from './searchSelect'

const GROUP_ICON: Record<PaletteGroup, LucideIcon> = { Sunucular: Server, Organizasyonlar: Building2, Sayfalar: FileText }

// Ctrl+K (⌘K) araması: sunucuya, organizasyona ya da sayfaya yazarak gitmek. Sunucu ve organizasyon listesi pencere ilk
// açıldığında bir kez yüklenir (yetkiye göre süzülü gelir). Ok tuşları gezinir, Enter gider, Esc kapatır.
export function CommandPalette({ open, onClose }: { open: boolean; onClose: () => void }) {
  const { can } = useAuth()
  const [entities, setEntities] = useState<PaletteItem[] | null>(null)

  // İlk açılışta sunucular ve organizasyonlar yüklenir; sonraki açılışlarda eldeki liste kullanılır.
  useEffect(() => {
    if (!open || entities !== null) return
    const canSeeOrgs = can('organization.view')
    Promise.all([dashboardApi.overview().catch(() => null), canSeeOrgs ? organizationsApi.list().catch(() => []) : Promise.resolve([])]).then(([overview, orgs]) => {
      const orgName = new Map((overview?.organizations ?? []).map((o) => [o.id, o.name]))
      setEntities([
        ...(overview?.hosts ?? []).map((h) => ({
          key: `host:${h.id}`,
          group: 'Sunucular' as const,
          label: h.title,
          hint: orgName.get(h.organization_id) ?? h.ip,
          to: `/hosts/${h.id}`,
          keywords: h.ip,
        })),
        ...orgs
          .filter((o) => o.access !== 'context')
          .map((o) => ({ key: `org:${o.id}`, group: 'Organizasyonlar' as const, label: o.name, hint: o.address, to: `/organizations/${o.id}` })),
      ])
    })
  }, [open, entities, can])

  const pages = useMemo(() => pageItems(navigation(can), can('host.create')), [can])

  // Diyalog her açılışta yeniden kurulur: arama metni ve vurgu kendiliğinden sıfırlanır.
  return open ? <PaletteDialog entities={entities} pages={pages} onClose={onClose} /> : null
}

function PaletteDialog({ entities, pages, onClose }: { entities: PaletteItem[] | null; pages: PaletteItem[]; onClose: () => void }) {
  const navigate = useNavigate()
  const listId = useId()
  const inputRef = useRef<HTMLInputElement>(null)
  const [query, setQuery] = useState('')
  const [active, setActive] = useState(0)
  const results = useMemo(() => searchPalette([...(entities ?? []), ...pages], query), [entities, pages, query])

  useEffect(() => {
    const opener = document.activeElement as HTMLElement | null
    inputRef.current?.focus()
    document.body.classList.add('scroll-lock')
    return () => {
      document.body.classList.remove('scroll-lock')
      opener?.focus?.()
    }
  }, [])

  function go(item: PaletteItem) {
    onClose()
    navigate(item.to)
  }

  function onKeyDown(e: KeyboardEvent<HTMLInputElement>) {
    if (e.key === 'ArrowDown' || e.key === 'ArrowUp') {
      e.preventDefault()
      setActive((a) => moveActive(a, results.length, e.key as 'ArrowDown' | 'ArrowUp'))
    } else if (e.key === 'Enter') {
      e.preventDefault()
      if (results[active]) go(results[active])
    } else if (e.key === 'Escape') {
      e.preventDefault()
      onClose()
    }
  }

  return (
    <div className="palette-root">
      <div className="modal-scrim" onClick={onClose} aria-hidden="true" />
      <div className="palette" role="dialog" aria-modal="true" aria-label="Hızlı arama">
        <div className="palette-input">
          <Search size={17} strokeWidth={1.9} aria-hidden="true" />
          <input
            ref={inputRef}
            role="combobox"
            aria-expanded="true"
            aria-controls={listId}
            aria-activedescendant={results[active] ? `${listId}-${active}` : undefined}
            aria-label="Sunucu, organizasyon ya da sayfa ara"
            placeholder="Sunucu, organizasyon ya da sayfa ara…"
            autoComplete="off"
            spellCheck={false}
            value={query}
            onChange={(e) => {
              setQuery(e.target.value)
              setActive(0)
            }}
            onKeyDown={onKeyDown}
          />
          <kbd>Esc</kbd>
        </div>
        <ul id={listId} role="listbox" aria-label="Sonuçlar" className="palette-list">
          {results.length === 0 && <li className="palette-empty">{entities === null && query ? 'Yükleniyor…' : 'Sonuç yok'}</li>}
          {GROUP_ORDER.map((group) => {
            const items = results.filter((r) => r.group === group)
            if (items.length === 0) return null
            const Icon = GROUP_ICON[group]
            return (
              <li key={group} role="presentation">
                <div className="palette-group">{group}</div>
                <ul role="presentation">
                  {items.map((item) => {
                    const index = results.indexOf(item)
                    return (
                      <li
                        key={item.key}
                        id={`${listId}-${index}`}
                        role="option"
                        aria-selected={index === active}
                        className={`palette-item${index === active ? ' active' : ''}`}
                        onMouseDown={(e) => {
                          e.preventDefault()
                          go(item)
                        }}
                        onMouseEnter={() => setActive(index)}
                      >
                        <Icon size={15} strokeWidth={1.75} aria-hidden="true" />
                        <span className="palette-label">{item.label}</span>
                        {item.hint && <span className="palette-hint">{item.hint}</span>}
                        {index === active && <CornerDownLeft className="palette-enter" size={14} strokeWidth={1.9} aria-hidden="true" />}
                      </li>
                    )
                  })}
                </ul>
              </li>
            )
          })}
        </ul>
      </div>
    </div>
  )
}
