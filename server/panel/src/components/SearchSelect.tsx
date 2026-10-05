import { useEffect, useId, useMemo, useRef, useState, type KeyboardEvent } from 'react'
import { ChevronDown } from 'lucide-react'
import { filterOptions, moveActive, type SelectOption } from './searchSelect'

// Yazarak aranabilen seçim kutusu (combobox): kutuya yazılan metin listeyi süzer, seçilen değer kutuda görünür. Klavye:
// ok tuşları gezinir, Enter seçer, Esc kapatır. Liste açıkken kutu boşalır ve seçili değer yer tutucu olarak kalır; böylece
// yazmaya hemen başlanabilir. Uzun listelerde (150+ sunucu) kaydırmak yerine aramak içindir.
export function SearchSelect({
  id,
  label,
  options,
  value,
  onChange,
  placeholder = 'Seçin…',
  emptyText = 'Eşleşen sonuç yok',
}: {
  id: string
  label: string
  options: SelectOption[]
  value?: string
  onChange: (value: string) => void
  placeholder?: string
  emptyText?: string
}) {
  const listId = useId()
  const rootRef = useRef<HTMLDivElement>(null)
  const listRef = useRef<HTMLUListElement>(null)
  const [open, setOpen] = useState(false)
  const [query, setQuery] = useState('')
  const [active, setActive] = useState(-1)

  const selected = options.find((o) => o.value === value)
  const shown = useMemo(() => filterOptions(options, query), [options, query])

  // Dışarı tıklanınca kapanır.
  useEffect(() => {
    if (!open) return
    const onDown = (e: MouseEvent) => {
      if (!rootRef.current?.contains(e.target as Node)) close()
    }
    document.addEventListener('mousedown', onDown)
    return () => document.removeEventListener('mousedown', onDown)
  }, [open])

  // Vurgulanan seçenek görünür alanda kalsın.
  useEffect(() => {
    if (active < 0) return
    listRef.current?.querySelector<HTMLElement>(`[data-index="${active}"]`)?.scrollIntoView({ block: 'nearest' })
  }, [active])

  function openList() {
    if (open) return
    setOpen(true)
    setQuery('')
    setActive(Math.max(0, options.findIndex((o) => o.value === value)))
  }

  function close() {
    setOpen(false)
    setQuery('')
    setActive(-1)
  }

  function choose(option: SelectOption) {
    onChange(option.value)
    close()
  }

  function onKeyDown(e: KeyboardEvent<HTMLInputElement>) {
    if (e.key === 'ArrowDown' || e.key === 'ArrowUp') {
      e.preventDefault()
      if (!open) openList()
      else setActive((a) => moveActive(a, shown.length, e.key as 'ArrowDown' | 'ArrowUp'))
    } else if (e.key === 'Enter') {
      if (open && active >= 0 && shown[active]) {
        e.preventDefault()
        choose(shown[active])
      }
    } else if (e.key === 'Escape') {
      if (open) {
        e.stopPropagation()
        close()
      }
    }
  }

  return (
    <div className="search-select" ref={rootRef}>
      <label htmlFor={id} className="visually-hidden">
        {label}
      </label>
      <input
        id={id}
        role="combobox"
        aria-expanded={open}
        aria-controls={listId}
        aria-autocomplete="list"
        aria-activedescendant={open && active >= 0 && shown[active] ? `${listId}-${active}` : undefined}
        autoComplete="off"
        spellCheck={false}
        value={open ? query : (selected?.label ?? '')}
        placeholder={open && selected ? selected.label : placeholder}
        onFocus={openList}
        onClick={openList}
        onChange={(e) => {
          if (!open) setOpen(true)
          setQuery(e.target.value)
          setActive(0)
        }}
        onKeyDown={onKeyDown}
      />
      <ChevronDown className="search-select-caret" size={15} strokeWidth={2} aria-hidden="true" />
      {open && (
        <ul id={listId} ref={listRef} role="listbox" aria-label={label} className="search-select-list">
          {shown.length === 0 ? (
            <li className="search-select-empty">{emptyText}</li>
          ) : (
            shown.map((o, i) => (
              <li
                key={o.value}
                id={`${listId}-${i}`}
                data-index={i}
                role="option"
                aria-selected={o.value === value}
                className={`search-select-option${i === active ? ' active' : ''}`}
                style={o.depth ? { paddingLeft: 10 + o.depth * 16 } : undefined}
                // mousedown'da seçilir: input'un blur'u listeyi kapatmadan önce.
                onMouseDown={(e) => {
                  e.preventDefault()
                  choose(o)
                }}
                onMouseEnter={() => setActive(i)}
              >
                <span>{o.label}</span>
                {o.hint && <span className="search-select-hint">{o.hint}</span>}
              </li>
            ))
          )}
        </ul>
      )}
    </div>
  )
}
