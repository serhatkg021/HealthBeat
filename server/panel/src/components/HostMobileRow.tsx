import { Link } from 'react-router-dom'
import type { Host } from '../types/api'
import { agoText } from '../pages/cache'
import { agentKind, needsUpdate, type AgentFields, type AgentPolicy } from '../pages/agentStatus'
import { hostStatusLabel } from '../labels'
import { AgentBadge } from './AgentBadge'
import { MaintenanceBadge } from './MaintenanceBadge'
import { StatusBadge } from './StatusBadge'

// Sunucu listelerinin dar ekrandaki iki satırı (compact-mobile tablolar): üstte durum noktası ve ad, sağda açık alert'ler,
// bakım ve (yalnızca güncellenmesi gerekiyorsa) agent rozeti; altta organizasyon, IP ve çevrimdışıysa ne zamandır
// görülmediği. Satırın tamamı sunucu sayfasına götürür. Masaüstünde gizlidir; tablonun ilk hücresi olarak verilir.
export function HostMobileRow({
  host,
  orgName,
  critical = 0,
  warning = 0,
  policy,
  now,
}: {
  host: Pick<Host, 'id' | 'title' | 'ip' | 'status' | 'last_seen' | 'maintenance_until_local'> & AgentFields
  orgName?: string
  critical?: number
  warning?: number
  policy: AgentPolicy
  now: number
}) {
  const online = host.status === 'online'
  const sub = [orgName, host.ip, !online && host.last_seen ? `${agoText(host.last_seen, now)} görüldü` : null].filter(Boolean).join(' · ')
  return (
    <td className="mobile-row">
      <div className="mobile-row-top">
        <Link className="mobile-row-link mobile-row-title" to={`/hosts/${host.id}`}>
          <span className={`status-dot${online ? ' online' : ''}`} title={hostStatusLabel(host.status)} />
          <span className="visually-hidden">{hostStatusLabel(host.status)}: </span>
          {host.title}
        </Link>
        <span className="mobile-row-badges">
          {critical > 0 && <StatusBadge tone="critical">{critical} kritik</StatusBadge>}
          {warning > 0 && <StatusBadge tone="warning">{warning} uyarı</StatusBadge>}
          <MaintenanceBadge untilLocal={host.maintenance_until_local} />
          {needsUpdate(agentKind(host, policy)) && <AgentBadge host={host} policy={policy} />}
        </span>
      </div>
      <div className="mobile-row-sub">
        <span>{sub}</span>
      </div>
    </td>
  )
}
