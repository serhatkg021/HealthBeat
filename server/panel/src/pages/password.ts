// Server'ın şifre politikasının (model.ValidatePassword) istemci tarafı yansıması; böylece form
// hiçbir şey göndermeden önce bir sorunu açıklayabilir. Yetkili olan server'dır.

export const MIN_PASSWORD_LENGTH = 12
export const MAX_PASSWORD_BYTES = 72 // bcrypt yalnızca ilk 72 baytı kullanır

// İlk sorunu anlatan bir mesaj döndürür, girdi kabul edilebilirse null.
export function validateNewPassword(current: string, next: string, confirm: string): string | null {
  if (!current) return 'Mevcut şifreyi girin.'
  // UTF-16 birimleri değil karakterler: [...s] Go'nun utf8.RuneCountInString'i gibi kod noktalarını dolaşır.
  if ([...next].length < MIN_PASSWORD_LENGTH) return `Yeni şifre en az ${MIN_PASSWORD_LENGTH} karakter olmalı.`
  if (new TextEncoder().encode(next).length > MAX_PASSWORD_BYTES) {
    return `Yeni şifre en çok ${MAX_PASSWORD_BYTES} bayt olabilir.`
  }
  if (next === current) return 'Yeni şifre mevcut şifreden farklı olmalı.'
  if (next !== confirm) return 'Yeni şifre ile tekrarı eşleşmiyor.'
  return null
}
