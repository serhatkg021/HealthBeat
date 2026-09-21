import type { ThresholdLevels } from '../types/api'
import { clampPct, usageAriaLabel, usageTone, type UsageTone } from '../pages/usage'

// Yüzde çubuğu: toplam alan izi, kullanılan kısım eşik durumuna göre boyalı (normal/uyarı/kritik).
// Eşikler izin üzerinde ince işaretlerle görünür. Renk tek başına bilgi taşımaz: değer yanında yazılır,
// çubuğun kendisi de sözlü bir etiket alır.
export function UsageBar({
  pct,
  label,
  levels,
  size = 'md',
}: {
  pct: number
  label: string
  levels?: ThresholdLevels | null
  size?: 'md' | 'sm'
}) {
  const tone: UsageTone = usageTone(pct, levels)
  const width = clampPct(pct)
  return (
    <div
      className={`usage-bar usage-${tone} usage-${size}`}
      role="progressbar"
      aria-valuemin={0}
      aria-valuemax={100}
      aria-valuenow={Math.round(width)}
      aria-label={usageAriaLabel(label, pct, tone)}
    >
      <div className="usage-fill" style={{ width: `${width}%` }} />
      {levels && (
        <>
          <span className="usage-tick usage-tick-warning" style={{ left: `${clampPct(levels.warning_level)}%` }} title={`Uyarı eşiği %${levels.warning_level}`} />
          <span className="usage-tick usage-tick-critical" style={{ left: `${clampPct(levels.critical_level)}%` }} title={`Kritik eşik %${levels.critical_level}`} />
        </>
      )}
    </div>
  )
}
