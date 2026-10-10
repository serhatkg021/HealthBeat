import type { MaintenanceOccurrence } from '../types/api'
import { occurrenceText } from './maintenance'

// OCCURRENCE_LIMIT, detayda ve formun önizlemesinde gösterilen sonraki tekrar sayısıdır.
export const OCCURRENCE_LIMIT = 3

// Sonraki tekrarlar: eşit genişlikte hücrelerden oluşan ızgara (geniş ekranda 3, dar ekranda daha az sütun). Atlanmış
// tekrarlar listede yoktur; neden eksik oldukları altta bir notla söylenir.
export function OccurrenceGrid({
  occurrences,
  skipped = [],
  currentYear,
}: {
  occurrences: MaintenanceOccurrence[]
  skipped?: string[]
  currentYear?: number
}) {
  const shown = occurrences.slice(0, OCCURRENCE_LIMIT)
  return (
    <div className="occurrence-block">
      {shown.length === 0 ? (
        <span className="muted">Bundan sonra tekrar yok.</span>
      ) : (
        <ul className="occurrence-grid">
          {shown.map((o) => (
            <li key={o.start} className="tnum">
              {occurrenceText(o.start_local, o.end_local, currentYear)}
            </li>
          ))}
        </ul>
      )}
      {skipped.length > 0 && (
        <span className="muted occurrence-note">
          {skipped.length} tekrar atlandı ({skipped.map((s) => `${s.slice(8, 10)}.${s.slice(5, 7)}`).join(', ')})
        </span>
      )}
    </div>
  )
}
