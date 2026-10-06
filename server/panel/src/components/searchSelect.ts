// Aranabilir seçim kutusunun saf mantığı: seçeneklerin yazılan metne göre süzülmesi ve klavyeyle gezinme. React
// içermez; Node'un çalıştırıcısıyla birim test edilir.

export interface SelectOption {
  value: string
  label: string
  // Etiketin yanında soluk gösterilen ek bilgi (ör. sunucunun organizasyonu); aramaya da girer.
  hint?: string
  // Ağaç girintisi (organizasyon ağacı); 0 = kök.
  depth?: number
  // Görünmeyen ama aranan ek metin (ör. IP adresi, üst şirket zinciri).
  keywords?: string
}

const norm = (s: string) => s.toLocaleLowerCase('tr')

// Metnin her kelimesi etikette, ek bilgide ya da anahtar kelimelerde geçen seçenekler, özgün sırasıyla. Boş metin
// hepsini döndürür.
export function filterOptions(options: SelectOption[], query: string): SelectOption[] {
  const words = norm(query).split(/\s+/).filter(Boolean)
  if (words.length === 0) return options
  return options.filter((o) => {
    const hay = norm(`${o.label} ${o.hint ?? ''} ${o.keywords ?? ''}`)
    return words.every((w) => hay.includes(w))
  })
}

// Ok tuşlarıyla vurgulanan seçeneğin yeni sırası (uçlarda başa/sona sarar); liste boşsa -1.
export function moveActive(current: number, count: number, key: 'ArrowDown' | 'ArrowUp'): number {
  if (count === 0) return -1
  if (current < 0) return key === 'ArrowDown' ? 0 : count - 1
  return key === 'ArrowDown' ? (current + 1) % count : (current - 1 + count) % count
}
