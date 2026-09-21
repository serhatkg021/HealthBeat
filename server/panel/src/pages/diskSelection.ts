// Bir sunucunun hangi mount'larının disk alert'i üretebileceğini seçmek için saf mantık. React
// ve import yok; böylece Node'un yerleşik çalıştırıcısıyla birim test edilebilir.
//
// Server seçimi iki alanda saklar: all_mounts_alert (true = agent'ın raporladığı her mount) ve
// custom_alert_mounts (yalnızca false iken geçerli mount yolları; boş liste "hiçbiri" demektir).
// Ekran bunu iki mod olarak gösterir:
//  'all'      — raporlanan her mount alert üretir
//  'selected' — yalnızca işaretlenenler (boş liste "hiçbiri" demektir)
// 'all' iken de son seçili liste saklanır: kullanıcı "seçili" moda dönünce seçimini kaybetmesin.

export type Mode = 'all' | 'selected'

export interface ReportedDisk {
  mount: string
  used_pct: number
}

export interface Row {
  mount: string
  usedPct: number | null // null: agent bu mount'u (henüz) raporlamadı
  reported: boolean
  selected: boolean
}

export interface State {
  mode: Mode
  selected: Set<string>
}

// Server'ın missingMountReports değerini yansıtır.
export const MISSING_AFTER = 3

const MAX_MOUNT_BYTES = 255
const MAX_MOUNTS = 64

export function fromServer(allMounts: boolean, custom: string[]): State {
  return { mode: allMounts ? 'all' : 'selected', selected: new Set(custom) }
}

// Server'a gönderilecek şey: 'all' modunda da liste gönderilir (saklanır, kullanılmaz).
export function toServer(state: State): { all_mounts_alert: boolean; custom_alert_mounts: string[] } {
  return { all_mounts_alert: state.mode === 'all', custom_alert_mounts: [...state.selected].sort() }
}

// Server'ın kurallarını (model.ValidateMountList) yansıtır; böylece form hiçbir şey göndermeden
// önce bir sorunu açıklayabilir; yetkili olan server'dır.
export function isValidMount(mount: string): boolean {
  if (!mount.startsWith('/')) return false
  if (new TextEncoder().encode(mount).length > MAX_MOUNT_BYTES) return false
  // eslint-disable-next-line no-control-regex
  return !/[\u0000-\u001f\u007f-\u009f]/.test(mount)
}

// "/data, /mnt/backup"ı (virgül ya da yeni satırla) benzersiz, kırpılmış yollara ayrıştırır ve
// geçerli mount yolu olmayan girdileri listeler.
export function parseMountInput(text: string): { mounts: string[]; invalid: string[] } {
  const mounts: string[] = []
  const invalid: string[] = []
  for (const raw of text.split(/[,\n]/)) {
    const m = raw.trim()
    if (m === '') continue
    if (!isValidMount(m)) invalid.push(m)
    else if (!mounts.includes(m)) mounts.push(m)
  }
  return { mounts, invalid }
}

// Önce raporlanan mount'lar (yola göre), sonra seçili ama raporlanmayan mount'lar — görünür
// kalmalı, yoksa kayıtlı bir seçim görülemez ya da geri alınamazdı.
export function buildRows(reported: ReportedDisk[], selected: ReadonlySet<string>): Row[] {
  const rows: Row[] = [...reported]
    .sort((a, b) => a.mount.localeCompare(b.mount))
    .map((d) => ({ mount: d.mount, usedPct: d.used_pct, reported: true, selected: selected.has(d.mount) }))
  const known = new Set(reported.map((d) => d.mount))
  for (const m of [...selected].sort()) {
    if (!known.has(m)) rows.push({ mount: m, usedPct: null, reported: false, selected: true })
  }
  return rows
}

export function setMount(state: State, mount: string, on: boolean): State {
  const selected = new Set(state.selected)
  if (on) selected.add(mount)
  else selected.delete(mount)
  return { ...state, selected }
}

// Elle yazılan yolları (agent'ın raporlamadığı mount'lar için) seçime ekler. Bir şey yanlışsa
// bunun yerine bir hata mesajı döndürür.
export function addMounts(state: State, text: string): { state: State; error: string | null } {
  const { mounts, invalid } = parseMountInput(text)
  if (invalid.length > 0) {
    return { state, error: `Geçersiz yol: ${invalid.join(', ')} (mutlak yol olmalı, "/" ile başlamalı)` }
  }
  const selected = new Set(state.selected)
  for (const m of mounts) selected.add(m)
  if (selected.size > MAX_MOUNTS) return { state, error: `En fazla ${MAX_MOUNTS} disk seçilebilir.` }
  return { state: { ...state, selected }, error: null }
}

export function isDirty(current: State, saved: State): boolean {
  if (current.mode !== saved.mode) return true
  if (current.mode === 'all') return false // bu modda işaretli kutular önemsizdir
  if (current.selected.size !== saved.selected.size) return true
  for (const m of current.selected) if (!saved.selected.has(m)) return true
  return false
}

// Ekran için, ne olacağının tek satırlık özeti.
export function summarize(state: State, rows: Row[]): string {
  if (state.mode === 'all') return 'Raporlanan tüm diskler için alert üretilir.'
  if (state.selected.size === 0) return 'Hiç disk seçilmedi: bu sunucu için disk alert’i kapalı.'
  const missing = rows.filter((r) => r.selected && !r.reported).length
  const base = `${state.selected.size} disk için alert üretilir; diğerleri için üretilmez. Seçili bir disk ${MISSING_AFTER} ardışık raporda görünmezse “disk kayboldu” alert’i açılır.`
  return missing > 0 ? `${base} ${missing} tanesi henüz raporlanmıyor.` : base
}
