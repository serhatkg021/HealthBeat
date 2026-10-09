import { useState, type ReactNode } from 'react'
import { Maximize2, type LucideIcon } from 'lucide-react'
import { Modal } from './Modal'

// Grafik kartı: başlık, sağda seçiciler (disk, arayüz) ve büyüt düğmesi, altında açıklama ve grafik(ler). Büyüt, aynı
// grafikleri geniş bir pencerede yüksek çizer (children(true)); pencerede de aralık ve imleç diğer grafiklerle ortaktır,
// Esc ya da dışına tıklamak kapatır.
export function ChartCard({
  title,
  icon: Icon,
  desc,
  actions,
  children,
}: {
  title: string
  icon: LucideIcon
  desc?: ReactNode
  actions?: ReactNode
  children: (large: boolean) => ReactNode
}) {
  const [open, setOpen] = useState(false)
  return (
    <div className="card chart-card">
      <div className="card-title-row">
        <h2 className="card-title">
          <Icon size={16} strokeWidth={1.75} />
          {title}
        </h2>
        <span className="row row-tight">
          {actions}
          <button type="button" className="btn btn-sm btn-ghost chart-zoom" onClick={() => setOpen(true)} aria-label={`${title} grafiğini büyüt`} title="Büyüt">
            <Maximize2 size={14} strokeWidth={1.9} />
          </button>
        </span>
      </div>
      {desc && <p className="card-desc">{desc}</p>}
      {children(false)}
      <Modal open={open} size="lg" title={title} onClose={() => setOpen(false)}>
        {desc && <p className="card-desc">{desc}</p>}
        {open && children(true)}
      </Modal>
    </div>
  )
}
