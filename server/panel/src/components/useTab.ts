import { useCallback } from 'react'
import { useSearchParams } from 'react-router-dom'
import { resolveTab, TAB_PARAM } from '../tabs'

// Etkin sekmeyi URL'de (`?sekme=`) tutar: yenileme ve geri tuşu sekmeyi korur, bağlantı paylaşılabilir.
// Varsayılan sekme adreste görünmez. `param` başka bir menü (ör. ayarlar bölümleri) için farklı bir
// parametre adı seçmeye yarar.
export function useTab(ids: readonly string[], fallback: string, param: string = TAB_PARAM): [string, (id: string) => void] {
  const [params, setParams] = useSearchParams()
  const active = resolveTab(params.get(param), ids, fallback)
  const setActive = useCallback(
    (id: string) => {
      setParams(
        (prev) => {
          const next = new URLSearchParams(prev)
          if (id === fallback) next.delete(param)
          else next.set(param, id)
          return next
        },
        { replace: true },
      )
    },
    [setParams, fallback, param],
  )
  return [active, setActive]
}
