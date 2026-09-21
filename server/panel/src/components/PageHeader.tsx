import type { ReactNode } from 'react'
import { Link } from 'react-router-dom'
import { ChevronLeft } from 'lucide-react'

interface Props {
  title: ReactNode
  subtitle?: ReactNode
  back?: { to: string; label: string }
  // Başlığın yanında (ör. durum rozeti) ve sağ üstte (ör. düğmeler) gösterilenler.
  badge?: ReactNode
  actions?: ReactNode
}

export function PageHeader({ title, subtitle, back, badge, actions }: Props) {
  return (
    <div className="page-header">
      <div className="page-header-main">
        {back && (
          <Link to={back.to} className="back-link">
            <ChevronLeft size={15} strokeWidth={2} />
            {back.label}
          </Link>
        )}
        <h1 className="page-title">
          {title}
          {badge}
        </h1>
        {subtitle && <p className="page-subtitle">{subtitle}</p>}
      </div>
      {actions && <div className="page-actions">{actions}</div>}
    </div>
  )
}
