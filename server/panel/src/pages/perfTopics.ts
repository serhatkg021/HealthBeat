// Sunucu sayfasının Performans sekmesi konuya göre: her konu (CPU, Bellek, Disk …) kendi grafiklerini, son rapordaki
// değerlerini ve alert kurallarını bir arada gösterir. Konu kataloğu, menüdeki "şu an" değerleri, grafik yerleşim kuralı ve
// konunun Alert kuralları karşılığı burada. React yok; Node'un çalıştırıcısıyla birim test edilir.
import type { Alert, AlertLevel, AlertType, HostInfo, MetricPoint, SystemState } from '../types/api.ts'
import type { RuleRowView } from './hostRuleRows.ts'
import { agoText } from './cache.ts'
import { formatBytes } from './hardwareTotals.ts'
import { itemKey, topicInfo, topicItems, type RuleItem, type TopicId } from './ruleTopics.ts'
import { capacityRows, raidState } from './systemState.ts'
import { decimal, formatBitRate, formatByteRate, formatCelsius, formatCount, formatMs, formatPct } from './units.ts'

export type PerfTopicId = 'ozet' | 'cpu' | 'bellek' | 'disk' | 'ag' | 'sicaklik' | 'sinirlar'

export interface PerfTopic {
  id: PerfTopicId
  title: string
  desc: string
  // Konunun Alert kuralları karşılığı ve oradaki hangi kuralların bu konuyu ilgilendirdiği (öğe anahtarları; boşsa
  // o konunun bütün eşik ve durum kuralları). Karşılığı olmayan konuda (Özet, Ağ, Sistem sınırları) alert kuralı yoktur.
  rules?: { topic: TopicId; keys?: string[] }
}

// Menü sırası; açılışta ilki seçilidir.
export const PERF_TOPICS: PerfTopic[] = [
  { id: 'ozet', title: 'Özet', desc: 'Ana kaynakların aynı aralıkta yan yana görünümü; ayrıntı ilgili konuda.' },
  {
    id: 'cpu',
    title: 'CPU',
    desc: 'Kullanım, diski ve hipervizörü bekleme, işlerin ne kadar yavaşladığı.',
    rules: { topic: 'cpu-bellek', keys: ['threshold:cpu'] },
  },
  {
    id: 'bellek',
    title: 'Bellek',
    desc: 'Kullanım, bellek baskısı, swap ve bellek yetmezliği.',
    rules: { topic: 'cpu-bellek', keys: ['threshold:ram', 'status:oom_kill'] },
  },
  { id: 'disk', title: 'Disk', desc: 'Doluluk, G/Ç ve disk sağlığı; fiziksel disk başına.', rules: { topic: 'disk' } },
  { id: 'ag', title: 'Ağ', desc: 'Arayüz başına trafik ve hatalar; TCP bağlantıları.' },
  {
    id: 'sicaklik',
    title: 'Sıcaklık',
    desc: 'Sensör başına son rapor; donanımın bildirdiği sınırlar ve alert eşiği yan yana. Geçmişi tutulmaz.',
    rules: { topic: 'sicaklik' },
  },
  {
    id: 'sinirlar',
    title: 'Sistem sınırları',
    desc: 'Çekirdeğin kaynak sınırlarına ne kadar yakın olunduğu; dolunca yeni dosya, bağlantı ya da süreç açılamaz.',
  },
]

export const perfTopicInfo = (id: PerfTopicId): PerfTopic => PERF_TOPICS.find((t) => t.id === id)!

// Adresteki `?konu=`; bilinmeyen ya da boş değer ilk konudur.
export function resolvePerfTopic(konu: string | null | undefined): PerfTopicId {
  return PERF_TOPICS.find((t) => t.id === konu?.trim())?.id ?? PERF_TOPICS[0].id
}

// Konunun ilgilendiği alert kuralları (eşik ve durum), Alert kurallarının konu kataloğundaki sırayla.
export function perfRuleItems(id: PerfTopicId): RuleItem[] {
  const r = perfTopicInfo(id).rules
  if (!r) return []
  return topicItems(topicInfo(r.topic), 'sunucu').filter(
    (i): i is RuleItem => (i.kind === 'threshold' || i.kind === 'status') && (!r.keys || r.keys.includes(itemKey(i))),
  )
}

// Grafik yerleşimi: bir satırda en çok iki blok. Geniş blok satırı tek başına doldurur; satırında yalnız kalan blok da tam
// genişlik olur (bir sonraki geniş ya da yok). Dönen dizi, blok başına "tam genişlik mi".
export function chartLayout(blocks: readonly { wide?: boolean }[]): boolean[] {
  const out: boolean[] = []
  let col = 0
  for (let i = 0; i < blocks.length; i++) {
    const next = blocks[i + 1]
    const full = !!blocks[i].wide || (col === 0 && (!next || !!next.wide))
    out.push(full)
    col = full ? 0 : (col + 1) % 2
  }
  return out
}

