import { Navigate, useSearchParams } from 'react-router-dom'
import { useAuth } from '../auth/AuthContext'
import { PageHeader } from '../components/PageHeader'
import { ALERT_RULES_PATH, AUDIT_PATH, navigation, notificationsPath } from '../navigation'
import { SettingsPage } from './SettingsPage'

// Ayarlar: yalnızca kurulum geneli yapılandırma (agent sürümleri, saklama, oturum, panel adresi, loglama), soldaki bölüm
// menüsüyle. Eşikler Alert kuralları'na, kanallar ve sistem sahipleri Bildirim'e, denetim kaydı Sistem Araçları'na taşındı;
// eski adresleri oralara yönlenir.
export function SettingsHubPage() {
  const { can } = useAuth()
  const [params] = useSearchParams()
  const tab = navigation(can).settings[0]
  const sekme = params.get('sekme')
  if (sekme === 'esikler') return <Navigate to={ALERT_RULES_PATH} replace />
  if (sekme === 'denetim') return <Navigate to={AUDIT_PATH} replace />
  const bolum = params.get('bolum')
  if (bolum === 'sahipler' || bolum === 'kanallar') return <Navigate to={notificationsPath(bolum)} replace />
  if (!tab) return <Navigate to="/" replace />
  return (
    <div>
      <PageHeader title="Ayarlar" subtitle={tab.description} />
      <SettingsPage />
    </div>
  )
}
