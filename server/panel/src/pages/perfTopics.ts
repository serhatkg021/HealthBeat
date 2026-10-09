// Sunucu sayfasının Performans sekmesi konuya göre: her konu (CPU, Bellek, Disk …) kendi grafiklerini, son rapordaki
// değerlerini ve alert kurallarını bir arada gösterir. Konu kataloğu, menüdeki "şu an" değerleri, grafik yerleşim kuralı ve
// konunun Alert kuralları karşılığı burada. React yok; Node'un çalıştırıcısıyla birim test edilir.
import type { MetricPoint, SystemState } from '../types/api.ts'
import { itemKey, topicInfo, topicItems, type RuleItem, type TopicId } from './ruleTopics.ts'
import { capacityRows } from './systemState.ts'
import { formatBitRate, formatCelsius, formatPct } from './units.ts'

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
