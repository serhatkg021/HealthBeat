import assert from 'node:assert/strict'
import { test } from 'node:test'
import {
  addMounts,
  buildRows,
  fromServer,
  isDirty,
  isValidMount,
  parseMountInput,
  setMount,
  summarize,
  toServer,
} from './diskSelection.ts'

const reported = [
  { mount: '/var/lib/docker', used_pct: 97 },
  { mount: '/', used_pct: 40 },
  { mount: '/data', used_pct: 96 },
]

test('server state maps to the two screen modes and back', () => {
  assert.deepEqual(fromServer(true, []), { mode: 'all', selected: new Set() })
  assert.deepEqual(fromServer(false, ['/', '/data']), { mode: 'selected', selected: new Set(['/', '/data']) })
  assert.deepEqual(fromServer(false, []), { mode: 'selected', selected: new Set() }) // hiçbiri, tümü DEĞİL
  assert.deepEqual(fromServer(true, ['/x']), { mode: 'all', selected: new Set(['/x']) }) // 'all' iken de son seçim saklanır

  // 'all' modunda liste yine gönderilir (server saklar): "seçili" moda dönünce seçim kaybolmaz.
  assert.deepEqual(toServer({ mode: 'all', selected: new Set(['/x']) }), { all_mounts_alert: true, custom_alert_mounts: ['/x'] })
  assert.deepEqual(toServer({ mode: 'selected', selected: new Set(['/data', '/']) }), { all_mounts_alert: false, custom_alert_mounts: ['/', '/data'] })
  assert.deepEqual(toServer({ mode: 'selected', selected: new Set() }), { all_mounts_alert: false, custom_alert_mounts: [] }) // hiçbiri: asla 'all' olmamalı
})

test('a saved selection survives a round trip unchanged', () => {
  for (const [all, custom] of [[true, []], [false, []], [false, ['/']], [false, ['/', '/data', '/mnt/My Disk']]] as const) {
    assert.deepEqual(toServer(fromServer(all, [...custom])), { all_mounts_alert: all, custom_alert_mounts: [...custom].sort() })
  }
})

test('rows list reported mounts by path, then selected mounts that were not reported', () => {
  const rows = buildRows(reported, new Set(['/data', '/mnt/backup']))
  assert.deepEqual(
    rows.map((r) => [r.mount, r.reported, r.selected]),
    [
      ['/', true, false],
      ['/data', true, true],
      ['/var/lib/docker', true, false],
      ['/mnt/backup', false, true], // daha önce kaydedilmiş, şimdi raporlanmıyor: geri alınabilsin diye hâlâ görünür
    ],
  )
  assert.equal(rows[3].usedPct, null)
  assert.equal(rows[1].usedPct, 96)
})

test('ticking and unticking touches one mount and does not mutate the input', () => {
  const before = fromServer(false, ['/'])
  const after = setMount(before, '/data', true)
  assert.deepEqual([...after.selected].sort(), ['/', '/data'])
  assert.deepEqual([...before.selected], ['/'])
  assert.deepEqual([...setMount(after, '/', false).selected], ['/data'])
  assert.equal(setMount(after, '/data', true).selected.size, 2)
})

test('mount validation mirrors the server', () => {
  for (const ok of ['/', '/data', '/mnt/My Disk', '/mnt/yedek-ş', '/a/b.c_d-e']) assert.equal(isValidMount(ok), true, ok)
  for (const bad of ['', 'data', 'C:\\data', '/da\u0000ta', '/data\n', '/x\u007f', '/' + 'a'.repeat(255)]) {
    assert.equal(isValidMount(bad), false, JSON.stringify(bad))
  }
  assert.equal(isValidMount('/' + 'a'.repeat(254)), true) // tam 255 bayt sorun değil
  assert.equal(isValidMount('/' + 'ş'.repeat(128)), false) // 129 karakter ama 257 bayt
})

test('typed input is split, trimmed, de-duplicated and checked', () => {
  assert.deepEqual(parseMountInput(' /data , /mnt/backup,\n/data\n\n'), { mounts: ['/data', '/mnt/backup'], invalid: [] })
  assert.deepEqual(parseMountInput('/data, backup, C:\\x'), { mounts: ['/data'], invalid: ['backup', 'C:\\x'] })
  assert.deepEqual(parseMountInput(''), { mounts: [], invalid: [] })
  assert.deepEqual(parseMountInput(' , ,\n'), { mounts: [], invalid: [] })
})

test('adding typed mounts joins the selection, or explains what is wrong and changes nothing', () => {
  const start = fromServer(false, ['/'])
  const ok = addMounts(start, '/data, /mnt/backup')
  assert.equal(ok.error, null)
  assert.deepEqual([...ok.state.selected].sort(), ['/', '/data', '/mnt/backup'])

  const bad = addMounts(start, '/data, oops')
  assert.match(bad.error ?? '', /oops/)
  assert.deepEqual([...bad.state.selected], ['/']) // /data eklenmedi: ya hep ya hiç

  const many = Array.from({ length: 65 }, (_, i) => `/m${i}`).join(',')
  assert.match(addMounts(fromServer(false, []), many).error ?? '', /64/)
})

test('dirty tracking ignores ticks while in "all" mode and detects any real change', () => {
  const saved = fromServer(false, ['/', '/data'])
  assert.equal(isDirty(saved, saved), false)
  assert.equal(isDirty(setMount(saved, '/x', true), saved), true)
  assert.equal(isDirty(setMount(saved, '/data', false), saved), true)
  assert.equal(isDirty({ mode: 'all', selected: new Set() }, saved), true)

  const savedAll = fromServer(true, [])
  assert.equal(isDirty({ mode: 'all', selected: new Set(['/junk']) }, savedAll), false)
  assert.equal(isDirty({ mode: 'selected', selected: new Set() }, savedAll), true)
  assert.equal(isDirty(fromServer(false, []), fromServer(false, [])), false)
})

test('the summary says what will happen, including the "none" and "not reported" cases', () => {
  const rows = (sel: string[]) => buildRows(reported, new Set(sel))
  assert.match(summarize(fromServer(true, []), rows([])), /tüm diskler/)
  assert.match(summarize(fromServer(false, []), rows([])), /kapalı/)
  assert.match(summarize(fromServer(false, ['/', '/data']), rows(['/', '/data'])), /2 disk için/)
  assert.match(summarize(fromServer(false, ['/', '/mnt/x']), rows(['/', '/mnt/x'])), /1 tanesi henüz raporlanmıyor/)
})

test('the summary says a selected disk that stops being reported raises a "disk kayboldu" alert — only when disks are selected', () => {
  const rows = (sel: string[]) => buildRows(reported, new Set(sel))
  assert.match(summarize(fromServer(false, ['/', '/data']), rows(['/', '/data'])), /3 ardışık raporda görünmezse “disk kayboldu”/)
  assert.doesNotMatch(summarize(fromServer(true, []), rows([])), /kayboldu/, 'with "all disks" nothing is expected, so nothing can go missing')
  assert.doesNotMatch(summarize(fromServer(false, []), rows([])), /kayboldu/, 'with none selected there is nothing to miss')
})
