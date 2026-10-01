import { Link } from 'react-router-dom'
import { TriangleAlert } from 'lucide-react'
import type { SystemNotice } from './useSystemNotices'

// Kayma hızı metnin uzunluğuna göre: uzun şerit de kısa şerit de aynı okunabilir hızda geçer.
const SECONDS_PER_CHAR = 0.22
const MIN_SECONDS = 18

// Alt çubukta kayan uyarı şeridi. Fareyle üzerine gelince ya da odaklanınca durur; "hareketi azalt" tercihinde kaymaz
// (bkz. index.css).
export function SystemTicker({ notices }: { notices: SystemNotice[] }) {
  if (notices.length === 0) return null
  const length = notices.reduce((n, x) => n + x.text.length + 6, 0)
  const duration = Math.max(MIN_SECONDS, Math.round(length * SECONDS_PER_CHAR))
  return (
    <div className="ticker" role="status" aria-label="Sistem uyarıları">
      <div className="ticker-track" style={{ animationDuration: `${duration}s` }}>
        {notices.map((n) => (
          <span key={n.id} className="ticker-item">
            <TriangleAlert size={14} strokeWidth={2} aria-hidden="true" />
            {n.to ? <Link to={n.to}>{n.text}</Link> : <span>{n.text}</span>}
          </span>
        ))}
      </div>
    </div>
  )
}
