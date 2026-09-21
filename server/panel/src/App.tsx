import type { ReactElement } from 'react'
import { Navigate, Route, BrowserRouter, Routes, useLocation } from 'react-router-dom'
import { AuthProvider, useAuth } from './auth/AuthContext'
import { Layout } from './components/Layout'
import { LoginPage } from './pages/LoginPage'
import { DashboardPage } from './pages/DashboardPage'
import { OrganizationsPage } from './pages/OrganizationsPage'
import { OrganizationHostsPage } from './pages/OrganizationHostsPage'
import { MyHostsPage } from './pages/MyHostsPage'
import { HostDetailPage } from './pages/HostDetailPage'
import { AlertsPage } from './pages/AlertsPage'
import { ThresholdsPage } from './pages/ThresholdsPage'
import { UsersPage } from './pages/UsersPage'
import { AuditPage } from './pages/AuditPage'
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

function RequireRole({ roles, children }: { roles: string[]; children: ReactElement }) {
  const { user } = useAuth()
  if (!user || !roles.includes(user.role)) return <Navigate to="/" replace />
  return children
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
                <RequireRole roles={['super_admin', 'org_admin']}>
                  <OrganizationsPage />
                </RequireRole>
              }
            />
            <Route
              path="/organizations/:id"
              element={
                <RequireRole roles={['super_admin', 'org_admin']}>
                  <OrganizationHostsPage />
                </RequireRole>
              }
            />
            <Route path="/my-hosts" element={<MyHostsPage />} />
            <Route path="/hosts/:id" element={<HostDetailPage />} />
            <Route path="/alerts" element={<AlertsPage />} />
            <Route path="/thresholds" element={<ThresholdsPage />} />
            <Route
              path="/users"
              element={
                <RequireRole roles={['super_admin']}>
                  <UsersPage />
                </RequireRole>
              }
            />
            <Route
              path="/audit"
              element={
                <RequireRole roles={['super_admin']}>
                  <AuditPage />
                </RequireRole>
              }
            />
          </Route>
          <Route path="*" element={<Navigate to="/" replace />} />
        </Routes>
      </BrowserRouter>
    </AuthProvider>
  )
}