// Menüde konunun yanında görünen son rapor değeri; bilinmiyorsa boş.
export function topicNow(id: PerfTopicId, latest: MetricPoint | null, state: SystemState | undefined): string {
  switch (id) {
    case 'cpu':
      return latest ? formatPct(latest.cpu_usage_pct, 0) : ''
    case 'bellek':
      return latest ? formatPct(latest.ram_usage_pct, 0) : ''
    case 'disk': {
      // En dolu mount.
      const used = (latest?.disk ?? []).map((d) => d.used_pct)
      return used.length > 0 ? formatPct(Math.max(...used), 0) : ''
    }
    case 'ag': {
      // Bütün arayüzlerde gelen + giden.
      const net = latest?.net_io ?? []
      return net.length > 0 ? formatBitRate(net.reduce((sum, n) => sum + n.rx_bps + n.tx_bps, 0)) : ''
    }
    case 'sicaklik': {
      // En sıcak sensör.
      const temps = (state?.temperatures ?? []).map((t) => t.celsius)
      return temps.length > 0 ? formatCelsius(Math.max(...temps)) : ''
    }
    case 'sinirlar': {
      // Sınırına en yakın tablo; sınırı olmayanlar sayılmaz.
      const pcts = capacityRows(state?.capacity)
        .filter((r) => !r.unlimited)
        .map((r) => r.pct)
      return pcts.length > 0 ? formatPct(Math.max(...pcts), 0) : ''
    }
    default:
      return ''
  }
}

export interface PerfTile {
  label: string
  value: string
  hint?: string
  tone?: 'warning' | 'critical'
}

export interface TileContext {
  latest: MetricPoint | null
  state?: SystemState
  cores?: number
  ramTotalMB?: number
  info?: Pick<HostInfo, 'load_avg' | 'swap'>
  // Disk ve Ağ konularında seçili disk ve arayüz.
  disk?: string
  iface?: string
  now: number
}

const DASH = '—'
const MB = 1024 * 1024

// Konunun "Şu an" kutuları: son rapordaki değerler. Bilinmeyen değer "—" olur (eski agent'ta da kutu yerinde kalır).
// Özet, Sıcaklık ve Sistem sınırlarında kutu yoktur (içerikleri zaten son rapordur).
export function perfTiles(id: PerfTopicId, ctx: TileContext): PerfTile[] {
  const { latest, state } = ctx
  const sys = latest?.system
  switch (id) {
    case 'cpu': {
      const load = ctx.info?.load_avg
      return [
        { label: 'Kullanım', value: latest ? formatPct(latest.cpu_usage_pct) : DASH, ...(ctx.cores ? { hint: `${ctx.cores} çekirdek` } : {}) },
        { label: 'Yük ortalaması', value: load && load.length === 3 ? load.map((v) => decimal(v, 2)).join(' · ') : DASH, hint: '1 · 5 · 15 dk' },
        { label: 'iowait', value: formatPct(sys?.cpu_detail?.iowait_pct), hint: 'diski bekleme' },
        { label: 'Baskı (PSI)', value: formatPct(sys?.pressure?.cpu?.some60), hint: 'some, 60 sn' },
      ]
    }
    case 'bellek': {
      const used = latest && ctx.ramTotalMB ? formatBytes((ctx.ramTotalMB * latest.ram_usage_pct * MB) / 100) : null
      const swap = ctx.info?.swap
      const oom = state?.oom_kills
      return [
        { label: 'Kullanım', value: latest ? formatPct(latest.ram_usage_pct) : DASH, ...(used ? { hint: `${used} / ${formatBytes(ctx.ramTotalMB! * MB)}` } : {}) },
        {
          label: 'Swap',
          value: swap ? (swap.total_mb === 0 ? 'yok' : swap.used_mb > 0 ? formatBytes(swap.used_mb * MB) : '0') : DASH,
          ...(swap && swap.total_mb > 0 ? { hint: `${formatBytes(swap.total_mb * MB)} içinden` } : {}),
        },
        {
          label: 'Bellek yetmezliği (OOM)',
          value: oom === undefined ? DASH : formatCount(oom),
          hint: oom === undefined ? 'bildirilmedi' : state?.oom_last_increase_at ? `son: ${agoText(state.oom_last_increase_at, ctx.now)}` : 'açılıştan beri · görülmedi',
          ...(oom ? { tone: 'warning' as const } : {}),
        },
        { label: 'Baskı (PSI)', value: formatPct(sys?.pressure?.memory?.some60), hint: 'some, 60 sn' },
      ]
    }
    case 'disk': {
      const mounts = latest?.disk ?? []
      const fullest = mounts.length > 0 ? mounts.reduce((a, b) => (b.used_pct > a.used_pct ? b : a)) : null
      const io = latest?.disk_io?.find((d) => d.name === ctx.disk)
      // Agent dizi yokken alanı hiç göndermez: son rapor varsa ve alan yoksa dizi yoktur.
      const raid = state ? (state.raid ?? []) : undefined
      const worst = raid?.map(raidState).find((r) => r.tone === 'critical') ?? raid?.map(raidState).find((r) => r.tone === 'warning')
      return [
        { label: 'En dolu mount', value: fullest ? formatPct(fullest.used_pct) : DASH, ...(fullest ? { hint: fullest.mount } : {}) },
        { label: 'Gecikme', value: io ? formatMs(io.await_ms) : DASH, hint: ctx.disk ? `${ctx.disk} · ortalama işlem` : 'ortalama işlem' },
        { label: 'Hız', value: io ? formatByteRate(io.read_bps + io.write_bps) : DASH, ...(io ? { hint: `okuma ${formatByteRate(io.read_bps)} · yazma ${formatByteRate(io.write_bps)}` } : {}) },
        raid === undefined
          ? { label: 'Yazılım RAID', value: DASH, hint: 'bildirilmedi' }
          : raid.length === 0
            ? { label: 'Yazılım RAID', value: 'yok', hint: 'dizi bulunmadı' }
            : {
                label: 'Yazılım RAID',
                value: `${raid.length} dizi`,
                hint: worst ? worst.label : 'sağlıklı',
                ...(worst ? { tone: worst.tone === 'critical' ? ('critical' as const) : ('warning' as const) } : {}),
              },
      ]
    }
    case 'ag': {
      const net = latest?.net_io?.find((n) => n.interface === ctx.iface)
      const tcp = sys?.tcp
      return [
        { label: 'Gelen', value: net ? formatBitRate(net.rx_bps) : DASH, ...(ctx.iface ? { hint: ctx.iface } : {}) },
        { label: 'Giden', value: net ? formatBitRate(net.tx_bps) : DASH, ...(ctx.iface ? { hint: ctx.iface } : {}) },
        { label: 'Kurulu TCP bağlantısı', value: tcp?.established === undefined ? DASH : formatCount(tcp.established), ...(tcp?.time_wait !== undefined ? { hint: `TIME_WAIT ${formatCount(tcp.time_wait)}` } : {}) },
        { label: 'Yeniden iletim', value: formatPct(tcp?.retrans_pct, 2), hint: 'son rapor' },
      ]
    }
    default:
      return []
  }
}

