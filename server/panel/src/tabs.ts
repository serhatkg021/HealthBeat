// Sekme menüleri için saf mantık: URL'deki sekme adının çözümlenmesi ve klavye ile gezinme.
// React içermez; Node'un çalıştırıcısıyla birim test edilir.

// Sekme, sayfa adresinde `?sekme=` olarak saklanır; böylece yenileme ve geri tuşu sekmeyi korur.
export const TAB_PARAM = 'sekme'

// URL'deki değer izin verilen sekmelerden biri değilse (yazım hatası, artık var olmayan bir sekme,
// yetkisi olmayan bir sekme) varsayılan sekmeye düşülür. aliases, adı değişmiş ya da başka sekmeye taşınmış eski
// sekme adlarını (yer imleri, eski bağlantılar) yenisine çevirir.
export function resolveTab(raw: string | null, ids: readonly string[], fallback: string, aliases: Readonly<Record<string, string>> = {}): string {
  const id = raw !== null && Object.hasOwn(aliases, raw) ? aliases[raw] : raw
  return id !== null && ids.includes(id) ? id : fallback
}

// Ok tuşları komşu sekmeye (uçlarda başa/sona sarar), Home/End ilk/son sekmeye gider; başka tuş null. Dikey menüde
// yukarı/aşağı okları, yataydakinde sol/sağ okları geçerlidir.
export function nextTab(ids: readonly string[], current: string, key: string, orientation: 'horizontal' | 'vertical' = 'horizontal'): string | null {
  if (ids.length === 0) return null
  const i = ids.indexOf(current)
  const [next, prev] = orientation === 'vertical' ? ['ArrowDown', 'ArrowUp'] : ['ArrowRight', 'ArrowLeft']
  switch (key) {
    case next:
      return ids[(i + 1) % ids.length]
    case prev:
      return ids[(i - 1 + ids.length) % ids.length]
    case 'Home':
      return ids[0]
    case 'End':
      return ids[ids.length - 1]
    default:
      return null
  }
}
