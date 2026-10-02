import { Navigate } from 'react-router-dom'
import { ScrollText, Settings, SlidersHorizontal, type LucideIcon } from 'lucide-react'
import { useAuth } from '../auth/AuthContext'
import { PageHeader } from '../components/PageHeader'
import { TabPanel, Tabs } from '../components/Tabs'
import { useTab } from '../components/useTab'
import { navigation } from '../navigation'
import { AuditPage } from './AuditPage'
import { SettingsPage } from './SettingsPage'
import { ThresholdsPage } from './ThresholdsPage'

const ICONS: Record<string, LucideIcon> = {
  esikler: SlidersHorizontal,
  denetim: ScrollText,
  sistem: Settings,
}

// Ayarlar: kurulum geneli sayfalar (eşikler, denetim kaydı, sistem ayarları) tek sayfada sekmelerle ayrılır. Her sekme
// yalnızca izni olana görünür; hiçbirini göremeyen buraya gelemez.
export function SettingsHubPage() {
  const { can } = useAuth()
  const tabs = navigation(can).settings
  const ids = tabs.map((t) => t.id)
  const [active, setActive] = useTab(ids, ids[0] ?? '')
  if (tabs.length === 0) return <Navigate to="/" replace />
  const description = tabs.find((t) => t.id === active)?.description
  return (
    <div>
      <PageHeader title="Ayarlar" subtitle={description} />
      <Tabs items={tabs.map((t) => ({ id: t.id, label: t.label, icon: ICONS[t.id] }))} active={active} onChange={setActive} label="Ayarlar" />
      <TabPanel id="esikler" active={active}>
        <ThresholdsPage />
      </TabPanel>
      <TabPanel id="denetim" active={active}>
        <AuditPage />
      </TabPanel>
      <TabPanel id="sistem" active={active}>
        <SettingsPage />
      </TabPanel>
    </div>
  )
}
