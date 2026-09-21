// Sayfa başına satır sayısı seçeneklerinin saf mantığı. Sayfaların varsayılan boyutu (10, 20, 50) her zaman
// seçenekler arasında olmalı; aksi halde <select> ilk seçeneği gösterir ve ekrandaki satır sayısıyla çelişir.

export const PAGE_SIZE_OPTIONS = [10, 20, 50, 100]

// Geçerli boyut standart listede yoksa (ör. eski bir varsayılan) listeye sıralı eklenir; böylece seçici her
// zaman gerçekte kullanılan boyutu gösterir.
export function pageSizeOptions(current: number): number[] {
  if (PAGE_SIZE_OPTIONS.includes(current)) return PAGE_SIZE_OPTIONS
  return [...PAGE_SIZE_OPTIONS, current].sort((a, b) => a - b)
}
