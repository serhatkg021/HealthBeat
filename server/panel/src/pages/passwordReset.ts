// E-posta ile şifre sıfırlamanın istemci tarafı saf mantığı: bağlantıdaki token'ın okunması, e-posta biçim
// denetimi ve yeni şifre doğrulaması. Yetkili olan server'dır; buradaki denetimler yalnızca gereksiz istek
// göndermemek ve sorunu açıklamak içindir.
import { MAX_PASSWORD_BYTES, MIN_PASSWORD_LENGTH } from './password.ts'

// Server'ın ürettiği token: 32 rastgele bayt, base64url (43 karakter). Sınırlar biçim kontrolü içindir.
const TOKEN_RE = /^[A-Za-z0-9_-]{20,200}$/

// Sıfırlama bağlantısı token'ı adresin #parçasında taşır ("/reset-password#token=…"): tarayıcı parçayı
// server'a, proxy günlüklerine ya da Referer başlığına göndermez. Geçerli biçimde değilse '' döner.
export function tokenFromHash(hash: string): string {
  const token = new URLSearchParams(hash.replace(/^#/, '')).get('token') ?? ''
  return TOKEN_RE.test(token) ? token : ''
}

// Server'ın plausibleEmail'iyle aynı kural: yalnızca biçim ("x@y"); gerçek doğrulama e-postanın ulaşmasıdır.
export function plausibleEmail(raw: string): boolean {
  const s = raw.trim().toLowerCase()
  if (s === '' || s.length > 254 || /[\s<>]/.test(s)) return false
  const at = s.lastIndexOf('@')
  return at > 0 && at < s.length - 1
}

// Sıfırlamada mevcut şifre yoktur; ilk sorunu anlatan mesajı döndürür, girdi kabul edilebilirse null.
export function validateResetPassword(next: string, confirm: string): string | null {
  if ([...next].length < MIN_PASSWORD_LENGTH) return `Yeni şifre en az ${MIN_PASSWORD_LENGTH} karakter olmalı.`
  if (new TextEncoder().encode(next).length > MAX_PASSWORD_BYTES) return `Yeni şifre en çok ${MAX_PASSWORD_BYTES} bayt olabilir.`
  if (next !== confirm) return 'Yeni şifre ile tekrarı eşleşmiyor.'
  return null
}
