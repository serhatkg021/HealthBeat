// Bakım pencereleri sayfasının saf mantığı (React yok; Node'un çalıştırıcısıyla birim test edilir): zamanın okunur
// cümlesi, durum rozeti, formun API isteğine çevrilmesi ve geri doldurulması. Bütün saatler kurulumun saat dilimindeki
// yerel saattir ("2026-10-12T01:00"); çeviriyi server yapar.

import type { MaintenanceInput, MaintenanceRecurrence, MaintenanceWindow } from '../types/api.ts'

export const RECURRENCES: { value: MaintenanceRecurrence; label: string }[] = [
  { value: 'once', label: 'Tek seferlik' },
  { value: 'daily', label: 'Günlük' },
  { value: 'weekly', label: 'Haftalık' },
  { value: 'monthly', label: 'Aylık' },
]

// 1 = Pazartesi … 7 = Pazar (server ile aynı).
export const WEEKDAYS = [
  { value: 1, short: 'Pzt', long: 'Pazartesi', possessive: 'Pazartesisi' },
  { value: 2, short: 'Sal', long: 'Salı', possessive: 'Salısı' },
  { value: 3, short: 'Çar', long: 'Çarşamba', possessive: 'Çarşambası' },
  { value: 4, short: 'Per', long: 'Perşembe', possessive: 'Perşembesi' },
  { value: 5, short: 'Cum', long: 'Cuma', possessive: 'Cuması' },
  { value: 6, short: 'Cmt', long: 'Cumartesi', possessive: 'Cumartesisi' },
  { value: 7, short: 'Paz', long: 'Pazar', possessive: 'Pazarı' },
]

export const MONTH_LAST = -1

export const MONTH_WEEKS = [
  { value: 1, label: 'ilk' },
  { value: 2, label: 'ikinci' },
  { value: 3, label: 'üçüncü' },
  { value: 4, label: 'dördüncü' },
  { value: MONTH_LAST, label: 'son' },
]

// Aralık sınırları (server ile aynı): günlükte en çok 30, haftalık ve aylıkta 12.
export const MAX_REPEAT: Record<Exclude<MaintenanceRecurrence, 'once'>, number> = { daily: 30, weekly: 12, monthly: 12 }

const UNIT_NAME: Record<Exclude<MaintenanceRecurrence, 'once'>, string> = { daily: 'günde', weekly: 'haftada', monthly: 'ayda' }

// formatDuration, dakikayı okunur süreye çevirir: "30 dk", "2 saat", "1 saat 30 dk", "1 gün 8 saat".
export function formatDuration(minutes: number): string {
  const d = Math.floor(minutes / 1440)
  const h = Math.floor((minutes % 1440) / 60)
  const m = minutes % 60
  const parts = [d && `${d} gün`, h && `${h} saat`, m && `${m} dk`].filter(Boolean)
  return parts.length ? parts.join(' ') : '0 dk'
}

// formatDate, "2026-10-12" → "2026.10.12" (bakım pencerelerinin tarih biçimi: YYYY.AA.GG).
export function formatDate(date: string): string {
  return date.replaceAll('-', '.')
}

// formatLocal, "2026-10-12T01:00" → "2026.10.12 01:00".
export function formatLocal(local: string): string {
  return `${formatDate(local.slice(0, 10))} ${local.slice(11, 16)}`
}

function weekdayPhrase(days: number[], long: boolean): string {
  const set = [...new Set(days)].sort((a, b) => a - b)
  if (set.length === 7) return 'Her gün'
  if (set.join() === '1,2,3,4,5') return 'Hafta içi'
  if (set.join() === '6,7') return 'Hafta sonu'
  return set.map((d) => (long ? WEEKDAYS[d - 1]?.long : WEEKDAYS[d - 1]?.short) ?? '?').join(', ')
}

