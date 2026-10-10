// Saat dilimi seçenekleri (React yok; birim test edilir): tarayıcının bildiği IANA adları, o anki UTC ofsetleriyle.
// Ofset server'ın biçimiyle yazılır ("UTC+3", "UTC+5:30", "UTC−4", "UTC").

export interface TimezoneOption {
  value: string
  label: string
  offsetMinutes: number
}

// formatOffset, dakika cinsinden ofseti yazar (server'daki tz.FormatOffset ile aynı).
export function formatOffset(minutes: number): string {
  if (minutes === 0) return 'UTC'
  const sign = minutes < 0 ? '−' : '+'
  const m = Math.abs(minutes)
  const h = Math.floor(m / 60)
  const rest = m % 60
  return rest === 0 ? `UTC${sign}${h}` : `UTC${sign}${h}:${String(rest).padStart(2, '0')}`
}

// offsetMinutes, at anında zone'un UTC ofsetidir (dakika). Tarayıcı zone'u tanımıyorsa null.
export function offsetMinutes(zone: string, at: Date): number | null {
  try {
    const part = new Intl.DateTimeFormat('en-US', { timeZone: zone, timeZoneName: 'longOffset' })
      .formatToParts(at)
      .find((p) => p.type === 'timeZoneName')?.value
    if (!part || part === 'GMT') return 0
    const m = /^GMT([+-])(\d{2}):(\d{2})$/.exec(part)
    if (!m) return null
    const v = Number(m[2]) * 60 + Number(m[3])
    return m[1] === '-' ? -v : v
  } catch {
    return null
  }
}

// timezoneOptions, seçilebilecek saat dilimleridir: ofsete, sonra ada göre sıralı; etiket "Europe/Istanbul (UTC+3)".
// zones verilmezse tarayıcının listesi kullanılır (UTC her zaman vardır).
export function timezoneOptions(at: Date, zones?: string[]): TimezoneOption[] {
  const names = zones ?? (typeof Intl.supportedValuesOf === 'function' ? Intl.supportedValuesOf('timeZone') : [])
  const all = [...new Set(['UTC', ...names])]
  const out: TimezoneOption[] = []
  for (const value of all) {
    const offset = offsetMinutes(value, at)
    if (offset === null) continue
    out.push({ value, label: `${value} (${formatOffset(offset)})`, offsetMinutes: offset })
  }
  return out.sort((a, b) => a.offsetMinutes - b.offsetMinutes || a.value.localeCompare(b.value))
}
