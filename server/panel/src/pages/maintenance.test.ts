import assert from 'node:assert/strict'
import { test } from 'node:test'
import type { MaintenanceWindow } from '../types/api.ts'
import {
  emptyDraft, filterWindows, formatDuration, fromWindow, occurrenceText, ruleText, scopeCount, shownOccurrence, statusOf, toInput,
  typeLabel, upcomingLabel, validityText, weekdayOf,
} from './maintenance.ts'

function win(over: Partial<MaintenanceWindow>): MaintenanceWindow {
  return {
    id: 'w', title: 'Bakım', recurrence: 'daily', repeat_every: 1, start_time: '02:00', duration_minutes: 120, valid_from: '2026-10-10',
    hosts: [], organizations: [], hidden_scope: 0, status: 'scheduled', can_manage: true, created_at: '', updated_at: '',
    ...over,
  }
}

test('durations read naturally', () => {
  assert.equal(formatDuration(30), '30 dk')
  assert.equal(formatDuration(120), '2 saat')
  assert.equal(formatDuration(90), '1 saat 30 dk')
  assert.equal(formatDuration(32 * 60), '1 gün 8 saat')
  assert.equal(formatDuration(10080), '7 gün')
})

test('every occurrence is written the same way; a next-day end gets +1', () => {
  assert.equal(occurrenceText('2026-10-20T22:00', '2026-10-20T23:30'), 'Sal 20.10.2026 22:00 – 23:30')
  assert.equal(occurrenceText('2026-10-30T23:00', '2026-10-31T02:00'), 'Cum 30.10.2026 23:00 – 02:00 +1')
  assert.equal(occurrenceText('2026-10-10T22:00', '2026-10-12T06:00'), 'Cmt 10.10.2026 22:00 – 12.10 06:00')
  assert.equal(occurrenceText('2026-10-20T22:00', '2026-10-20T23:30', 2026), 'Sal 20.10 22:00 – 23:30')
  assert.equal(occurrenceText('2027-01-03T03:00', '2027-01-03T05:00', 2026), 'Paz 03.01.2027 03:00 – 05:00')
})

test('the type column names the kind and a longer interval', () => {
  assert.equal(typeLabel({ recurrence: 'once', repeat_every: 1 }), 'Tek seferlik')
  assert.equal(typeLabel({ recurrence: 'daily', repeat_every: 1 }), 'Günlük')
  assert.equal(typeLabel({ recurrence: 'weekly', repeat_every: 2 }), 'Haftalık · 2 haftada')
  assert.equal(typeLabel({ recurrence: 'monthly', repeat_every: 3 }), 'Aylık · 3 ayda')
})

test('the detail spells out the rule and the dates', () => {
  assert.equal(ruleText(win({})), 'Her gün · başlangıç 02:00 · süre 2 saat')
  assert.equal(ruleText(win({ recurrence: 'weekly', weekdays: [4, 2], start_time: '22:00', duration_minutes: 90, repeat_every: 2 })),
    '2 haftada bir · Salı, Perşembe · başlangıç 22:00 · süre 1 saat 30 dk')
  assert.equal(ruleText(win({ recurrence: 'weekly', weekdays: [1, 2, 3, 4, 5] })), 'Her hafta · Hafta içi · başlangıç 02:00 · süre 2 saat')
  assert.equal(ruleText(win({ recurrence: 'monthly', month_week: 1, month_weekday: 7, repeat_every: 3 })), '3 ayda bir · Ayın ilk Pazarı · başlangıç 02:00 · süre 2 saat')
  assert.equal(ruleText(win({ recurrence: 'monthly', month_day: -1 })), 'Her ay · Ayın son günü · başlangıç 02:00 · süre 2 saat')
  assert.equal(ruleText(win({ recurrence: 'once' })), 'Tek seferlik')
  assert.equal(validityText(win({})), 'İlk tekrar 10.10.2026 · bitiş yok')
  assert.equal(validityText(win({ valid_until: '2026-12-31' })), 'İlk tekrar 10.10.2026 · son tekrar 31.12.2026')
  assert.equal(validityText(win({ recurrence: 'once' })), '')
})

test('the time column shows the running, the next or the last occurrence', () => {
  const occ = (s: string) => ({ start: '', end: '', start_local: s, end_local: s })
  const shown = (w: MaintenanceWindow) => {
    const o = shownOccurrence(w)
    return o && `${o.kind}:${o.start_local}`
  }
  assert.equal(shown(win({ status: 'active', current: occ('a'), next: occ('b') })), 'şu an:a')
  assert.equal(shown(win({ status: 'scheduled', next: occ('b') })), 'sıradaki:b')
  assert.equal(shown(win({ status: 'past', last: occ('c') })), 'son:c')
  assert.equal(shown(win({ status: 'past', recurrence: 'once', starts_local: 'x', ends_local: 'y' })), 'planlanan:x')
  assert.equal(shownOccurrence(win({ status: 'past' })), null)
  assert.equal(upcomingLabel(3), 'Sonraki 3 tekrar')
  assert.equal(upcomingLabel(1), 'Sonraki 1 tekrar')
  assert.equal(upcomingLabel(0), 'Sonraki tekrarlar')
})

