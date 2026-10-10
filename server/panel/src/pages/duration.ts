// Alert kurallarındaki süre koşulu: eşik ya da durum bu kadar süre kesintisiz sürünce alert açılır (boş = hemen). Server
// saniye saklar (1 sn – 30 gün); panelde sayı ve birim olarak girilir. React yok: Node'un çalıştırıcısıyla birim test edilir.

export type DurationUnit = 'sn' | 'dk' | 'sa'

export const DURATION_UNITS: { unit: DurationUnit; label: string; seconds: number }[] = [
  { unit: 'sn', label: 'saniye', seconds: 1 },
  { unit: 'dk', label: 'dakika', seconds: 60 },
  { unit: 'sa', label: 'saat', seconds: 3600 },
]

// Server'ın model.MaxRuleDurationSeconds'ı.
export const MAX_DURATION_SECONDS = 30 * 24 * 3600

// Girilen değer metin olarak tutulur; böylece yarım yazılmış bir sayı imlecin altında yeniden yazılmaz.
export interface DurationDraft {
  value: string
  unit: DurationUnit
}

export const emptyDuration = (): DurationDraft => ({ value: '', unit: 'dk' })

// Saklanan süreyi tam bölünen en büyük birimle gösterir: 600 -> 10 dk, 90 -> 90 sn.
export function durationDraft(seconds?: number | null): DurationDraft {
  if (!seconds) return emptyDuration()
  const unit = [...DURATION_UNITS].reverse().find((u) => seconds % u.seconds === 0) ?? DURATION_UNITS[0]
  return { value: String(seconds / unit.seconds), unit: unit.unit }
}

// Boş = hemen (undefined). Ondalık virgülle de yazılabilir (1,5 dk = 90 sn).
export function parseDuration(d: DurationDraft): { seconds: number | undefined } | { error: string } {
  const text = d.value.trim().replace(',', '.')
  if (text === '') return { seconds: undefined }
  const n = Number(text)
  if (!Number.isFinite(n) || n <= 0) return { error: 'Süre pozitif bir sayı olmalı (hemen için boş bırakın).' }
  const factor = DURATION_UNITS.find((u) => u.unit === d.unit)?.seconds ?? 1
  const seconds = Math.round(n * factor)
  if (seconds < 1) return { error: 'Süre en az 1 saniye olmalı.' }
  if (seconds > MAX_DURATION_SECONDS) return { error: 'Süre en fazla 30 gün olabilir.' }
  return { seconds }
}

// Geçerli bir taslağın saniyesi; boş ya da hatalıysa undefined.
export function durationSeconds(d: DurationDraft): number | undefined {
  const p = parseDuration(d)
  return 'seconds' in p ? p.seconds : undefined
}

// İki taslak aynı süreyi mi anlatıyor (10 dk = 600 sn); hatalı olanlar yazıldıkları gibi karşılaştırılır.
export function sameDuration(a: DurationDraft, b: DurationDraft): boolean {
  const x = parseDuration(a)
  const y = parseDuration(b)
  if ('seconds' in x && 'seconds' in y) return x.seconds === y.seconds
  return a.value.trim() === b.value.trim() && a.unit === b.unit
}

// Tam süre, sıfır olmayan birimlerle: 90 -> "1 dk 30 sn", 5400 -> "1 sa 30 dk", 93600 -> "1 gün 2 sa".
export function formatDuration(seconds: number): string {
  const parts: string[] = []
  let s = Math.max(0, Math.round(seconds))
  for (const [size, name] of [
    [86400, 'gün'],
    [3600, 'sa'],
    [60, 'dk'],
    [1, 'sn'],
  ] as const) {
    const n = Math.floor(s / size)
    s -= n * size
    if (n > 0) parts.push(`${n} ${name}`)
  }
  return parts.length > 0 ? parts.join(' ') : '0 sn'
}

// Salt okunur süre metni: süre almayan türde "anlık", süre yoksa "hemen".
export function durationText(takesDuration: boolean | undefined, seconds?: number): string {
  if (!takesDuration) return 'anlık'
  return seconds ? `${formatDuration(seconds)} boyunca` : 'hemen'
}
