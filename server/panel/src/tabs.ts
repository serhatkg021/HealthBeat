// Sekme menüleri için saf mantık: URL'deki sekme adının çözümlenmesi ve klavye ile gezinme.
// React içermez; Node'un çalıştırıcısıyla birim test edilir.

// Sekme, sayfa adresinde `?sekme=` olarak saklanır; böylece yenileme ve geri tuşu sekmeyi korur.
export const TAB_PARAM = 'sekme'

// URL'deki değer izin verilen sekmelerden biri değilse (yazım hatası, artık var olmayan bir sekme,
// yetkisi olmayan bir sekme) varsayılan sekmeye düşülür.
export function resolveTab(raw: string | null, ids: readonly string[], fallback: string): string {
  return raw !== null && ids.includes(raw) ? raw : fallback
}

// Ok tuşları komşu sekmeye (uçlarda başa/sona sarar), Home/End ilk/son sekmeye gider; başka tuş null.
export function nextTab(ids: readonly string[], current: string, key: string): string | null {
  if (ids.length === 0) return null
  const i = ids.indexOf(current)
  switch (key) {
    case 'ArrowRight':
      return ids[(i + 1) % ids.length]
    case 'ArrowLeft':
      return ids[(i - 1 + ids.length) % ids.length]
    case 'Home':
      return ids[0]
    case 'End':
      return ids[ids.length - 1]
    default:
      return null
  }
}
