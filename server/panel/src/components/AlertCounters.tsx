import { Link } from 'react-router-dom'
import { Info, OctagonAlert, TriangleAlert, type LucideIcon } from 'lucide-react'
import { alertLevelLabel } from '../labels'
import type { AlertLevel } from '../types/api'
import type { OpenAlertCounts } from './useOpenAlertCounts'

const LEVELS: { level: AlertLevel; icon: LucideIcon }[] = [
  { level: 'info', icon: Info },
  { level: 'warning', icon: TriangleAlert },
  { level: 'critical', icon: OctagonAlert },
]

// Açık alert'lerin seviye başına sayısı; her biri o seviyenin açık alert listesine götürür.
export function AlertCounters({ counts }: { counts: OpenAlertCounts }) {
  return (
    <div className="alert-counters" role="group" aria-label="Açık alert’ler">
      {LEVELS.map(({ level, icon: Icon }) => {
        const n = counts[level]
        const label = `${n} açık ${alertLevelLabel(level)} alert’i`
        return (
          <Link
            key={level}
            to={`/alerts?seviye=${level}`}
            className={`alert-counter alert-counter-${level}${n === 0 ? ' zero' : ''}`}
            aria-label={label}
            title={label}
          >
            <Icon size={16} strokeWidth={1.9} />
            <span className="tnum">{n}</span>
          </Link>
        )
      })}
    </div>
  )
}
