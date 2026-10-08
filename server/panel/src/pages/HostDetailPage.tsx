import { useEffect, useState } from 'react'
import { useParams } from 'react-router-dom'
import { hostsApi } from '../api/endpoints'
import { HostAlerts } from './HostAlerts'
import { HostInventory } from './HostInventory'
import { HostOverview } from './HostOverview'
import { HostPerformance } from './HostPerformance'
import { HostServices } from './HostServices'
import { HostSettings } from './HostSettings'
import { useAgentPolicy } from '../components/useAgentPolicy'
import type { Host, HostThresholdsResponse, DockerContainerReport, MetricPoint } from '../types/api'
import { StatusBadge } from '../components/StatusBadge'
import { PageHeader } from '../components/PageHeader'
import { Link } from 'react-router-dom'
import { Bell, Boxes, ChartLine, LayoutDashboard, MonitorCog, Settings, TriangleAlert } from 'lucide-react'
import { TabPanel, Tabs, type TabItem } from '../components/Tabs'
import { useTab } from '../components/useTab'
import { hostStatusLabel } from '../labels'
import { useDocumentTitle } from '../components/useDocumentTitle'

// Sekmeler sabittir: yeni bir özellik yeni sekme açmaz, bu altısından birine girer (zamana bağlı metrikler
// Performans'a, sunucuda çalışanlar Servisler'e, değişmeyen bilgiler Envanter'e).
const TAB_IDS = ['genel', 'performans', 'servisler', 'envanter', 'alertler', 'ayarlar']

const TAB_ITEMS: TabItem[] = [
  { id: 'genel', label: 'Genel', icon: LayoutDashboard },
  { id: 'performans', label: 'Performans', icon: ChartLine },
  { id: 'servisler', label: 'Servisler', icon: Boxes },
  { id: 'envanter', label: 'Envanter', icon: MonitorCog },
  { id: 'alertler', label: 'Alert’ler', icon: Bell },
  { id: 'ayarlar', label: 'Ayarlar', icon: Settings },
]

// Eski sekme adları (yer imleri, eski bağlantılar) yeni yerlerine açılır.
const TAB_ALIASES = { sistem: 'envanter', docker: 'servisler' }

export function HostDetailPage() {
  const { id } = useParams<{ id: string }>()

  const agentPolicy = useAgentPolicy()
  const [host, setHost] = useState<Host | null>(null)
  // Son rapor edilen değerler — "Genel" sekmesi grafik değil, en güncel durumu gösterir; geçmiş "Performans"
  // sekmesinde seçilen aralıkla ayrıca yüklenir (bkz. HostPerformance).
  const [latest, setLatest] = useState<MetricPoint | null>(null)
  const [containers, setContainers] = useState<DockerContainerReport[]>([])
  // Çubuk renkleri için sunucunun geçerli eşikleri (yüklenemezse çubuklar eşiksiz, yeşil kalır).
  const [thresholds, setThresholds] = useState<HostThresholdsResponse | null>(null)
  const [error, setError] = useState<string | null>(null)

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

  useEffect(reload, [id])

  const [tab, setTab] = useTab(TAB_IDS, 'genel', undefined, TAB_ALIASES)
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
        {host && (
          <HostOverview
            host={host}
            latest={latest}
            thresholds={thresholds}
            policy={agentPolicy}
            onHistory={() => setTab('performans')}
            onShowAlerts={() => setTab('alertler')}
          />
        )}
      </TabPanel>

      <TabPanel id="performans" active={tab}>
        {host && <HostPerformance host={host} disk={latest?.disk ?? []} thresholds={thresholds} />}
      </TabPanel>

      <TabPanel id="servisler" active={tab}>
        {host && <HostServices host={host} containers={containers} thresholds={thresholds} />}
      </TabPanel>

      <TabPanel id="envanter" active={tab}>
        {host && <HostInventory host={host} thresholds={thresholds} />}
      </TabPanel>

      {id && (
        <TabPanel id="alertler" active={tab} keepMounted>
          <HostAlerts hostId={id} hostTitle={host?.title} />
        </TabPanel>
      )}

      {host && (
        <TabPanel id="ayarlar" active={tab} keepMounted>
          <HostSettings host={host} onChanged={reload} onError={setError} />
        </TabPanel>
      )}
    </div>
  )
}
