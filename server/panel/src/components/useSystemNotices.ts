import { useEffect, useState } from 'react'
import { useLocation } from 'react-router-dom'
import { channelsApi, notificationsApi } from '../api/endpoints'
import { notificationsPath } from '../navigation'
import { SETTINGS_CHANGED, notificationGap, type NotificationGap } from '../pages/settingsForm'

export interface SystemNotice {
  id: string
  text: string
  // Uyarının giderildiği sayfa.
  to?: string
}

// Alt çubuktaki şeritte gösterilen kurulum geneli uyarılar. Bildirim boşluğu (e-posta kanalı kapalı ya da e-posta alan
// sistem kuralı yok) yalnızca ayarları görebilenlere sorulur; sayfa değişince ve kanal, sistem kişisi ya da sistem kuralı
// değişince yeniden bakılır. Sürüm uyumsuzluğu herkese gösterilir.
export function useSystemNotices(canViewSettings: boolean, versionMismatch: string | null): SystemNotice[] {
  const [gap, setGap] = useState<NotificationGap | null>(null)
  const { pathname } = useLocation()

  useEffect(() => {
    if (!canViewSettings) return
    let alive = true
    const check = () =>
      Promise.all([channelsApi.list(), notificationsApi.list({ system: true })])
        .then(([channels, rules]) => alive && setGap(notificationGap(channels, rules)))
        .catch(() => undefined) // uyarı en iyi çabadır; asıl sayfa kendi hatasını gösterir
    void check()
    window.addEventListener(SETTINGS_CHANGED, check)
    return () => {
      alive = false
      window.removeEventListener(SETTINGS_CHANGED, check)
    }
  }, [canViewSettings, pathname])

  const notices: SystemNotice[] = []
  if (canViewSettings && gap) {
    const to = gap.fix === 'system-rules' ? notificationsPath('kurallar', { kind: 'sistem' }) : notificationsPath('kanallar')
    notices.push({ id: 'bildirim', text: gap.text, to })
  }
  if (versionMismatch) notices.push({ id: 'surum', text: versionMismatch })
  return notices
}
