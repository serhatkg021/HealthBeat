// Performans sekmesindeki protokol 4 grafiklerinin saf mantığı: metrik satırlarından (ham, yuvarlanmamış) grafik
// satırları. Bir satırda olmayan değer satıra yazılmaz; çizgi orada kesilir (eski agent'ın satırları, bilinmeyen alan).
// Yuvarlama yalnızca gösterimde (units.ts). React yok: Node'un çalıştırıcısıyla birim test edilir.
import type { MetricPoint, NetIOSample } from '../types/api.ts'
import { niceAxis } from './units.ts'

export type Row = Record<string, number>

const ts = (p: MetricPoint): number => new Date(p.timestamp).getTime()

// Satıra yalnızca bilinen sayıları yazar.
function row(p: MetricPoint, values: Record<string, number | undefined>): Row {
  const out: Row = { ts: ts(p) }
  for (const [k, v] of Object.entries(values)) if (typeof v === 'number' && Number.isFinite(v)) out[k] = v
  return out
}

// Aralıkta protokol 4 zaman serisi olan en az bir satır var mı (eski agent'ta yok).
export const hasHealthSeries = (points: MetricPoint[]): boolean => points.some((p) => p.system || p.disk_io || p.net_io)

export function cpuDetailRows(points: MetricPoint[]): Row[] {
  return points.map((p) => row(p, { iowait: p.system?.cpu_detail?.iowait_pct, steal: p.system?.cpu_detail?.steal_pct }))
}

// PSI'ın 60 sn ortalaması (raporlar genelde 10–60 sn arayla gelir; 10 sn ortalaması aradaki dalgalanmayı kaçırır).
export function psiRows(points: MetricPoint[]): Row[] {
  return points.map((p) => {
    const ps = p.system?.pressure
    return row(p, {
      cpu_some: ps?.cpu?.some60,
      mem_some: ps?.memory?.some60,
      mem_full: ps?.memory?.full60,
      io_some: ps?.io?.some60,
      io_full: ps?.io?.full60,
    })
  })
}

// Üç PSI grafiğinin ortak üst sınırı: aynı ölçek karşılaştırmayı dürüst tutar. En az %5 (küçük dalgalanmalar büyümesin);
// üstü yuvarlak bir değere çekilir (niceAxis).
export function psiMax(rows: Row[]): number {
  let max = 0
  for (const r of rows) for (const [k, v] of Object.entries(r)) if (k !== 'ts' && v > max) max = v
  return niceAxis(Math.max(5, max)).top
}

// Aralıkta G/Ç'si ölçülen diskler, ada göre.
export function ioDisks(points: MetricPoint[]): string[] {
  return [...new Set(points.flatMap((p) => (p.disk_io ?? []).map((d) => d.name)))].sort()
}

export function diskIORows(points: MetricPoint[], disk: string): Row[] {
  return points.map((p) => {
    const d = p.disk_io?.find((x) => x.name === disk)
    return row(p, {
      await: d?.await_ms,
      read_bps: d?.read_bps,
      write_bps: d?.write_bps,
      read_iops: d?.read_iops,
      write_iops: d?.write_iops,
      util: d?.util_pct,
    })
  })
}

export function netInterfaces(points: MetricPoint[]): string[] {
  return [...new Set(points.flatMap((p) => (p.net_io ?? []).map((n) => n.interface)))].sort()
}

// Varsayılan seçim: aralıkta en çok trafik geçen arayüz (lo ve köprüler genelde değil).
export function busiestInterface(points: MetricPoint[]): string | undefined {
  const total = new Map<string, number>()
  for (const p of points) for (const n of p.net_io ?? []) total.set(n.interface, (total.get(n.interface) ?? 0) + n.rx_bps + n.tx_bps)
  let best: string | undefined
  for (const [name, sum] of total) if (best === undefined || sum > total.get(best)! || (sum === total.get(best) && name < best)) best = name
  return best
}

// Aralıkta görülen en çok trafikli disk (varsayılan seçim).
export function busiestDisk(points: MetricPoint[]): string | undefined {
  const total = new Map<string, number>()
  for (const p of points) for (const d of p.disk_io ?? []) total.set(d.name, (total.get(d.name) ?? 0) + d.read_bps + d.write_bps)
  let best: string | undefined
  for (const [name, sum] of total) if (best === undefined || sum > total.get(best)! || (sum === total.get(best) && name < best)) best = name
  return best
}

export function netRows(points: MetricPoint[], iface: string): Row[] {
  return points.map((p) => {
    const n = p.net_io?.find((x) => x.interface === iface)
    return row(p, { rx: n?.rx_bps, tx: n?.tx_bps })
  })
}

// Aralıktaki hata ve düşen paket toplamı (her satırdaki değer kendi aralığının farkıdır, toplanabilir).
export function netErrorTotals(points: MetricPoint[], iface: string): Pick<NetIOSample, 'rx_errors' | 'tx_errors' | 'rx_drops' | 'tx_drops'> {
  const out = { rx_errors: 0, tx_errors: 0, rx_drops: 0, tx_drops: 0 }
  for (const p of points) {
    const n = p.net_io?.find((x) => x.interface === iface)
    if (!n) continue
    out.rx_errors += n.rx_errors
    out.tx_errors += n.tx_errors
    out.rx_drops += n.rx_drops
    out.tx_drops += n.tx_drops
  }
  return out
}

export function swapRows(points: MetricPoint[]): Row[] {
  return points.map((p) => row(p, { swap_in: p.system?.memory_detail?.swap_in_per_s, swap_out: p.system?.memory_detail?.swap_out_per_s }))
}

export function tcpRows(points: MetricPoint[]): Row[] {
  return points.map((p) => row(p, { retrans: p.system?.tcp?.retrans_pct }))
}

// Son bilinen TCP bağlantı sayıları (aralığın en yeni satırından).
export function latestTcp(points: MetricPoint[]): { established?: number; time_wait?: number } | null {
  for (let i = points.length - 1; i >= 0; i--) {
    const t = points[i].system?.tcp
    if (t && (t.established !== undefined || t.time_wait !== undefined)) return { established: t.established, time_wait: t.time_wait }
  }
  return null
}

// Satırlarda verilen anahtarlardan en az biri var mı (grafik boş mu).
export const hasAny = (rows: Row[], keys: string[]): boolean => rows.some((r) => keys.some((k) => k in r))
