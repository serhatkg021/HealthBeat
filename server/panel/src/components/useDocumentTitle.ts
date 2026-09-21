import { useEffect } from 'react'
import { fullTitle } from '../pageTitle'

// Sekme başlığını ayarlar; ayrılırken uygulama adına döner. Başlık henüz yoksa (veri yükleniyor) dokunmaz.
export function useDocumentTitle(title?: string | null): void {
  useEffect(() => {
    if (!title) return
    document.title = fullTitle(title)
    return () => {
      document.title = fullTitle()
    }
  }, [title])
}
