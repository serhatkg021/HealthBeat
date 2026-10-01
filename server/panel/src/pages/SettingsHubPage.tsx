import { Link, Navigate } from 'react-router-dom'
import { ChevronRight, ScrollText, Settings, SlidersHorizontal, type LucideIcon } from 'lucide-react'
import { useAuth } from '../auth/AuthContext'
import { PageHeader } from '../components/PageHeader'
import { navigation } from '../navigation'

const ICONS: Record<string, LucideIcon> = {
  esikler: SlidersHorizontal,
  denetim: ScrollText,
  sistem: Settings,
}

// Ayarlar girişi: kurulum geneli sayfalara (eşikler, denetim kaydı, sistem ayarları) götüren kartlar. Her kart yalnızca
// izni olana görünür; hiçbirini göremeyen buraya gelemez.
export function SettingsHubPage() {
  const { can } = useAuth()
  const cards = navigation(can).settings
  if (cards.length === 0) return <Navigate to="/" replace />
  return (
    <div>
      <PageHeader title="Ayarlar" subtitle="Kurulum genelindeki ayarlar ve kayıtlar" />
      <div className="hub-grid">
        {cards.map((card) => {
          const Icon = ICONS[card.id]
          return (
            <Link key={card.id} to={card.to} className="card hub-card">
              <span className="hub-card-icon">{Icon && <Icon size={20} strokeWidth={1.75} />}</span>
              <span className="hub-card-text">
                <span className="hub-card-title">{card.label}</span>
                <span className="hub-card-description">{card.description}</span>
              </span>
              <ChevronRight size={18} strokeWidth={1.75} className="hub-card-arrow" aria-hidden="true" />
            </Link>
          )
        })}
      </div>
    </div>
  )
}
