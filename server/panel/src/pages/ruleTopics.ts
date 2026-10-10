// Alert kuralları sayfasının konuları: her eşik türü, durum kuralı, seçim ve otomatik alert tam olarak bir konuya aittir;
// sayfa kuralları bilgi türüne göre değil konuya göre (CPU ve bellek, Disk …) gösterir. React yok; Node'un çalıştırıcısıyla
// birim test edilir.
import type { MetricType, StatusRule } from '../types/api.ts'
import type { RuleScope } from '../navigation.ts'

export type TopicId = 'cpu-bellek' | 'disk' | 'sicaklik' | 'servis' | 'container' | 'saat' | 'bakim' | 'erisilebilirlik'

// Hangi konunun alert üreteceğinin seçimi; yalnızca sunucu kapsamında vardır.
export type SelectionKey = 'disk_mounts' | 'watched_services'

// Ayarı olmayan, her zaman açık alert'ler.
export type AutoKey = 'disk_missing' | 'host_offline'

export type TopicItem =
  | { kind: 'threshold'; metric: MetricType }
  | { kind: 'status'; rule: StatusRule }
  | { kind: 'selection'; key: SelectionKey }
  | { kind: 'auto'; key: AutoKey }

// Açılıp kapanabilen, sayılan kurallar.
export type RuleItem = Extract<TopicItem, { kind: 'threshold' | 'status' }>

export interface Topic {
  id: TopicId
  title: string
  desc: string
  items: TopicItem[]
}

export interface ItemText {
  label: string
  hint: string
}

export const SELECTIONS: Record<SelectionKey, ItemText> = {
  disk_mounts: {
    label: 'Alert üreten mount’lar',
    hint: 'Doluluk alert’i yalnızca seçili mount’lar için açılır; seçimden çıkan mount’un açık alert’i sonraki raporda kapanır.',
  },
  watched_services: {
    label: 'İzlenen servisler',
    hint: 'Servis alert’leri yalnızca izlenen servisler için açılır. Seçim sunucu sayfasının Servisler sekmesindedir.',
  },
}

export const AUTO_RULES: Record<AutoKey, ItemText> = {
  disk_missing: {
    label: 'Disk kayboldu',
    hint: 'Beklenen bir mount art arda 3 raporda görünmezse kritik alert açılır: seçim yapıldıysa seçili mount’lar, tümü seçiliyse kendi eşiği olan mount’lar.',
  },
  host_offline: {
    label: 'Sunucu çevrimdışı',
    hint: 'Sunucudan rapor aralığının 3 katı süre veri gelmezse kritik alert açılır.',
  },
}

const t = (metric: MetricType): TopicItem => ({ kind: 'threshold', metric })
const s = (rule: StatusRule): TopicItem => ({ kind: 'status', rule })

// Menü sırası; açılışta ilki seçilidir.
export const TOPICS: Topic[] = [
  { id: 'cpu-bellek', title: 'CPU ve bellek', desc: 'İşlemci ve bellek kullanımı, bellek yetmezliği.', items: [t('cpu'), t('ram'), s('oom_kill')] },
  {
    id: 'disk',
    title: 'Disk',
    desc: 'Doluluk, gecikme ve dosya sistemi sağlığı; hangi mount’ların alert üreteceği.',
    items: [
      { kind: 'selection', key: 'disk_mounts' },
      t('disk'),
      t('disk_latency'),
      { kind: 'auto', key: 'disk_missing' },
      s('fs_readonly'),
      s('raid_degraded'),
      s('raid_rebuilding'),
    ],
  },
  { id: 'sicaklik', title: 'Sıcaklık', desc: 'Sensör başına; donanımın bildirdiği sınırlar öneri olarak gösterilir.', items: [t('temperature')] },
  {
    id: 'servis',
    title: 'Servisler',
    desc: 'İzlenen systemd servisleri: çalışmıyor ya da sürekli yeniden başlıyor.',
    items: [{ kind: 'selection', key: 'watched_services' }, s('service_failed'), t('service_restart')],
  },
  {
    id: 'container',
    title: 'Container (Docker)',
    desc: 'Container’ların yeniden başlatması, sağlığı ve bellek yetmezliği.',
    items: [t('docker_restart'), s('container_unhealthy'), s('container_oom')],
  },
  { id: 'saat', title: 'Saat', desc: 'NTP senkronu ve saat farkı.', items: [t('time_offset'), s('time_unsynced'), s('time_source')] },
  { id: 'bakim', title: 'Sistem bakımı', desc: 'Bekleyen güncellemeler ve yeniden başlatma.', items: [s('reboot_required'), s('security_updates')] },
  { id: 'erisilebilirlik', title: 'Erişilebilirlik', desc: 'Sunucudan veri gelmemesi.', items: [{ kind: 'auto', key: 'host_offline' }] },
]

export const topicInfo = (id: TopicId): Topic => TOPICS.find((x) => x.id === id)!

// Adresteki `?konu=`; bilinmeyen ya da boş değer ilk konudur.
export function resolveTopic(konu: string | null | undefined): TopicId {
  return TOPICS.find((x) => x.id === konu?.trim())?.id ?? TOPICS[0].id
}

// Taslak ve değişiklik takibi için bir öğenin kalıcı anahtarı ("threshold:cpu", "status:oom_kill" …).
export function itemKey(item: TopicItem): string {
  switch (item.kind) {
    case 'threshold':
      return `threshold:${item.metric}`
    case 'status':
      return `status:${item.rule}`
    default:
      return `${item.kind}:${item.key}`
  }
}

export const isRuleItem = (item: TopicItem): item is RuleItem => item.kind === 'threshold' || item.kind === 'status'

// Bir konunun o kapsamda gösterilen öğeleri: seçimler yalnızca sunucu kapsamındadır.
export function topicItems(topic: Topic, scope: RuleScope['kind']): TopicItem[] {
  return topic.items.filter((item) => item.kind !== 'selection' || scope === 'sunucu')
}

// Menüdeki "etkin / toplam": yalnızca açılıp kapanabilen kurallar sayılır (seçimler ve otomatik alert'ler değil).
export function ruleCount(items: TopicItem[], isOn: (item: RuleItem) => boolean): { on: number; total: number } {
  const rules = items.filter(isRuleItem)
  return { on: rules.filter(isOn).length, total: rules.length }
}

// Konuda kaydedilmemiş değişiklik var mı (değişen öğelerin anahtarlarına göre).
export function topicHasChanges(items: TopicItem[], changed: ReadonlySet<string>): boolean {
  return items.some((item) => changed.has(itemKey(item)))
}
