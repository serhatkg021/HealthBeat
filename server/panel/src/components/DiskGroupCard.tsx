import { HardDrive } from 'lucide-react'
import type { HostThresholdsResponse } from '../types/api'
import type { DiskGroup } from '../pages/diskLayout'
import { shareOfDisk, usedBytesOf } from '../pages/diskLayout'
import { diskKindLabel, formatBytes } from '../pages/hardwareTotals'
import { mountLevels, usageTone } from '../pages/usage'
import { MountMeter } from './MountMeter'

// Bir fiziksel disk: üstte diskin tamamını temsil eden bölünmüş çubuk (her mount kendi boyuyla, kullanılan
// kısmı eşik renginde), altında mount başına ayrıntı. Böylece 2 TB diskte 1.5 TB /storage ile 500 GB /
// olduğu ve her birinin ne kadarının dolu olduğu tek bakışta görülür.
export function DiskGroupCard({ group, thresholds }: { group: DiskGroup; thresholds?: HostThresholdsResponse | null }) {
  const { disk, mounts, otherBytes } = group
  const size = disk.size_bytes ?? 0
  const segments = mounts.filter((m) => m.usage && !m.shared)

  return (
    <div className="card disk-group">
      <div className="disk-group-head">
        <span className="disk-group-icon">
          <HardDrive size={16} strokeWidth={1.9} />
        </span>
        <div className="disk-group-title">
          <span className="mono disk-group-name">{disk.name}</span>
          <span className="muted">{[disk.model, diskKindLabel(disk.kind) === '—' ? '' : diskKindLabel(disk.kind)].filter(Boolean).join(' · ') || 'Model bilinmiyor'}</span>
        </div>
        <div className="disk-group-size tnum" title="Aygıtın ham boyutu">{formatBytes(size)}</div>
      </div>

      {size > 0 && segments.length > 0 && (
        <>
          <div className="disk-split" role="img" aria-label={`${disk.name} bölümleri: ${segments.map((m) => `${m.mount} ${formatBytes(m.usage!.total)}`).join(', ')}`}>
            {segments.map((m) => {
              const share = shareOfDisk(disk, m) ?? 0
              const tone = usageTone(m.usage!.used_pct, mountLevels(thresholds?.thresholds, thresholds?.mount_thresholds, m.mount))
              return (
                <div key={m.mount} className="disk-split-seg" style={{ flexGrow: share, flexBasis: 0 }} title={`${m.mount}: ${formatBytes(usedBytesOf(m.usage!))} / ${formatBytes(m.usage!.total)}`}>
                  <div className={`disk-split-fill usage-${tone}`} style={{ width: `${Math.min(100, m.usage!.used_pct)}%` }} />
                  {share >= 14 && <span className="disk-split-label mono">{m.mount}</span>}
                </div>
              )
            })}
            {otherBytes > 0 && (
              <div className="disk-split-seg disk-split-other" style={{ flexGrow: (otherBytes / size) * 100, flexBasis: 0 }} title={`Dosya sistemine ayrılmayan: ${formatBytes(otherBytes)}`} />
            )}
          </div>
          {otherBytes > 0 && <div className="disk-split-legend muted">Açık gri: dosya sistemine bağlı olmayan alan (bölüm tablosu, swap, ayrılmamış) · {formatBytes(otherBytes)}</div>}
        </>
      )}

      <div className="mount-grid">
        {mounts.length === 0 && <div className="muted">Bu diske bağlı mount yok.</div>}
        {mounts.map((m) => (
          <MountMeter
            key={m.mount}
            mount={m.mount}
            usage={m.usage}
            levels={mountLevels(thresholds?.thresholds, thresholds?.mount_thresholds, m.mount)}
            note={
              m.shared
                ? 'Birden çok fiziksel diske yayılıyor (LVM/RAID); boyutu disklere bölünemez.'
                : size > 0 && m.usage
                  ? `Diskin %${(shareOfDisk(disk, m) ?? 0).toFixed(0)}’i`
                  : undefined
            }
          />
        ))}
      </div>
    </div>
  )
}
