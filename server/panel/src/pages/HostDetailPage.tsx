import { useEffect, useState } from 'react'
import { useParams } from 'react-router-dom'
import { hostsApi } from '../api/endpoints'
import { useAuth } from '../auth/AuthContext'
import { HostAlerts } from './HostAlerts'
import { HostDocker } from './HostDocker'
import { HostMetricHistory } from './HostMetricHistory'
import { HostOverview } from './HostOverview'
import { HostSettings } from './HostSettings'
import { HostSystem } from './HostSystem'
import { useAgentPolicy } from '../components/useAgentPolicy'
import type { Host, HostThresholdsResponse, DockerContainerReport, MetricPoint } from '../types/api'
import { StatusBadge } from '../components/StatusBadge'
import { PageHeader } from '../components/PageHeader'
import { Link } from 'react-router-dom'
import { Bell, Container, LayoutDashboard, MonitorCog, Settings, TriangleAlert } from 'lucide-react'
import { TabPanel, Tabs, type TabItem } from '../components/Tabs'
import { useTab } from '../components/useTab'
import { hostStatusLabel } from '../labels'
import { useDocumentTitle } from '../components/useDocumentTitle'

const TAB_IDS = ['genel', 'sistem', 'docker', 'alertler', 'ayarlar']

const TAB_ITEMS: TabItem[] = [
  { id: 'genel', label: 'Genel', icon: LayoutDashboard },
  { id: 'sistem', label: 'Sistem', icon: MonitorCog },
  { id: 'docker', label: 'Docker', icon: Container },
  { id: 'alertler', label: 'Alert’ler', icon: Bell },
  { id: 'ayarlar', label: 'Ayarlar', icon: Settings },
]

export function HostDetailPage() {
  const { id } = useParams<{ id: string }>()
  const { user } = useAuth()
  const canEdit = user?.role === 'super_admin' || user?.role === 'org_admin' // server ile aynı kural (host.update)

  const agentPolicy = useAgentPolicy()
  const [host, setHost] = useState<Host | null>(null)
  // Son rapor edilen değerler — "Genel" sekmesi grafik değil, en güncel durumu gösterir; geçmiş bir "Detay"
  // modalında ayrıca ve isteğe bağlı bir tarih aralığıyla yüklenir (bkz. HostMetricHistory).
  const [latest, setLatest] = useState<MetricPoint | null>(null)
  const [containers, setContainers] = useState<DockerContainerReport[]>([])
  // Çubuk renkleri için sunucunun geçerli eşikleri (yüklenemezse çubuklar eşiksiz, yeşil kalır).
  const [thresholds, setThresholds] = useState<HostThresholdsResponse | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [historyModal, setHistoryModal] = useState<'cpu-ram' | 'disk' | null>(null)

  function reload() {
    if (!id) return
    hostsApi
      .get(id)
      .then(setHost)
      .catch((err) => setError(err instanceof Error ? err.message : 'sunucu yüklenemedi'))
    hostsApi.latestMetric(id).then(setLatest).catch(() => undefined)
    hostsApi.docker(id).then(setContainers).catch(() => undefined)
    hostsApi.thresholds(id).then(setThresholds).catch(() => undefined)
  }

  function reloadThresholds() {
    if (!id) return
    hostsApi.thresholds(id).then(setThresholds).catch(() => undefined)
  }

  useEffect(reload, [id])

  const [tab, setTab] = useTab(TAB_IDS, 'genel')
  useDocumentTitle(host?.title)

  return (
    <div>
      <PageHeader
        back={host ? { to: `/organizations/${host.organization_id}`, label: 'Organizasyon' } : undefined}
        title={host?.title ?? 'Sunucu'}
        badge={host && <StatusBadge tone={host.status === 'online' ? 'good' : 'critical'}>{hostStatusLabel(host.status)}</StatusBadge>}
        subtitle={host && <span className="mono">{host.ip}</span>}
      />
      {error && <div className="error-banner">{error}</div>}
      {host?.same_machine_as && host.same_machine_as.length > 0 && (
        <div className="notice notice-warning same-machine-warning">
          <div className="notice-title">
            <TriangleAlert size={16} strokeWidth={1.9} />
            Aynı makine birden çok kez kayıtlı olabilir
          </div>
          Bu sunucu, makine kimliği (machine-id) aynı olan şu sunucularla eşleşiyor:{' '}
          {host.same_machine_as.map((m, i) => (
            <span key={m.id}>
              {i > 0 && ', '}
              <Link to={`/hosts/${m.id}`}>{m.title}</Link>
            </span>
          ))}
          . Aynı makine iki kez eklenmişse birini silin; klonlanmış bir sanal makineyse bu beklenen bir durumdur.
        </div>
      )}

      <Tabs items={TAB_ITEMS} active={tab} onChange={setTab} label="Sunucu bölümleri" />

      <TabPanel id="genel" active={tab}>
        {host && <HostOverview host={host} latest={latest} thresholds={thresholds} policy={agentPolicy} onDetail={setHistoryModal} />}
      </TabPanel>

      <TabPanel id="sistem" active={tab}>
        {host && <HostSystem host={host} disk={latest?.disk ?? []} thresholds={thresholds} />}
      </TabPanel>

      <TabPanel id="docker" active={tab}>
        {host && <HostDocker host={host} containers={containers} thresholds={thresholds} />}
      </TabPanel>

      {id && (
        <TabPanel id="alertler" active={tab} keepMounted>
          <HostAlerts hostId={id} />
        </TabPanel>
      )}

      {host && (
        <TabPanel id="ayarlar" active={tab} keepMounted>
          <HostSettings host={host} canEdit={canEdit} onChanged={reload} onError={setError} onThresholdsSaved={reloadThresholds} />
        </TabPanel>
      )}

      {id && (
        <HostMetricHistory
          hostId={id}
          kind={historyModal ?? 'cpu-ram'}
          open={historyModal !== null}
          onClose={() => setHistoryModal(null)}
        />
      )}
    </div>
  )
}