test('status badges are short; the time is in its own column', () => {
  assert.deepEqual(statusOf(win({ status: 'active' })), { tone: 'warning', text: 'Sürüyor' })
  assert.deepEqual(statusOf(win({ status: 'scheduled' })), { tone: 'neutral', text: 'Planlı' })
  assert.equal(statusOf(win({ status: 'past', ended_at: '2026-10-10T10:00:00Z' })).text, 'Bitirildi')
  assert.equal(statusOf(win({ status: 'past' })).text, 'Geçmiş')
})

test('scope is counted so the column always fits', () => {
  const h = (n: string) => ({ id: n, name: n })
  assert.equal(scopeCount({ hosts: [h('web-1'), h('web-2')], organizations: [], hidden_scope: 0 }), '2 sunucu')
  assert.equal(scopeCount({ hosts: [h('test-1')], organizations: [h('İstanbul DC')], hidden_scope: 2 }), '1 organizasyon, 1 sunucu, +2 görülemeyen')
  assert.equal(scopeCount({ hosts: [], organizations: [], hidden_scope: 0 }), '—')
})

test('the form becomes a request with only the chosen kind of fields, and comes back', () => {
  const d = emptyDraft('2026-10-10T21:11')
  assert.equal(d.startsLocal, '2026-10-10T22:00')
  assert.equal(d.endsLocal, '2026-10-11T00:00') // gece yarısını geçer
  assert.deepEqual(toInput({ ...d, title: ' DC ', hostIds: ['h'] }), {
    title: 'DC', recurrence: 'once', host_ids: ['h'], organization_ids: [], starts_local: '2026-10-10T22:00', ends_local: '2026-10-11T00:00',
  })

  const weekly = { ...d, recurrence: 'weekly' as const, weekdays: [4, 2], durationValue: '1.5', durationUnit: 'hour' as const, repeatEvery: '2', orgIds: ['o'] }
  assert.deepEqual(toInput(weekly), {
    title: '', recurrence: 'weekly', host_ids: [], organization_ids: ['o'], start_time: '02:00', duration_minutes: 90, repeat_every: 2,
    valid_from: '2026-10-10', weekdays: [2, 4],
  })
  const monthly = toInput({ ...d, recurrence: 'monthly', monthMode: 'weekday', monthWeek: '-1', monthWeekday: '5', validUntil: '2026-12-31' })
  assert.equal(monthly.month_day, undefined)
  assert.equal(monthly.month_week, -1)
  assert.equal(monthly.month_weekday, 5)
  assert.equal(monthly.valid_until, '2026-12-31')

  const back = fromWindow(win({ recurrence: 'monthly', month_week: -1, month_weekday: 5, duration_minutes: 2880, hosts: [{ id: 'h', name: 'h' }] }), '2026-10-10T21:11')
  assert.equal(back.monthMode, 'weekday')
  assert.equal(back.durationValue, '2')
  assert.equal(back.durationUnit, 'day')
  assert.deepEqual(back.hostIds, ['h'])
  assert.equal(fromWindow(win({ duration_minutes: 90 }), '2026-10-10T21:11').durationUnit, 'min')
})

test('filtering by status and by title or scope name', () => {
  const ws = [
    win({ id: '1', title: 'DC ağı', status: 'active', organizations: [{ id: 'o', name: 'İstanbul DC' }] }),
    win({ id: '2', title: 'Yedek', status: 'scheduled', hosts: [{ id: 'h', name: 'db-1' }] }),
    win({ id: '3', title: 'Eski', status: 'past' }),
  ]
  assert.deepEqual(filterWindows(ws, 'all', '').map((w) => w.id), ['1', '2', '3'])
  assert.deepEqual(filterWindows(ws, 'scheduled', '').map((w) => w.id), ['2'])
  assert.deepEqual(filterWindows(ws, 'all', 'istanbul').map((w) => w.id), ['1'])
  assert.deepEqual(filterWindows(ws, 'all', 'DB-1').map((w) => w.id), ['2'])
})

test('the list puts running windows first, then the soonest planned, then the past', () => {
  const occ = (start: string, end: string) => ({ start, end, start_local: start, end_local: end })
  const ws = [
    win({ id: 'past-old', status: 'past', updated_at: '2026-10-01' }),
    win({ id: 'later', status: 'scheduled', next: occ('2026-10-20T02:00', '2026-10-20T04:00') }),
    win({ id: 'running', status: 'active', current: occ('2026-10-10T20:00', '2026-10-11T01:00') }),
    win({ id: 'soon', status: 'scheduled', next: occ('2026-10-11T02:00', '2026-10-11T04:00') }),
    win({ id: 'past-new', status: 'past', updated_at: '2026-10-09' }),
  ]
  assert.deepEqual(filterWindows(ws, 'all', '').map((w) => w.id), ['running', 'soon', 'later', 'past-new', 'past-old'])
})

test('weekday names come from the local date', () => {
  assert.equal(weekdayOf('2026-10-13T02:00'), 'Sal')
  assert.equal(weekdayOf('2026-10-11T23:00'), 'Paz')
  assert.equal(weekdayOf('2026-10-12T00:30'), 'Pzt')
})
