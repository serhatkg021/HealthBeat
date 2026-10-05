import { Navigate, useSearchParams } from 'react-router-dom'
import { ScrollText, Settings, type LucideIcon } from 'lucide-react'
import { useAuth } from '../auth/AuthContext'
import { PageHeader } from '../components/PageHeader'
import { TabPanel, Tabs } from '../components/Tabs'
import { useTab } from '../components/useTab'
import { ALERT_RULES_PATH, navigation, notificationsPath } from '../navigation'
import { AuditPage } from './AuditPage'
import { SettingsPage } from './SettingsPage'

const ICONS: Record<string, LucideIcon> = {
  denetim: ScrollText,
  sistem: Settings,
}

// Ayarlar: kurulum geneli sayfalar (denetim kaydı, sistem ayarları) tek sayfada sekmelerle ayrılır. Her sekme yalnızca
// izni olana görünür; hiçbirini göremeyen buraya gelemez.
export function SettingsHubPage() {
  const { can } = useAuth()
  const [params] = useSearchParams()
  const tabs = navigation(can).settings
  const ids = tabs.map((t) => t.id)
  const [active, setActive] = useTab(ids, ids[0] ?? '')
  // Eski "Sistem Eşikleri" sekmesi artık Alert kuralları sayfasıdır.
  if (params.get('sekme') === 'esikler') return <Navigate to={ALERT_RULES_PATH} replace />
  // Eski Sistem Ayarları bölümleri "Sistem sahipleri" ve "Bildirim kanalları" artık Bildirim sayfasındadır.
  const bolum = params.get('bolum')
  if (bolum === 'sahipler' || bolum === 'kanallar') return <Navigate to={notificationsPath(bolum)} replace />
  if (tabs.length === 0) return <Navigate to="/" replace />
  const description = tabs.find((t) => t.id === active)?.description
  return (
    <div>
      <PageHeader title="Ayarlar" subtitle={description} />
      <Tabs items={tabs.map((t) => ({ id: t.id, label: t.label, icon: ICONS[t.id] }))} active={active} onChange={setActive} label="Ayarlar" />
      <TabPanel id="denetim" active={active}>
        <AuditPage />
      </TabPanel>
      <TabPanel id="sistem" active={active}>
        <SettingsPage />
      </TabPanel>
    </div>
  )
}
