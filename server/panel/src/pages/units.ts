// Protokol 4 değerlerinin gösterimi. Agent ölçtüğünü yuvarlamadan gönderir, server da öyle saklar; hassasiyet burada,
// gösterimde verilir (bkz. healthbeat-server/internal/model/ingest_v4.go). Ondalık ayracı virgüldür (bildirim metinleriyle
// aynı); bilinmeyen ya da sayı olmayan değer "—" olur. React yok: Node'un çalıştırıcısıyla birim test edilir.

const DASH = '—'

const known = (v: number | null | undefined): v is number => typeof v === 'number' && Number.isFinite(v)

// Sabit ondalıkla, virgül ayracıyla: (12.345, 1) -> "12,3".
export function decimal(v: number, digits: number): string {
  const s = v.toFixed(digits)
  // -0,0 gibi eksi işaretli sıfır gösterilmez.
  return (Number(s) === 0 ? s.replace('-', '') : s).replace('.', ',')
}

// Büyüklük arttıkça ondalık azalır, toplam anlamlı basamak sabit kalır: en çok `max` ondalık (|v| < 10), 10'un her
// katında bir eksik. Sıfır ondalıksız yazılır.
function adaptive(v: number, max: number): number {
  const a = Math.abs(v)
  if (a === 0) return 0
  if (a < 10) return max
  return Math.max(0, max - Math.floor(Math.log10(a)))
}

// Yüzde (CPU iowait/steal, PSI, disk meşguliyeti …): tek ondalık, "%12,3".
export function formatPct(v: number | null | undefined, digits = 1): string {
  return known(v) ? `%${decimal(v, digits)}` : DASH
}

// Süre (disk gecikmesi): "0,37 ms", "12,4 ms", "152 ms".
export function formatMs(v: number | null | undefined, max = 2): string {
  return known(v) ? `${decimal(v, adaptive(v, max))} ms` : DASH
}

// Saat farkı (NTP): milisaniyenin altı da anlamlıdır, işaret korunur: "1,841 ms", "-12,35 ms".
export function formatOffsetMs(v: number | null | undefined): string {
  return formatMs(v, 3)
}

export function formatCelsius(v: number | null | undefined): string {
  return known(v) ? `${decimal(v, 1)} °C` : DASH
}

// Bir hız değerini birimine böler: 100'ün altında tek ondalık, üstünde tam sayı.
function scaled(v: number, base: number, units: string[]): string {
  let x = v
  let i = 0
  while (Math.abs(x) >= base && i < units.length - 1) {
    x /= base
    i++
  }
  return `${decimal(x, i === 0 || Math.abs(x) >= 100 ? 0 : 1)} ${units[i]}`
}

// Disk hızı (bayt/sn), 1024 tabanlı (formatBytes ile aynı): "1,5 MB/sn".
export function formatByteRate(bps: number | null | undefined): string {
  return known(bps) ? scaled(bps, 1024, ['B/sn', 'KB/sn', 'MB/sn', 'GB/sn', 'TB/sn']) : DASH
}

// Ağ hızı (bit/sn), ağda alışıldığı gibi 1000 tabanlı: "94,2 Mbit/sn".
export function formatBitRate(bps: number | null | undefined): string {
  return known(bps) ? scaled(bps, 1000, ['bit/sn', 'Kbit/sn', 'Mbit/sn', 'Gbit/sn', 'Tbit/sn']) : DASH
}

// Saniyedeki işlem: 10'un altında tek ondalık ("0,4 IOPS"), üstünde tam sayı.
export function formatIOPS(v: number | null | undefined): string {
  return known(v) ? `${decimal(v, Math.abs(v) < 10 ? 1 : 0)} IOPS` : DASH
}

// Saniyedeki olay (swap'a yazılan sayfa …): "3,5/sn".
export function formatPerSecond(v: number | null | undefined): string {
  return known(v) ? `${decimal(v, 1)}/sn` : DASH
}

// Sayaç ve sınırlar (dosya tanıtıcısı, conntrack, süreç): binlik ayraçlı tam sayı, "123.456".
export function formatCount(v: number | null | undefined): string {
  return known(v) ? Math.round(v).toLocaleString('tr-TR') : DASH
}