function monthPhrase(w: Pick<MaintenanceWindow, 'month_day' | 'month_week' | 'month_weekday'>): string {
  if (w.month_day !== undefined) return w.month_day === MONTH_LAST ? 'Ayın son günü' : `Ayın ${w.month_day}. günü`
  const week = MONTH_WEEKS.find((x) => x.value === w.month_week)?.label ?? '?'
  return `Ayın ${week} ${WEEKDAYS[(w.month_weekday ?? 1) - 1]?.possessive ?? '?'}`
}

// occurrenceText, bir tekrarı tek kalıpta yazar; başlangıç ve bitiş her zaman tam tarih-saattir:
// "2026.10.20 22:00 – 2026.10.20 23:30".
export function occurrenceText(startLocal: string, endLocal: string): string {
  return `${formatLocal(startLocal)} – ${formatLocal(endLocal)}`
}

// typeLabel, Tür sütunudur: "Tek seferlik", "Haftalık", aralık 1'den büyükse "Haftalık · 2 haftada".
export function typeLabel(w: Pick<MaintenanceWindow, 'recurrence' | 'repeat_every'>): string {
  const label = RECURRENCES.find((r) => r.value === w.recurrence)?.label ?? w.recurrence
  return w.recurrence !== 'once' && w.repeat_every > 1 ? `${label} · ${w.repeat_every} ${UNIT_NAME[w.recurrence]}` : label
}

// scopeCount, Kapsam sütunudur: "2 sunucu", "1 organizasyon, 1 sunucu"; görülemeyenler "+N görülemeyen".
export function scopeCount(w: Pick<MaintenanceWindow, 'hosts' | 'organizations' | 'hidden_scope'>): string {
  const parts = [
    w.organizations.length && `${w.organizations.length} organizasyon`,
    w.hosts.length && `${w.hosts.length} sunucu`,
    w.hidden_scope && `+${w.hidden_scope} görülemeyen`,
  ].filter(Boolean)
  return parts.length ? parts.join(', ') : '—'
}

// ruleText, detaydaki kural cümlesidir: "2 haftada bir · Salı, Perşembe · başlangıç 22:00 · süre 1 saat 30 dk".
export function ruleText(w: MaintenanceWindow): string {
  if (w.recurrence === 'once') return 'Tek seferlik'
  const n = w.repeat_every
  const every = n > 1 ? `${n} ${UNIT_NAME[w.recurrence]} bir` : { daily: 'Her gün', weekly: 'Her hafta', monthly: 'Her ay' }[w.recurrence]
  const which = w.recurrence === 'weekly' ? weekdayPhrase(w.weekdays ?? [], true) : w.recurrence === 'monthly' ? monthPhrase(w) : ''
  return [every, which, `başlangıç ${w.start_time}`, `süre ${formatDuration(w.duration_minutes ?? 0)}`].filter(Boolean).join(' · ')
}

// validityText, tekrarlı pencerenin tarih aralığıdır: "İlk tekrar 10.10.2026 · bitiş yok".
export function validityText(w: MaintenanceWindow): string {
  if (w.recurrence === 'once' || !w.valid_from) return ''
  return `İlk tekrar ${formatDate(w.valid_from)} · ${w.valid_until ? `son tekrar ${formatDate(w.valid_until)}` : 'bitiş yok'}`
}

// shownOccurrence, Zaman sütununun gösterdiği tekrar ve hangisi olduğudur: süren pencerede o anki ("şu an"), planlıda
// sıradaki, geçmişte son yaşanan; hiç yaşanmamış (başlamadan bitirilmiş) tek seferlik pencerede planlanan aralık.
export function shownOccurrence(w: MaintenanceWindow): { start_local: string; end_local: string; kind: string } | null {
  if (w.status === 'active' && w.current) return { ...w.current, kind: 'şu an' }
  if (w.status === 'scheduled' && w.next) return { ...w.next, kind: 'sıradaki' }
  if (w.last) return { ...w.last, kind: 'son' }
  if (w.recurrence === 'once' && w.starts_local && w.ends_local) return { start_local: w.starts_local, end_local: w.ends_local, kind: 'planlanan' }
  return null
}

