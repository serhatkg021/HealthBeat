import type { ReactNode } from 'react'
import { Sparkles, type LucideIcon } from 'lucide-react'

// Henüz gelmemiş bir özelliğin yeri: kesik çerçeveli, soluk bir kart ve "Yakında · örnek veri" rozeti. İçerik yalnızca
// özelliğin nasıl görüneceğini anlatan ÖRNEK veridir (bkz. comingSoonSamples.ts); tıklanamaz, odak almaz ve hiçbir gerçek
// sayaca karışmaz. Özellik gelince bu kart gerçek bileşenle değiştirilir.
export function ComingSoon({
  title,
  icon: Icon = Sparkles,
  description,
  children,
}: {
  title: string
  icon?: LucideIcon
  description: string
  children?: ReactNode
}) {
  return (
    <section className="card coming-soon" aria-label={`${title} (yakında, örnek veri)`}>
      <div className="card-title-row">
        <h2 className="card-title">
          <Icon size={16} strokeWidth={1.75} />
          {title}
        </h2>
        <span className="coming-soon-badge">Yakında · örnek veri</span>
      </div>
      <p className="card-desc">{description}</p>
      {children && (
        <div className="coming-soon-body" inert>
          {children}
        </div>
      )}
    </section>
  )
}
