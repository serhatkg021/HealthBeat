// Tarayıcı sekmesi başlığı: her sayfa kendi adını taşır ("Alert'ler · HealthBeat"); böylece çok sekme açıkken
// ve geçmişte sayfalar ayırt edilir. Detay sayfaları (sunucu, organizasyon) adı veriden gelir, bu yüzden
// burada null döner ve sayfanın kendisi başlığı verir.
export const APP_NAME = 'HealthBeat'

const TITLES: Record<string, string> = {
  '/': 'Özet',
  '/alerts': 'Alert’ler',
  '/organizations': 'Organizasyonlar',
  '/thresholds': 'Eşikler',
  '/users': 'Kullanıcılar',
  '/audit': 'Denetim Kaydı',
  '/my-hosts': 'Sunucularım',
  '/change-password': 'Şifre değiştir',
  '/login': 'Giriş',
  '/forgot-password': 'Şifremi unuttum',
  '/reset-password': 'Yeni şifre',
}

export function pageTitle(pathname: string): string | null {
  const clean = pathname.length > 1 ? pathname.replace(/\/+$/, '') : pathname
  return TITLES[clean] ?? null
}

// "Başlık · HealthBeat"; başlık yoksa yalnızca uygulama adı.
export function fullTitle(title?: string | null): string {
  const t = title?.trim()
  return t ? `${t} · ${APP_NAME}` : APP_NAME
}