// upcomingLabel, sonraki tekrarlar bölümünün başlığıdır: "Sonraki 3 tekrar", daha azı kaldıysa o sayı.
export function upcomingLabel(count: number): string {
  return count > 0 ? `Sonraki ${count} tekrar` : 'Sonraki tekrarlar'
}

export type StatusTone = 'good' | 'warning' | 'critical' | 'neutral'

// statusOf, durum rozetidir: Sürüyor, Planlı, elle bitirilmişse Bitirildi, değilse Geçmiş.
export function statusOf(w: Pick<MaintenanceWindow, 'status' | 'ended_at'>): { tone: StatusTone; text: string } {
  if (w.status === 'active') return { tone: 'warning', text: 'Sürüyor' }
  if (w.status === 'scheduled') return { tone: 'neutral', text: 'Planlı' }
  return { tone: 'neutral', text: w.ended_at ? 'Bitirildi' : 'Geçmiş' }
}

export type DurationUnit = 'min' | 'hour' | 'day'
const UNIT_MINUTES: Record<DurationUnit, number> = { min: 1, hour: 60, day: 1440 }
export const DURATION_UNITS: { value: DurationUnit; label: string }[] = [
  { value: 'min', label: 'dakika' },
  { value: 'hour', label: 'saat' },
  { value: 'day', label: 'gün' },
]

// MaintenanceDraft, formun durumudur (alanlar input değerleri olarak metindir).
export interface MaintenanceDraft {
  title: string
  recurrence: MaintenanceRecurrence
  startsLocal: string
  endsLocal: string
  startTime: string
  durationValue: string
  durationUnit: DurationUnit
  repeatEvery: string
  weekdays: number[]
  monthMode: 'day' | 'weekday'
  monthDay: string
  monthWeek: string
  monthWeekday: string
  validFrom: string
  validUntil: string
  hostIds: string[]
  orgIds: string[]
}

