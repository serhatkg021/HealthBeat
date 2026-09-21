import { useEffect, useState } from 'react'
import type { Page } from './client'

// Sayfalanabilir/aranabilir tabloların (Alert'ler, Kullanıcılar, Organizasyon sunucuları)
// paylaştığı page/pageSize/arama durumu ve getirme mantığı. deps'teki bir değer değişirse
// (ör. bir durum süzgeci) da yeniden yükler; page/pageSize/q'yu deps'e eklemeye gerek yok,
// zaten dahil.
export function usePagedQuery<T>(
  fetcher: (params: { q: string; limit: number; offset: number }) => Promise<Page<T>>,
  initialPageSize: number,
  deps: unknown[] = [],
) {
  const [page, setPage] = useState(1)
  const [pageSize, setPageSizeState] = useState(initialPageSize)
  const [q, setQ] = useState('')
  const [items, setItems] = useState<T[]>([])
  const [total, setTotal] = useState(0)
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState<string | null>(null)

  function reload() {
    setLoading(true)
    setError(null)
    fetcher({ q, limit: pageSize, offset: (page - 1) * pageSize })
      .then((res) => {
        setItems(res.items)
        setTotal(res.total)
      })
      .catch((err) => setError(err instanceof Error ? err.message : 'yüklenemedi'))
      .finally(() => setLoading(false))
  }

  // eslint-disable-next-line react-hooks/exhaustive-deps
  useEffect(reload, [page, pageSize, q, ...deps])

  // Yeni bir arama ya da sayfa boyutu değişikliği her zaman ilk sayfadan başlar — aksi halde
  // "3. sayfa"dayken arayan biri sonuç yokmuş gibi boş bir tablo görebilir.
  function setSearch(v: string) {
    setQ(v)
    setPage(1)
  }

  function setPageSize(n: number) {
    setPageSizeState(n)
    setPage(1)
  }

  return { items, total, page, setPage, pageSize, setPageSize, q, setSearch, loading, error, setError, reload }
}
