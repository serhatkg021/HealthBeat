import { Navigate } from 'react-router-dom'
import { DatabaseZap, FileSearch, ListChecks, ScrollText, type LucideIcon } from 'lucide-react'
import { useAuth } from '../auth/AuthContext'
import { PageHeader } from '../components/PageHeader'
import { TabPanel, Tabs } from '../components/Tabs'
import { useTab } from '../components/useTab'
import { navigation } from '../navigation'
import { AuditPage } from './AuditPage'
import { SystemCache } from './SystemCache'
import { SystemLogs } from './SystemLogs'
import { SystemQueue } from './SystemQueue'

const ICONS: Record<string, LucideIcon> = {
  kuyruk: ListChecks,
  cache: DatabaseZap,
  log: FileSearch,
  denetim: ScrollText,
}

// Sistem Araçları: server'ın iç durumunu ve yönetim işlemlerinin geçmişini (denetim kaydı) salt okunur gösteren
// sekmeler. Her sekme kendi izniyle görünür; hiçbirini
// göremeyen buraya gelemez.
export function SystemToolsPage() {
  const { can } = useAuth()
  const tabs = navigation(can).tools
  const ids = tabs.map((t) => t.id)
  const [active, setActive] = useTab(ids, ids[0] ?? '')
  if (tabs.length === 0) return <Navigate to="/" replace />
  return (
    <div>
      <PageHeader title="Sistem Araçları" subtitle="Server’ın iç durumu ve denetim kaydı; yalnızca görüntüleme" />
      <Tabs items={tabs.map((t) => ({ ...t, icon: ICONS[t.id] }))} active={active} onChange={setActive} label="Sistem araçları" />
      <TabPanel id="kuyruk" active={active}>
        <SystemQueue />
      </TabPanel>
      <TabPanel id="cache" active={active}>
        <SystemCache />
      </TabPanel>
      <TabPanel id="log" active={active}>
        <SystemLogs />
      </TabPanel>
      <TabPanel id="denetim" active={active}>
        <AuditPage />
      </TabPanel>
    </div>
  )
}