// localStamp, bir anı tarayıcının yerel saatinde "2026-10-10T21:11" olarak yazar (server saati alınamadığında yedek).
export function localStamp(ms: number): string {
  const d = new Date(ms)
  const p = (n: number) => String(n).padStart(2, '0')
  return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())}T${p(d.getHours())}:${p(d.getMinutes())}`
}

// emptyDraft, yeni pencerenin başlangıç değerleridir; nowLocal kurulumun saatindeki şu an ("2026-10-10T21:11").
// Tek seferlik pencere bir sonraki tam saatte başlar, iki saat sürer; tekrarlar bugünden başlar.
export function emptyDraft(nowLocal: string): MaintenanceDraft {
  const [date, time] = nowLocal.split('T')
  const hour = Number(time.slice(0, 2))
  const at = (h: number) => {
    const d = new Date(`${date}T00:00:00Z`)
    d.setUTCHours(h)
    return d.toISOString().slice(0, 16)
  }
  return {
    title: '',
    recurrence: 'once',
    startsLocal: at(hour + 1),
    endsLocal: at(hour + 3),
    startTime: '02:00',
    durationValue: '2',
    durationUnit: 'hour',
    repeatEvery: '1',
    weekdays: [7],
    monthMode: 'day',
    monthDay: '1',
    monthWeek: '1',
    monthWeekday: '7',
    validFrom: date,
    validUntil: '',
    hostIds: [],
    orgIds: [],
  }
}

// toInput, formu API gövdesine çevirir: yalnızca seçilen tekrar türünün alanları gönderilir.
export function toInput(d: MaintenanceDraft): MaintenanceInput {
  const base = { title: d.title.trim(), recurrence: d.recurrence, host_ids: d.hostIds, organization_ids: d.orgIds }
  if (d.recurrence === 'once') return { ...base, starts_local: d.startsLocal, ends_local: d.endsLocal }
  const input: MaintenanceInput = {
    ...base,
    start_time: d.startTime,
    duration_minutes: Math.round(Number(d.durationValue) * UNIT_MINUTES[d.durationUnit]),
    repeat_every: Number(d.repeatEvery) || 1,
    valid_from: d.validFrom,
    ...(d.validUntil ? { valid_until: d.validUntil } : {}),
  }
  if (d.recurrence === 'weekly') input.weekdays = [...d.weekdays].sort((a, b) => a - b)
  if (d.recurrence === 'monthly') {
    if (d.monthMode === 'day') input.month_day = Number(d.monthDay)
    else {
      input.month_week = Number(d.monthWeek)
      input.month_weekday = Number(d.monthWeekday)
    }
  }
  return input
}

// fromWindow, kayıtlı pencereyi düzenleme formuna doldurur; olmayan alanlar emptyDraft'tan gelir.
export function fromWindow(w: MaintenanceWindow, nowLocal: string): MaintenanceDraft {
  const d = emptyDraft(nowLocal)
  const minutes = w.duration_minutes ?? 120
  const unit: DurationUnit = minutes % 1440 === 0 ? 'day' : minutes % 60 === 0 ? 'hour' : 'min'
  return {
    ...d,
    title: w.title,
    recurrence: w.recurrence,
    startsLocal: w.starts_local ?? d.startsLocal,
    endsLocal: w.ends_local ?? d.endsLocal,
    startTime: w.start_time ?? d.startTime,
    durationValue: String(minutes / UNIT_MINUTES[unit]),
    durationUnit: unit,
    repeatEvery: String(w.repeat_every || 1),
    weekdays: w.weekdays ?? d.weekdays,
    monthMode: w.month_week !== undefined ? 'weekday' : 'day',
    monthDay: w.month_day !== undefined ? String(w.month_day) : d.monthDay,
    monthWeek: w.month_week !== undefined ? String(w.month_week) : d.monthWeek,
    monthWeekday: w.month_weekday !== undefined ? String(w.month_weekday) : d.monthWeekday,
    validFrom: w.valid_from ?? d.validFrom,
    validUntil: w.valid_until ?? '',
    hostIds: w.hosts.map((h) => h.id),
    orgIds: w.organizations.map((o) => o.id),
  }
}

export type MaintenanceFilter = 'all' | 'active' | 'scheduled' | 'past'

const STATUS_ORDER: Record<MaintenanceWindow['status'], number> = { active: 0, scheduled: 1, past: 2 }

// sortKey: önce süren (en erken biten önce), sonra planlı (en yakın başlayan önce), en sonda geçmiş (en yeni önce).
function compareWindows(a: MaintenanceWindow, b: MaintenanceWindow): number {
  if (a.status !== b.status) return STATUS_ORDER[a.status] - STATUS_ORDER[b.status]
  if (a.status === 'active') return (a.current?.end ?? '').localeCompare(b.current?.end ?? '')
  if (a.status === 'scheduled') return (a.next?.start ?? '').localeCompare(b.next?.start ?? '')
  return b.updated_at.localeCompare(a.updated_at)
}

// filterWindows, durum süzgecini ve metin aramasını (başlık ya da kapsam adı) uygular ve listeyi sıralar.
export function filterWindows(ws: MaintenanceWindow[], status: MaintenanceFilter, query: string): MaintenanceWindow[] {
  const q = query.trim().toLocaleLowerCase('tr')
  return ws.filter(
    (w) =>
      (status === 'all' || w.status === status) &&
      (!q || [w.title, ...w.hosts.map((h) => h.name), ...w.organizations.map((o) => o.name)].some((s) => s.toLocaleLowerCase('tr').includes(q))),
  ).sort(compareWindows)
}
