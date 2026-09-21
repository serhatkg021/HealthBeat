import { Box, Container, Cpu, MemoryStick, Play, Square } from 'lucide-react'
import type { Host, HostThresholdsResponse, DockerContainerReport } from '../types/api'
import { EmptyState } from '../components/EmptyState'
import { StatTile } from '../components/StatTile'
import { StatusBadge } from '../components/StatusBadge'
import { containerStatusLabel } from '../labels'
import { dockerSummary, formatMB } from './docker'
import { formatUptime } from './inventory'
import { containerLevels, usageTone } from './usage'

// Sunucu sayfasının "Docker" sekmesi: Docker sürümü, container sayıları/kaynak toplamı ve container listesi.
export function HostDocker({
  host,
  containers,
  thresholds,
}: {
  host: Host
  containers: DockerContainerReport[]
  thresholds: HostThresholdsResponse | null
}) {
  const s = dockerSummary(containers)
  const version = host.host_info?.docker_version

  return (
    <div>
      <div className="stat-grid">
        <StatTile label="Docker sürümü" icon={Container} tone="accent" small value={version || '—'} hint={version ? undefined : 'bildirilmedi'} />
        <StatTile label="Container" icon={Box} tone="accent" value={s.total} hint={s.restarted > 0 ? `${s.restarted} tanesi yeniden başlamış` : undefined} />
        <StatTile label="Çalışan" icon={Play} tone="good" value={s.running} />
        <StatTile label="Çalışmayan" icon={Square} tone={s.notRunning > 0 ? 'warning' : undefined} value={s.notRunning} />
        <StatTile label="Toplam CPU" icon={Cpu} tone="accent" small value={`%${s.cpuPct.toFixed(1)}`} hint="çalışan container'lar" />
        <StatTile label="Toplam RAM" icon={MemoryStick} tone="accent" small value={formatMB(s.ramMB)} hint="çalışan container'lar" />
      </div>

      <div className="card table-card">
        <h2 className="card-title">
          <Box size={16} strokeWidth={1.75} />
          Container'lar
        </h2>
        <table className="stack">
          <thead>
            <tr>
              <th>Ad</th>
              <th>Image</th>
              <th>Durum</th>
              <th>CPU</th>
              <th>RAM</th>
              <th>Çalışma süresi</th>
              <th>Restart</th>
            </tr>
          </thead>
          <tbody>
            {containers.map((c) => {
              const running = c.status === 'running'
              const tone = usageTone(c.restart_count, containerLevels(thresholds?.thresholds, thresholds?.container_thresholds, c.name))
              return (
                <tr key={c.name}>
                  <td className="primary mono">{c.name}</td>
                  <td className="muted mono cell-ellipsis" data-label="Image" title={c.image}>{c.image}</td>
                  <td data-label="Durum">
                    <StatusBadge tone={running ? 'good' : 'neutral'}>{containerStatusLabel(c.status)}</StatusBadge>
                  </td>
                  <td className="tnum" data-label="CPU">{c.cpu_pct.toFixed(1)}%</td>
                  <td className="tnum" data-label="RAM">{formatMB(c.ram_mb)}</td>
                  <td className="tnum" data-label="Çalışma süresi">{running ? formatUptime(c.uptime_seconds) : '—'}</td>
                  <td className="tnum" data-label="Restart">
                    {tone === 'good' ? c.restart_count : <StatusBadge tone={tone}>{c.restart_count}</StatusBadge>}
                  </td>
                </tr>
              )
            })}
            {containers.length === 0 && (
              <tr>
                <td colSpan={7} className="empty-cell">
                  <EmptyState icon={Box}>Docker container'ı bulunamadı.</EmptyState>
                </td>
              </tr>
            )}
          </tbody>
        </table>
      </div>
    </div>
  )
}
