// Profil sayfasının saf mantığı (React yok; Node'un çalıştırıcısıyla birim test edilir). Kişi yalnızca görünen adını ve
// telefonunu değiştirir (PATCH /api/v1/me); kurallar server'dakilerle aynıdır.
import type { User } from '../types/api.ts'

export const MAX_FULL_NAME = 200
export const MAX_PHONE = 32

export interface ProfilePatch {
  full_name?: string
  phone?: string
}

// Geçersizse kullanıcıya gösterilecek ileti, geçerliyse null.
export function validateProfile(fullName: string, phone: string): string | null {
  if (fullName.trim().length > MAX_FULL_NAME) return 'Ad çok uzun.'
  const p = phone.trim()
  if (p.length > MAX_PHONE) return 'Telefon çok uzun.'
  if (!/^[0-9 +\-().]*$/.test(p)) return 'Telefon yalnızca rakam, boşluk ve + - ( ) . içerebilir.'
  return null
}

// Yalnızca değişen alanları gönderir (boş metin alanı temizler); hiçbir şey değişmediyse null.
export function profilePatch(user: Pick<User, 'full_name' | 'phone'>, fullName: string, phone: string): ProfilePatch | null {
  const patch: ProfilePatch = {}
  if (fullName.trim() !== (user.full_name ?? '')) patch.full_name = fullName.trim()
  if (phone.trim() !== (user.phone ?? '')) patch.phone = phone.trim()
  return Object.keys(patch).length > 0 ? patch : null
}
