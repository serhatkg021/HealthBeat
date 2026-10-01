import { useEffect, useState } from 'react'
import { useLocation } from 'react-router-dom'
import { channelsApi, ownersApi } from '../api/endpoints'
import { SETTINGS_CHANGED, notificationGap } from '../pages/settingsForm'

export interface SystemNotice {
  id: string
  text: string
  // Uyarının giderildiği sayfa.
  to?: string
}

// Alt çubuktaki şeritte gösterilen kurulum geneli uyarılar. Bildirim boşluğu (e-posta kanalı kapalı ya da e-posta alan
// sistem sahibi yok) yalnızca ayarları görebilenlere sorulur; sayfa değişince ve Ayarlar'da kanal ya da sahip değişince
// yeniden bakılır. Sürüm uyumsuzluğu herkese gösterilir.
export function useSystemNotices(canViewSettings: boolean, versionMismatch: string | null): SystemNotice[] {
  const [gap, setGap] = useState<string | null>(null)
  const { pathname } = useLocation()

  useEffect(() => {
    if (!canViewSettings) return
    let alive = true
    const check = () =>
      Promise.all([channelsApi.list(), ownersApi.list()])
        .then(([channels, owners]) => alive && setGap(notificationGap(channels, owners)))
        .catch(() => undefined) // uyarı en iyi çabadır; asıl sayfa kendi hatasını gösterir
    void check()
    window.addEventListener(SETTINGS_CHANGED, check)
    return () => {
      alive = false
      window.removeEventListener(SETTINGS_CHANGED, check)
    }
  }, [canViewSettings, pathname])

  const notices: SystemNotice[] = []
  if (canViewSettings && gap) notices.push({ id: 'bildirim', text: gap, to: '/settings/system' })
  if (versionMismatch) notices.push({ id: 'surum', text: versionMismatch })
  return notices
}