// ---- alert kuralları şeridi ve açık alert noktası ---------------------------------------------------------------

export interface RuleLine {
  name: string
  // Etkin kuralda değer, süre ve kaynak; tanımlı olmayanda boş.
  value: string
  duration: string
  source: string
  // Konuya özel değerler (mount, disk, sensör) sayıyla: "+1 diske özel". Ayrıntısı Alert kurallarındadır.
  extra: string
  on: boolean
  // accent: etkin eşik; info/warning/critical: durum kuralının seviyesi.
  tone: 'accent' | 'info' | 'warning' | 'critical'
}

// Kural listesinin satırı: etkin kurallar sütunlu satır olur, tanımlı olmayanlar yalnızca adıyla alttaki özete girer.
// `from`, devralınan değerin kaynağıdır (bilinmiyorsa yalnızca "Devralındı").
export function ruleLine(row: RuleRowView, from?: string): RuleLine {
  const subjects = row.subjects?.items.length ?? 0
  return {
    name: row.label,
    value: row.on ? row.pills.map((p) => p.text).join(' · ') : '',
    duration: row.on && row.duration ? row.duration : '',
    source: row.source === 'own' ? 'Bu sunucu' : row.source === 'inherited' ? (from ? `Devralındı · ${from}` : 'Devralındı') : '',
    extra: subjects > 0 ? `+${subjects} ${row.subjects!.label.replace(':', '').toLocaleLowerCase('tr')}` : '',
    on: row.on,
    tone: row.kind === 'durum' && row.on ? (row.pills[0]?.tone as RuleLine['tone']) : 'accent',
  }
}

// Alert türünün Performans konusu; null = Performans konularına ait değil (servis, container, saat, çevrimdışı, bakım:
// onlar Genel'deki açık sorunlarda görünür).
export const ALERT_TOPIC: Record<AlertType, PerfTopicId | null> = {
  cpu: 'cpu',
  ram: 'bellek',
  oom_kill: 'bellek',
  disk: 'disk',
  disk_missing: 'disk',
  disk_latency: 'disk',
  fs_readonly: 'disk',
  raid_degraded: 'disk',
  temperature: 'sicaklik',
  docker_restart: null,
  host_offline: null,
  service_failed: null,
  service_restart_loop: null,
  container_unhealthy: null,
  container_oom: null,
  time_sync: null,
  reboot_required: null,
  security_updates: null,
}

const LEVEL_RANK: Record<AlertLevel, number> = { info: 0, warning: 1, critical: 2 }

// Konu başına açık alert sayısı ve en kötü seviyesi (menüdeki nokta).
export function openAlertsByTopic(alerts: readonly Pick<Alert, 'alert_type' | 'level'>[]): Partial<Record<PerfTopicId, { count: number; level: AlertLevel }>> {
  const out: Partial<Record<PerfTopicId, { count: number; level: AlertLevel }>> = {}
  for (const a of alerts) {
    const topic = ALERT_TOPIC[a.alert_type]
    if (!topic) continue
    const cur = out[topic]
    out[topic] = cur ? { count: cur.count + 1, level: LEVEL_RANK[a.level] > LEVEL_RANK[cur.level] ? a.level : cur.level } : { count: 1, level: a.level }
  }
  return out
}
