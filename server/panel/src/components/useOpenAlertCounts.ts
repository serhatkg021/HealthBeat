import { useEffect, useState } from 'react'
import { useLocation } from 'react-router-dom'
import { dashboardApi } from '../api/endpoints'
import type { AlertLevel } from '../types/api'

export type OpenAlertCounts = Record<AlertLevel, number>

const REFRESH_MS = 30_000

// Üst çubuktaki açık alert sayıları (Özet sayfasındakilerle aynı kaynak: dashboard özeti). Sayfa değişince ve 30
// saniyede bir yenilenir; alınamazsa son bilinen değer kalır (ilk seferde hiç gösterilmez).
export function useOpenAlertCounts(enabled: boolean): OpenAlertCounts | null {
  const [counts, setCounts] = useState<OpenAlertCounts | null>(null)
  const { pathname } = useLocation()

  useEffect(() => {
    if (!enabled) return
    let alive = true
    const load = () =>
      dashboardApi
        .summary()
        .then((s) => {
          if (alive) setCounts({ info: s.open_info_alerts ?? 0, warning: s.open_warning_alerts, critical: s.open_critical_alerts })
        })
        .catch(() => undefined)
    void load()
    const timer = window.setInterval(load, REFRESH_MS)
    return () => {
      alive = false
      window.clearInterval(timer)
    }
  }, [enabled, pathname])

  return enabled ? counts : null
}
