import type { MaintenanceOccurrence } from '../types/api'
import { formatDate, occurrenceText } from './maintenance'

// OCCURRENCE_LIMIT, detayda ve formun önizlemesinde gösterilen sonraki tekrar sayısıdır.
export const OCCURRENCE_LIMIT = 3

// Sonraki tekrarlar: eşit genişlikte hücrelerden oluşan ızgara (geniş ekranda 3, dar ekranda daha az sütun). Atlanmış
// tekrarlar listede yoktur; neden eksik oldukları altta bir notla söylenir.
export function OccurrenceGrid({ occurrences, skipped = [] }: { occurrences: MaintenanceOccurrence[]; skipped?: string[] }) {
  const shown = occurrences.slice(0, OCCURRENCE_LIMIT)
  return (
    <div className="occurrence-block">
      {shown.length === 0 ? (
        <span className="muted">Bundan sonra tekrar yok.</span>
      ) : (
        <ul className="occurrence-grid">
          {shown.map((o) => (
            <li key={o.start} className="tnum">
              {occurrenceText(o.start_local, o.end_local)}
            </li>
          ))}
        </ul>
      )}
      {skipped.length > 0 && (
        <span className="muted occurrence-note">
          {skipped.length} tekrar atlandı ({skipped.map((s) => formatDate(s.slice(0, 10))).join(', ')})
        </span>
      )}
    </div>
  )
}
