import { createContext, useCallback, useContext, useState, type ReactNode } from 'react'
import { authApi } from '../api/endpoints'
import { clearTokens, revokeSession, setTokens } from '../api/client'
import type { User } from '../types/api'

interface AuthContextValue {
  user: User | null
  loading: boolean
  error: string | null
  login: (email: string, password: string) => Promise<void>
  changePassword: (currentPassword: string, newPassword: string) => Promise<void>
  logout: () => void
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

export function AuthProvider({ children }: { children: ReactNode }) {
  const [user, setUser] = useState<User | null>(loadStoredUser)
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState<string | null>(null)

  const login = useCallback(async (email: string, password: string) => {
    setLoading(true)
    setError(null)
    try {
      const resp = await authApi.login(email, password)
      setTokens(resp.access_token, resp.refresh_token)
      localStorage.setItem(USER_KEY, JSON.stringify(resp.user))
      setUser(resp.user)
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
    localStorage.setItem(USER_KEY, JSON.stringify(resp.user))
    setUser(resp.user)
  }, [])

  const logout = useCallback(() => {
    void revokeSession() // refresh token'ı, aşağıda temizlenmeden önce eşzamanlı olarak okur
    clearTokens()
    localStorage.removeItem(USER_KEY)
    setUser(null)
  }, [])

  return <AuthContext.Provider value={{ user, loading, error, login, changePassword, logout }}>{children}</AuthContext.Provider>
}

export function useAuth(): AuthContextValue {
  const ctx = useContext(AuthContext)
  if (!ctx) throw new Error('useAuth, AuthProvider içinde kullanılmalı')
  return ctx
}
