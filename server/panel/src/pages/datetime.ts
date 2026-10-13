// Panelin zaman gösterimi: her zaman GG.AA.YYYY SS:DD(:SS) biçiminde ve kurulumun saat diliminde (Ayarlar → Saat dilimi;
// /meta'dan gelir). Tarayıcının yerel ayarı ve saat dilimi kullanılmaz: aynı alert'in saati her kullanıcıda, bakım
// pencerelerinde ve e-postalarda aynı görünür. Bakım ekranlarının "YYYY.AA.GG" biçimi ayrıdır (maintenance.ts).
// React içermez; Node'un çalıştırıcısıyla birim test edilir.

export const EMPTY = '—'

// Gösterim dilimi: IANA adı ya da (ad tanınmazsa) sabit ofset. Meta gelene kadar tarayıcının dilimi kullanılır.
type Zone = { name: string } | { offsetSeconds: number } | null

let zone: Zone = null
const formatters = new Map<string, Intl.DateTimeFormat>()

function formatter(timeZone: string | undefined): Intl.DateTimeFormat {
  const key = timeZone ?? ''
  let f = formatters.get(key)
  if (!f) {
    f = new Intl.DateTimeFormat('tr-TR', {
      timeZone,
      year: 'numeric',
      month: '2-digit',
      day: '2-digit',
      hour: '2-digit',
      minute: '2-digit',
      second: '2-digit',
      hourCycle: 'h23',
    })
    formatters.set(key, f)
  }
  return f
}

function validZone(name: string): boolean {
  try {
    formatter(name)
    return true
  } catch {
    return false
  }
}

// setDisplayTimeZone, gösterim dilimini ayarlar (meta yüklenince). Ad tanınmazsa (ör. "Local") ofset kullanılır; ikisi
// de yoksa tarayıcının dilimine dönülür.
export function setDisplayTimeZone(name?: string, offsetSeconds?: number): void {
  if (name && validZone(name)) zone = { name }
  else if (offsetSeconds !== undefined) zone = { offsetSeconds }
  else zone = null
}

interface Parts {
  day: string
  month: string
  year: string
  hour: string
  minute: string
  second: string
}

function parts(value: string | number | Date): Parts | null {
  const date = value instanceof Date ? value : new Date(value)
  if (Number.isNaN(date.getTime())) return null
  let f: Intl.DateTimeFormat
  let at = date
  if (zone && 'name' in zone) {
    f = formatter(zone.name)
  } else if (zone) {
    f = formatter('UTC')
    at = new Date(date.getTime() + zone.offsetSeconds * 1000)
  } else {
    f = formatter(undefined)
  }
  const out: Record<string, string> = {}
  for (const p of f.formatToParts(at)) out[p.type] = p.value
  return { day: out.day, month: out.month, year: out.year, hour: out.hour, minute: out.minute, second: out.second }
}

type Input = string | number | Date | null | undefined

// formatDateTime, "11.10.2026 22:51:41" (seconds: false ise "11.10.2026 22:51"); boş ya da geçersizse "—".
export function formatDateTime(value: Input, { seconds = true }: { seconds?: boolean } = {}): string {
  const p = value == null || value === '' ? null : parts(value)
  if (!p) return EMPTY
  return `${p.day}.${p.month}.${p.year} ${p.hour}:${p.minute}${seconds ? `:${p.second}` : ''}`
}

// formatDate, "11.10.2026"; boş ya da geçersizse "—".
export function formatDate(value: Input): string {
  const p = value == null || value === '' ? null : parts(value)
  return p ? `${p.day}.${p.month}.${p.year}` : EMPTY
}

// formatTime, "22:51:41" (seconds: false ise "22:51"); boş ya da geçersizse "—".
export function formatTime(value: Input, { seconds = true }: { seconds?: boolean } = {}): string {
  const p = value == null || value === '' ? null : parts(value)
  if (!p) return EMPTY
  return `${p.hour}:${p.minute}${seconds ? `:${p.second}` : ''}`
}

// inputValue, <input type="datetime-local"> değeri ("2026-10-11T22:51") — gösterimle aynı dilimde.
export function inputValue(value: string | number | Date): string {
  const p = parts(value)
  return p ? `${p.year}-${p.month}-${p.day}T${p.hour}:${p.minute}` : ''
}

// offsetAt, gösterim diliminin verilen andaki UTC ofsetidir (ms; yaz saati değişebilir).
function offsetAt(ms: number): number {
  if (zone && 'offsetSeconds' in zone) return zone.offsetSeconds * 1000
  if (!zone) return -new Date(ms).getTimezoneOffset() * 60_000
  const p = parts(ms)!
  return Date.UTC(+p.year, +p.month - 1, +p.day, +p.hour, +p.minute, +p.second) - Math.floor(ms / 1000) * 1000
}

// parseInputValue, bir datetime-local değerini ("2026-10-11T22:51") gösterim diliminde yorumlar; geçersizse null.
export function parseInputValue(value: string): Date | null {
  const m = /^(\d{4})-(\d{2})-(\d{2})T(\d{2}):(\d{2})$/.exec(value)
  if (!m) return null
  const naive = Date.UTC(+m[1], +m[2] - 1, +m[3], +m[4], +m[5])
  const check = new Date(naive) // 25:99, 31 Şubat gibi taşan değerler reddedilir
  if (check.getUTCMonth() !== +m[2] - 1 || check.getUTCDate() !== +m[3] || check.getUTCHours() !== +m[4] || check.getUTCMinutes() !== +m[5]) {
    return null
  }
  let at = naive - offsetAt(naive)
  const again = naive - offsetAt(at) // ofset geçişinin hemen yanında bir kez düzelt
  if (again !== at) at = again
  return new Date(at)
}
