import type { ReactNode } from 'react'
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
}

export function StatTile({ label, value, icon: Icon, tone, small, hint }: Props) {
  return (
    <div className="stat-tile">
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
    </div>
  )
}
