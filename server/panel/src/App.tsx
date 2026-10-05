import type { ReactElement } from 'react'
import { Navigate, Route, BrowserRouter, Routes, useLocation } from 'react-router-dom'
import { AuthProvider, useAuth } from './auth/AuthContext'
import type { Permission } from './auth/permissions'
import { Layout } from './components/Layout'
import { ALERT_RULES_PATH, AUDIT_PATH, HOSTS_PATH, LEGACY_SETTINGS_ROUTES, MAINTENANCE_PATH, NOTIFICATIONS_PATH, SETTINGS_PATH } from './navigation'
import { LoginPage } from './pages/LoginPage'
import { DashboardPage } from './pages/DashboardPage'
import { OrganizationsPage } from './pages/OrganizationsPage'
import { OrganizationHostsPage } from './pages/OrganizationHostsPage'
import { MyHostsPage } from './pages/MyHostsPage'
import { HostDetailPage } from './pages/HostDetailPage'
import { AlertsPage } from './pages/AlertsPage'
import { UsersPage } from './pages/UsersPage'
import { SettingsHubPage } from './pages/SettingsHubPage'
import { SystemToolsPage } from './pages/SystemToolsPage'
import { PendingPage } from './pages/PendingPage'
import { AlertRulesPage } from './pages/AlertRulesPage'
import { NotificationsPage } from './pages/NotificationsPage'
import { ProfilePage } from './pages/ProfilePage'
import { ChangePasswordPage } from './pages/ChangePasswordPage'
import { ForgotPasswordPage } from './pages/ForgotPasswordPage'
import { ResetPasswordPage } from './pages/ResetPasswordPage'

function RequireAuth({ children }: { children: ReactElement }) {
  const { user } = useAuth()
  const location = useLocation()
  if (!user) return <Navigate to="/login" replace />
  // Yeni bir şifre seçmesi gereken bir hesap (ör. ilk yönetici) başka hiçbir şeye erişemez —
  // API de onu reddeder, bu yalnızca hata göstermemeyi sağlar.
  if (user.must_change_password && location.pathname !== '/change-password') {
    return <Navigate to="/change-password" replace />
  }
  return children
}

// permission bir listeyse içlerinden biri yeterlidir.
function RequirePermission({ permission, children }: { permission: Permission | Permission[]; children: ReactElement }) {
  const { user, can } = useAuth()
  const any = Array.isArray(permission) ? permission : [permission]
  if (!user || !any.some(can)) return <Navigate to="/" replace />
  return children
}

// Eski ayrı sayfa adresleri (ör. /settings/system?bolum=kanallar) Ayarlar'ın ilgili sekmesine yönlenir; adresteki diğer
// parametreler (bölüm) korunur.
function LegacySettingsRedirect({ tab }: { tab: string }) {
  const params = new URLSearchParams(useLocation().search)
  params.set('sekme', tab)
  return <Navigate to={`${SETTINGS_PATH}?${params}`} replace />
}

export default function App() {
  return (
    <AuthProvider>
      <BrowserRouter>
        <Routes>
          <Route path="/login" element={<LoginPage />} />
          <Route path="/forgot-password" element={<ForgotPasswordPage />} />
          <Route path="/reset-password" element={<ResetPasswordPage />} />
          <Route
            path="/change-password"
            element={
              <RequireAuth>
                <ChangePasswordPage />
              </RequireAuth>
            }
          />
          <Route
            element={
              <RequireAuth>
                <Layout />
              </RequireAuth>
            }
          >
            <Route path="/" element={<DashboardPage />} />
            <Route
              path="/organizations"
              element={
                <RequirePermission permission="organization.view">
                  <OrganizationsPage />
                </RequirePermission>
              }
            />
            <Route
              path="/organizations/:id"
              element={
                <RequirePermission permission="organization.view">
                  <OrganizationHostsPage />
                </RequirePermission>
              }
            />
            <Route
              path={HOSTS_PATH}
              element={
                <RequirePermission permission="host.view">
                  <PendingPage
                    title="Sunucular"
                    subtitle="Tüm sunucuların süzülebilir listesi"
                    items={['Özet’teki sunucu tablosu ve süzgeçleri', 'Operatörün “Sunucularım” sayfası', 'Sunucu ekleme sihirbazı']}
                  />
                </RequirePermission>
              }
            />
            <Route path="/my-hosts" element={<MyHostsPage />} />
            <Route path="/hosts/:id" element={<HostDetailPage />} />
            <Route path="/alerts" element={<AlertsPage />} />
            <Route
              path={ALERT_RULES_PATH}
              element={
                <RequirePermission permission="threshold.view">
                  <AlertRulesPage />
                </RequirePermission>
              }
            />
            {/* Eski Eşikler sayfası (yer imleri) artık Alert kurallarının sistem kapsamıdır. */}
            <Route path="/thresholds" element={<Navigate to={ALERT_RULES_PATH} replace />} />
            <Route path="/audit" element={<Navigate to={AUDIT_PATH} replace />} />
            <Route
              path={MAINTENANCE_PATH}
              element={
                <PendingPage
                  title="Bakım pencereleri"
                  subtitle="Planlı bakım sırasında bildirimleri susturma"
                  items={['Yakında: bakım penceresi listesi ve formu (örnek veriyle)']}
                />
              }
            />
            <Route
              path={NOTIFICATIONS_PATH}
              element={
                <RequirePermission permission={['notification.view', 'settings.view', 'contact.view']}>
                  <NotificationsPage />
                </RequirePermission>
              }
            />
            <Route
              path="/users"
              element={
                <RequirePermission permission="user.view">
                  <UsersPage />
                </RequirePermission>
              }
            />
            <Route path="/profile" element={<ProfilePage />} />
            <Route path="/settings" element={<SettingsHubPage />} />
            {Object.entries(LEGACY_SETTINGS_ROUTES).map(([path, tab]) => (
              <Route key={path} path={path} element={<LegacySettingsRedirect tab={tab} />} />
            ))}
            <Route path="/system" element={<SystemToolsPage />} />
          </Route>
          <Route path="*" element={<Navigate to="/" replace />} />
        </Routes>
      </BrowserRouter>
    </AuthProvider>
  )
}
