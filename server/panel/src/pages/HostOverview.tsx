import { Activity, CircleArrowUp, Clock, Cpu, Download, HardDrive, Maximize2, MemoryStick, Timer, Upload, type LucideIcon } from 'lucide-react'
import type { Host, HostThresholdsResponse, MetricPoint } from '../types/api'
import { AgentBadge } from '../components/AgentBadge'
import { EmptyState } from '../components/EmptyState'
import { MountMeter } from '../components/MountMeter'
import { StatTile } from '../components/StatTile'
import { StatusBadge } from '../components/StatusBadge'
import { useNow } from '../components/useNow'
import { UsageBar } from '../components/UsageBar'
import { agentHint, agentKind, needsUpdate, unsupportedFieldsNotice, type AgentPolicy } from './agentStatus'
import { formatCores, formatRamUsage } from './hardwareTotals'
import { statusDuration } from './hostStatus'
import { effectiveLevels, mountLevels, pctText, usageTone, TONE_LABEL } from './usage'
import { hostStatusLabel } from '../labels'

// Sunucu sayfasının "Genel" sekmesi: sunucunun şu anki durumu. Yalnızca en son raporu gösterir; geçmiş
// "Detay" modalında (HostMetricHistory) ayrıca yüklenir. Donanım ve envanter "Sistem" sekmesindedir.
export function HostOverview({
  host,
  latest,
  thresholds,
  policy,
  onDetail,
}: {
  host: Host
  latest: MetricPoint | null
  thresholds: HostThresholdsResponse | null
  policy: AgentPolicy
  onDetail: (kind: 'cpu-ram' | 'disk') => void
}) {
  const online = host.status === 'online'
  // Çevrimiçiyse uptime, çevrimdışıysa son veriden bu yana geçen süre (canlı akar); bkz. statusDuration.
  const now = useNow(5000)
  const duration = statusDuration(host, now)
  const kind = agentKind(host, policy)

  return (
    <div>
      {needsUpdate(kind) && (
        <div className="notice" role="status">
          <div className="notice-title">
            <CircleArrowUp size={16} strokeWidth={1.9} />
            Agent güncellenmeli
          </div>
          {agentHint(kind, policy)}
        </div>
      )}
      {unsupportedFieldsNotice(host.unsupported_fields) && (
        <div className="notice" role="status">
          <div className="notice-title">
            <CircleArrowUp size={16} strokeWidth={1.9} />
            Server güncellenmeli
          </div>
          {unsupportedFieldsNotice(host.unsupported_fields)}
        </div>
      )}

      <div className="stat-grid">
        <StatTile
          label="Durum"
          icon={Activity}
          tone={online ? 'good' : 'critical'}
          small
          value={
            <span className="tile-inline">
              <StatusBadge tone={online ? 'good' : 'critical'}>{hostStatusLabel(host.status)}</StatusBadge>
              {duration && (
                <span className={`tile-uptime${online ? '' : ' tile-offline'}`} title={duration.title}>
                  {duration.text}
                </span>
              )}
            </span>
          }
          hint={duration?.hint}
        />
        <StatTile
          label="Agent"
          icon={host.mode === 'push' ? Upload : Download}
          tone="accent"
          small
          value={<AgentBadge host={host} policy={policy} />}
          hint={host.mode === 'push' ? 'Push modu: agent gönderir' : 'Pull modu: server çeker'}
        />
        <StatTile label="Aralık" icon={Timer} tone="accent" small value={`${host.interval_seconds} sn`} />
        <StatTile label="Son görülme" icon={Clock} small value={host.last_seen ? new Date(host.last_seen).toLocaleString() : '—'} />
      </div>

      {latest ? (
        <div className="grid-2">
          <UsageCard
            title="CPU"
            icon={Cpu}
            pct={latest.cpu_usage_pct}
            detail={formatCores(host.cpu_cores) ?? undefined}
            levels={effectiveLevels(thresholds?.thresholds, 'cpu')}
            onDetail={() => onDetail('cpu-ram')}
          />
          <UsageCard
            title="RAM"
            icon={MemoryStick}
            pct={latest.ram_usage_pct}
            detail={formatRamUsage(latest.ram_usage_pct, host.ram_total_mb) ?? undefined}
            levels={effectiveLevels(thresholds?.thresholds, 'ram')}
            onDetail={() => onDetail('cpu-ram')}
          />
        </div>
      ) : (
        <div className="card">
          <EmptyState icon={Activity}>Henüz metrik verisi yok.</EmptyState>
        </div>
      )}

      <div className="card">
        <div className="card-title-row">
          <h2 className="card-title">
            <HardDrive size={16} strokeWidth={1.75} />
            Disk kullanımı
          </h2>
          <button className="btn btn-sm" type="button" onClick={() => onDetail('disk')} disabled={!latest || latest.disk.length === 0}>
            <Maximize2 size={14} strokeWidth={2} />
            Detay
          </button>
        </div>
        {latest && latest.disk.length > 0 ? (
          <div className="mount-grid">
            {latest.disk.map((d) => (
              <MountMeter
                key={d.mount}
                mount={d.mount}
                usage={d}
                levels={mountLevels(thresholds?.thresholds, thresholds?.mount_thresholds, d.mount)}
              />
            ))}
          </div>
        ) : (
          <EmptyState icon={HardDrive}>Henüz disk verisi yok.</EmptyState>
        )}
      </div>
    </div>
  )
}

// CPU ya da RAM kartı: büyük yüzde, toplamın ne kadarının dolu olduğunu gösteren çubuk ve (biliniyorsa)
// mutlak değer. Çubuk eşik durumuna göre yeşil/sarı/kırmızıdır.
function UsageCard({
  title,
  icon: Icon,
  pct,
  detail,
  levels,
  onDetail,
}: {
  title: string
  icon: LucideIcon
  pct: number
  detail?: string
  levels: ReturnType<typeof effectiveLevels>
  onDetail: () => void
}) {
  const tone = usageTone(pct, levels)
  return (
    <div className="card usage-card">
      <div className="card-title-row">
        <h2 className="card-title">
          <Icon size={16} strokeWidth={1.75} />
          {title}
        </h2>
        <button className="btn btn-sm" type="button" onClick={onDetail}>
          <Maximize2 size={14} strokeWidth={2} />
          Detay
        </button>
      </div>
      <div className="usage-card-value">
        <span className={`usage-card-pct tone-text-${tone}`}>{pctText(pct)}</span>
        {tone !== 'good' && <StatusBadge tone={tone}>{TONE_LABEL[tone]}</StatusBadge>}
        {detail && <span className="usage-card-detail tnum">{detail}</span>}
      </div>
      <UsageBar pct={pct} label={title} levels={levels} />
      <div className="usage-card-foot muted">
        {levels ? `Uyarı %${levels.warning_level} · Kritik %${levels.critical_level}` : 'Bu metrik için eşik tanımlı değil'}
      </div>
    </div>
  )
}
