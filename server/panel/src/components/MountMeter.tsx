import type { ReactNode } from 'react'
import type { DiskUsage, ThresholdLevels } from '../types/api'
import { formatBytes } from '../pages/hardwareTotals'
import { pctText, usageTone } from '../pages/usage'
import { UsageBar } from './UsageBar'

// Bir mount'un doluluğu: "/storage  1.2 TB / 1.5 TB  %80" ve eşik renginde çubuk. usage yoksa mount
// raporlanmıyordur (agent bu diski göndermiyor).
export function MountMeter({
  mount,
  usage,
  levels,
  note,
}: {
  mount: string
  usage?: DiskUsage
  levels?: ThresholdLevels | null
  note?: ReactNode
}) {
  if (!usage) {
    return (
      <div className="mount-meter">
        <div className="mount-meter-head">
          <span className="mono mount-meter-name">{mount}</span>
          <span className="muted">raporlanmıyor</span>
        </div>
        {note && <div className="mount-meter-note">{note}</div>}
      </div>
    )
  }
  const used = Math.max(0, usage.total - usage.free)
  const tone = usageTone(usage.used_pct, levels)
  return (
    <div className="mount-meter">
      <div className="mount-meter-head">
        <span className="mono mount-meter-name" title={mount}>{mount}</span>
        <span className={`mount-meter-pct tone-text-${tone}`}>{pctText(usage.used_pct)}</span>
      </div>
      <UsageBar pct={usage.used_pct} label={mount} levels={levels} size="sm" />
      <div className="mount-meter-foot">
        <span className="tnum">
          {formatBytes(used)} / {formatBytes(usage.total)}
        </span>
        {usage.inodes_used_pct !== undefined && <span className="muted tnum">inode {pctText(usage.inodes_used_pct)}</span>}
      </div>
      {note && <div className="mount-meter-note">{note}</div>}
    </div>
  )
}
