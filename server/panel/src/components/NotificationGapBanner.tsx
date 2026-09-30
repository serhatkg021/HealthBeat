import { useEffect, useState } from 'react'
import { Link, useLocation } from 'react-router-dom'
import { TriangleAlert } from 'lucide-react'
import { channelsApi, ownersApi } from '../api/endpoints'
import { SETTINGS_CHANGED, notificationGap } from '../pages/settingsForm'

// Alert bildirimleri kimseye gitmiyorsa (e-posta kanalı kapalı ya da e-posta alan sistem sahibi yok) sayfanın üstünde
// uyarır. Yalnızca ayarları görebilenlere gösterilir (çağıran denetler). Sayfa değişince ve Ayarlar'da kanal ya da sahip
// değişince yeniden bakar.
export function NotificationGapBanner() {
  const [gap, setGap] = useState<string | null>(null)
  const { pathname } = useLocation()

  useEffect(() => {
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
  }, [pathname])

  if (!gap) return null
  return (
    <div className="notice settings-alert" role="status">
      <TriangleAlert size={16} strokeWidth={1.9} />
      <span>{gap}</span>
      {!pathname.startsWith('/settings') && <Link to="/settings">Ayarlar’a git</Link>}
    </div>
  )
}
