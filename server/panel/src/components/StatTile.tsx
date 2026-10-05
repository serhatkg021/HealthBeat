import type { ReactNode } from 'react'
import { Link } from 'react-router-dom'
import type { LucideIcon } from 'lucide-react'

interface Props {
  label: string
  value: ReactNode
  // Uzun metin değerleri (ör. tarih) için daha küçük yazı.
  small?: boolean
  icon?: LucideIcon
  tone?: 'good' | 'warning' | 'critical' | 'accent'
  // Değerin altındaki küçük, sönük açıklama satırı.
  hint?: ReactNode
  // Verilirse kutucuk o sayfaya giden bir bağlantıdır (ör. Özet'ten Sunucular'a süzgeçle).
  to?: string
}

export function StatTile({ label, value, icon: Icon, tone, small, hint, to }: Props) {
  const body = (
    <>
      <div className="stat-tile-head">
        <div className="stat-tile-label">{label}</div>
        {Icon && (
          <span className={`stat-tile-icon${tone ? ` tone-${tone}` : ''}`}>
            <Icon size={15} strokeWidth={1.9} />
          </span>
        )}
      </div>
      <div className={`stat-tile-value${small ? ' small' : ''}`}>{value}</div>
      {hint && <div className="stat-tile-hint">{hint}</div>}
    </>
  )
  return to ? (
    <Link to={to} className="stat-tile stat-tile-link">
      {body}
    </Link>
  ) : (
    <div className="stat-tile">{body}</div>
  )
}
