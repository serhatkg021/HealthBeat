// İnce bir fetch sarmalayıcısı: bearer access token'ı ekler, 401'de bir kez saydam biçimde
// yeniler ve hataları normalleştirir. Token'lar localStorage'da yaşar — dahili bir yönetim
// paneli için kabul edilebilir, ama bilinen bir XSS maruziyeti takasıdır: bir XSS açığı
// token'ları okuyabilir (httpOnly çereze geçiş, CSRF korumasıyla birlikte ileride ele alınabilir).

import { apiUrl } from '../config.ts'

const ACCESS_TOKEN_KEY = 'healthbeat_access_token'
const REFRESH_TOKEN_KEY = 'healthbeat_refresh_token'
const USER_KEY = 'healthbeat_user'

export function getAccessToken(): string | null {
  return localStorage.getItem(ACCESS_TOKEN_KEY)
}

function getRefreshToken(): string | null {
  return localStorage.getItem(REFRESH_TOKEN_KEY)
}

export function setTokens(access: string, refresh?: string): void {
  localStorage.setItem(ACCESS_TOKEN_KEY, access)
  if (refresh) localStorage.setItem(REFRESH_TOKEN_KEY, refresh)
}

// Yerel oturumu bitirir. Önbellekteki profil de gider: geride bırakmak login sayfasının
// kullanıcının hâlâ giriş yapmış olduğunu sanıp onu doğrudan geri göndermesine yol açar.
export function clearTokens(): void {
  localStorage.removeItem(ACCESS_TOKEN_KEY)
  localStorage.removeItem(REFRESH_TOKEN_KEY)
  localStorage.removeItem(USER_KEY)
}

// En iyi çaba server tarafı çıkışı: saklı refresh token'ın ait olduğu oturumu iptal eder.
// Bunun başarısına bakılmaksızın yerel token'lar temizlenir.
export async function revokeSession(): Promise<void> {
  const refreshToken = getRefreshToken()
  if (!refreshToken) return
  try {
    await fetch(apiUrl('/api/v1/auth/logout'), {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ refresh_token: refreshToken }),
    })
  } catch {
    // Çevrimdışı ya da server kapalı: token yine de kendiliğinden süresi dolar.
  }
}

export class ApiError extends Error {
  status: number
  // Sunucunun makine tarafından okunabilir hata kodu (varsa), ör. "reset_link_invalid"; mesaj metnine bağlı kalmadan
  // karar vermek içindir.
  code?: string
  // İsteğin sunucu logundaki kimliği (X-Request-ID). Sunucu hatalarında (5xx) mesajın sonuna eklenir: kullanıcı onu
  // yöneticiye iletir, yönetici logda bu kimlikle hatanın ayrıntısını bulur. 4xx'te kullanıcının düzeltebileceği bir
  // sorun olduğu için gösterilmez.
  requestId?: string
  constructor(status: number, message: string, code?: string, requestId?: string) {
    super(status >= 500 && requestId ? `${message} (hata kimliği: ${requestId})` : message)
    this.status = status
    this.code = code
    this.requestId = requestId
  }
}

// Eşzamanlı 401'ler tek bir yenileme çağrısı tetikler, başarısız istek başına bir tane değil.
let refreshPromise: Promise<boolean> | null = null

async function refreshAccessToken(): Promise<boolean> {
  const refreshToken = getRefreshToken()
  if (!refreshToken) return false

  if (!refreshPromise) {
    refreshPromise = (async () => {
      try {
        const resp = await fetch(apiUrl('/api/v1/auth/refresh'), {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ refresh_token: refreshToken }),
        })
        if (!resp.ok) return false
        const data = await resp.json()
        // Server refresh token'ları döndürür: az önce kullandığımız artık harcanmıştır; bu yüzden
        // yenisi onun yerine geçmeli, yoksa sonraki yenileme başarısız olur.
        setTokens(data.access_token, data.refresh_token)
        return true
      } catch {
        return false
      } finally {
        refreshPromise = null
      }
    })()
  }
  return refreshPromise
}

interface RequestOptions {
  method?: string
  body?: unknown
  query?: Record<string, string | undefined>
}

