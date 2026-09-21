import { StatusBadge } from './StatusBadge'
import { agentBadgeInfo, type AgentFields, type AgentPolicy } from '../pages/agentStatus'

// Agent'ın sürümü tek rozet: metin yalnızca sürüm ("v1.3.0"), renk sürüm durumu (yeşil güncel, sarı
// güncellenmeli, kırmızı desteklenmiyor). Durumun sözlü karşılığı fareyle üzerine gelince görünür ve
// ekran okuyucuya okunur; renk tek başına bilgi taşımaz.
export function AgentBadge({ host, policy }: { host: AgentFields; policy: AgentPolicy }) {
  const info = agentBadgeInfo(host, policy)
  return (
    <StatusBadge tone={info.tone} title={info.title}>
      <span className="mono">{info.text}</span>
      <span className="visually-hidden"> — {info.title}</span>
    </StatusBadge>
  )
}
