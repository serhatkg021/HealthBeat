import { createContext, useCallback, useContext, useEffect, useState, type ReactNode } from 'react'
import { authApi, meApi } from '../api/endpoints'
import { clearTokens, revokeSession, setTokens } from '../api/client'
import type { User } from '../types/api'
import { hasPermission, type Permission } from './permissions'

interface AuthContextValue {
  user: User | null
  loading: boolean
  error: string | null
  login: (email: string, password: string) => Promise<void>
  changePassword: (currentPassword: string, newPassword: string) => Promise<void>
  logout: () => void
  // can, oturumdaki kullanıcının izni olup olmadığıdır (bkz. permissions.ts).
  can: (permission: Permission) => boolean
}

const AuthContext = createContext<AuthContextValue | undefined>(undefined)

const USER_KEY = 'healthbeat_user'

function loadStoredUser(): User | null {
  const raw = localStorage.getItem(USER_KEY)
  if (!raw) return null
  try {
    return JSON.parse(raw) as User
  } catch {
    return null
  }
}

function storeUser(u: User): void {
  localStorage.setItem(USER_KEY, JSON.stringify(u))
}

// withCurrentProfile, kullanıcıyı GET /me ile tamamlar: rolün izinleri yalnızca orada gelir ve rol ya da izinler oturum
// açıkken değişmiş olabilir. /me alınamazsa elimizdeki kullanıcıyla devam edilir (izinsiz: yalnızca her rolün gördükleri).
async function withCurrentProfile(fallback: User): Promise<User> {
  try {
    return await meApi.get()
  } catch {
    return fallback
  }
}

export function AuthProvider({ children }: { children: ReactNode }) {
  const [user, setUser] = useState<User | null>(loadStoredUser)
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState<string | null>(null)

  // Sayfa açılışında saklı kullanıcı tazelenir: bir yönetici rolü değiştirdiyse ya da izin tablosu değiştiyse arayüz
  // yeniden girişi beklemeden (sayfa yenilenince) güncellenir.
  useEffect(() => {
    if (!loadStoredUser()) return
    meApi
      .get()
      .then((me) => {
        if (!loadStoredUser()) return // bu arada çıkış yapıldı
        storeUser(me)
        setUser(me)
      })
      .catch(() => undefined) // 401 ise istemci oturumu zaten kapatır; diğer hatalarda saklı kullanıcıyla devam edilir
  }, [])

  const login = useCallback(async (email: string, password: string) => {
    setLoading(true)
    setError(null)
    try {
      const resp = await authApi.login(email, password)
      setTokens(resp.access_token, resp.refresh_token)
      const me = await withCurrentProfile(resp.user)
      storeUser(me)
      setUser(me)
    } catch (err) {
      setError(err instanceof Error ? err.message : 'giriş yapılamadı')
      throw err
    } finally {
      setLoading(false)
    }
  }, [])

  const changePassword = useCallback(async (currentPassword: string, newPassword: string) => {
    const resp = await authApi.changePassword(currentPassword, newPassword)
    setTokens(resp.access_token, resp.refresh_token) // server daha eski her oturumu az önce sonlandırdı
    const me = await withCurrentProfile(resp.user)
    storeUser(me)
    setUser(me)
  }, [])

  const logout = useCallback(() => {
    void revokeSession() // refresh token'ı, aşağıda temizlenmeden önce eşzamanlı olarak okur
    clearTokens()
    localStorage.removeItem(USER_KEY)
    setUser(null)
  }, [])

  const can = useCallback((permission: Permission) => hasPermission(user, permission), [user])

  return (
    <AuthContext.Provider value={{ user, loading, error, login, changePassword, logout, can }}>{children}</AuthContext.Provider>
  )
}

export function useAuth(): AuthContextValue {
  const ctx = useContext(AuthContext)
  if (!ctx) throw new Error('useAuth, AuthProvider içinde kullanılmalı')
  return ctx
}