interface RawResult {
  data: unknown
  headers: Headers
}

async function rawRequest(path: string, opts: RequestOptions, retry: boolean): Promise<RawResult> {
  const url = new URL(apiUrl(path), window.location.origin)
  if (opts.query) {
    for (const [key, value] of Object.entries(opts.query)) {
      if (value !== undefined) url.searchParams.set(key, value)
    }
  }

  const headers: Record<string, string> = {}
  const token = getAccessToken()
  if (token) headers.Authorization = `Bearer ${token}`
  if (opts.body !== undefined) headers['Content-Type'] = 'application/json'

  const resp = await fetch(url.href, {
    method: opts.method ?? 'GET',
    headers,
    body: opts.body !== undefined ? JSON.stringify(opts.body) : undefined,
  })

  // Auth endpoint'lerinde 401, süresi dolmuş oturum değil hatalı kimlik bilgisi demektir.
  // Token'sız önbellekteki profil, daha önceki bir oturum sonundan kalandır: onu da temizle.
  const isAuthEndpoint = path.startsWith('/api/v1/auth/')
  if (resp.status === 401 && retry && !isAuthEndpoint && (token || localStorage.getItem(USER_KEY))) {
    if (token && (await refreshAccessToken())) {
      return rawRequest(path, opts, false)
    }
    clearTokens()
    if (window.location.pathname !== '/login') {
      window.location.assign('/login')
    }
    throw new ApiError(401, 'oturum süresi doldu')
  }

  if (resp.status === 204) {
    return { data: undefined, headers: resp.headers }
  }

  const contentType = resp.headers.get('content-type') ?? ''
  const data = contentType.includes('application/json') ? await resp.json() : undefined

  if (resp.status === 403 && data && typeof data === 'object' && (data as { code?: unknown }).code === 'password_change_required') {
    // Saklı profil bayat (bayrak girişten sonra ayarlandı): düzelt ve forma git.
    try {
      const raw = localStorage.getItem('healthbeat_user')
      if (raw) localStorage.setItem('healthbeat_user', JSON.stringify({ ...JSON.parse(raw), must_change_password: true }))
    } catch {
      // localStorage kullanılamıyor: aşağıdaki yönlendirme yine çalışır.
    }
    if (window.location.pathname !== '/change-password') window.location.assign('/change-password')
  }

  if (!resp.ok) {
    const message =
      data && typeof data === 'object' && 'error' in data ? String((data as { error: unknown }).error) : resp.statusText
    const code = data && typeof data === 'object' && typeof (data as { code?: unknown }).code === 'string' ? (data as { code: string }).code : undefined
    const requestId =
      data && typeof data === 'object' && typeof (data as { request_id?: unknown }).request_id === 'string'
        ? (data as { request_id: string }).request_id
        : (resp.headers.get('X-Request-ID') ?? undefined)
    throw new ApiError(resp.status, message, code, requestId)
  }

  return { data, headers: resp.headers }
}

export async function apiRequest<T>(path: string, opts: RequestOptions = {}): Promise<T> {
  const { data } = await rawRequest(path, opts, true)
  return data as T
}

export interface Page<T> {
  items: T[]
  // Süzgece uyan toplam satır sayısı; X-Total-Count başlığından. Sunucu bir sebeple
  // göndermezse (beklenmez) items.length'e düşer.
  total: number
}

// apiRequestPage, gövdesi her zaman düz bir dizi kalan sayfalanabilir uçlar (?q=&limit=&offset=
// alan) içindir — bkz. server/internal/httpapi/respond.go: parseListParams. Toplam sayı gövde
// şeklini değiştirmeden X-Total-Count yanıt başlığından okunur.
export async function apiRequestPage<T>(path: string, opts: RequestOptions = {}): Promise<Page<T>> {
  const { data, headers } = await rawRequest(path, opts, true)
  const items = (data as T[]) ?? []
  const rawTotal = headers.get('X-Total-Count')
  const total = rawTotal !== null ? Number(rawTotal) : items.length
  return { items, total: Number.isFinite(total) ? total : items.length }
}
