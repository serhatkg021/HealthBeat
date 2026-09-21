import { useEffect, useState } from 'react'
import { Search } from 'lucide-react'

// Debounce'lu arama kutusu: her tuş vuruşunda değil, kullanıcı yazmayı bıraktıktan
// debounceMs sonra onChange çağrılır. value dışarıdan değişirse (ör. filtre sıfırlama)
// taslak onunla senkronlanır.
export function SearchInput({
  value,
  onChange,
  placeholder,
  debounceMs = 300,
}: {
  value: string
  onChange: (v: string) => void
  placeholder?: string
  debounceMs?: number
}) {
  const [draft, setDraft] = useState(value)

  useEffect(() => setDraft(value), [value])

  useEffect(() => {
    if (draft === value) return
    const t = setTimeout(() => onChange(draft), debounceMs)
    return () => clearTimeout(t)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [draft])

  return (
    <div className="search-input">
      <Search size={15} strokeWidth={1.9} />
      <input
        type="search"
        value={draft}
        onChange={(e) => setDraft(e.target.value)}
        placeholder={placeholder ?? 'Ara…'}
        aria-label={placeholder ?? 'Ara'}
      />
    </div>
  )
}
