import { useContext, type ReactNode } from 'react'
import { createPortal } from 'react-dom'
import { Link } from 'react-router-dom'
import { ChevronLeft } from 'lucide-react'
import { HeaderSlotContext } from './headerSlot'

interface Props {
  title: ReactNode
  subtitle?: ReactNode
  back?: { to: string; label: string }
  // Başlığın yanında (ör. durum rozeti) ve sağ üstte (ör. düğmeler) gösterilenler.
  badge?: ReactNode
  actions?: ReactNode
}

// Başlık (geri bağlantısı ve rozetiyle) sabit üst çubuğa çizilir; alt başlık ve düğmeler sayfanın içinde kalır.
export function PageHeader({ title, subtitle, back, badge, actions }: Props) {
  const slot = useContext(HeaderSlotContext)
  const heading = (
    <>
      {back && (
        <Link to={back.to} className="back-link" aria-label={`Geri: ${back.label}`} title={back.label}>
          <ChevronLeft size={18} strokeWidth={2} />
        </Link>
      )}
      <h1 className="page-title">
        <span className="page-title-text">{title}</span>
        {badge}
      </h1>
    </>
  )
  return (
    <>
      {slot && createPortal(heading, slot)}
      {(!slot || subtitle || actions) && (
        <div className="page-header">
          <div className="page-header-main">
            {!slot && heading}
            {subtitle && <p className="page-subtitle">{subtitle}</p>}
          </div>
          {actions && <div className="page-actions">{actions}</div>}
        </div>
      )}
    </>
  )
}
