// Sunucu sayfasının "Genel" sekmesi bir özettir: Alert kurallarındaki konular (CPU ve bellek, Disk, Servisler, Saat …)
// birer kart; her kartta konunun son rapordaki durumu, açık alert'i ve etkin kural sayısı bir arada. Ayrıntı ilgili
// sekmededir (Performans, Servisler, Envanter). Kart içerikleri burada hesaplanır; React yok, Node'un çalıştırıcısıyla
// birim test edilir.
import type {
  Alert,
  AlertLevel,
  AlertType,
  DockerContainerReport,
  Host,
  HostServices,
  HostThresholdsResponse,
  MetricPoint,
  NotificationPreview,
} from '../types/api.ts'
import { agoText } from './cache.ts'
import { formatDateTime } from './datetime.ts'
import { formatBytes, formatRamUsage } from './hardwareTotals.ts'
import { formatUptime, supportsHealth } from './inventory.ts'
import { channelLabel } from './notificationRules.ts'
import { perfRuleItems, type RuleLine } from './perfTopics.ts'
import { isRuleItem, topicInfo, topicItems, TOPICS, type RuleItem, type TopicId } from './ruleTopics.ts'
import { isProblem, notReported } from './services.ts'
import { capacityRows, daemonLabel, raidState, temperatureLevels, temperatureTone, timeSyncIssues } from './systemState.ts'
import { decimal, formatBitRate, formatCelsius, formatCount, formatOffsetMs, formatPct } from './units.ts'
import { effectiveLevels, mountLevels, usageTone } from './usage.ts'

export type SummaryTone = 'good' | 'warning' | 'critical' | 'neutral'

export type SummaryCardId = 'cpu' | 'bellek' | 'disk' | 'ag' | 'sicaklik' | 'servis' | 'container' | 'saat' | 'bakim'

// Kartın etiket–değer satırı ("Çekirdek 12", "RAID md0 bozuk"); sorunlu satır kendi tonunda yazılır.
export interface SummaryFact {
  label: string
  value: string
  tone?: 'warning' | 'critical'
}

export interface SummaryCard {
  id: SummaryCardId
  title: string
  // Büyük yazılan değer ("%97", "senkron", "0"); bilinmiyorsa "—".
  value: string
  // Değerin ne olduğu ("kullanım", "en dolu mount · /", "sorunlu servis").
  caption?: string
  // Başlıktaki durum çipi: renk tek başına bilgi taşımaz. İyi durumda "normal", veri yoksa "bilgi yok".
  status: string
  tone: SummaryTone
  facts: SummaryFact[]
  // Veri olmadığında neden olmadığı ("agent güncellenince görünür").
  note?: string
  // Konunun açık alert'leri.
  open?: { count: number; level: AlertLevel }
  // Kartın götürdüğü sekme ve adres parametreleri.
  link: { tab: 'performans' | 'servisler' | 'envanter'; params?: Record<string, string> }
  // Konunun Alert kuralları karşılığı (kural sayısı ve bağlantı); kuralı olmayan konuda (Ağ) yok.
  rules?: { topic: TopicId; items: RuleItem[] }
}

export interface SummaryContext {
  host: Pick<Host, 'agent_protocol' | 'cpu_cores' | 'ram_total_mb' | 'host_info' | 'system_state'>
  latest: MetricPoint | null
  thresholds: HostThresholdsResponse | null
  containers: DockerContainerReport[]
  // Servis listesi; yüklenmediyse (ya da okunamadıysa) null.
  services: HostServices | null
  openAlerts: readonly Pick<Alert, 'alert_type' | 'level'>[]
  now: number
}

const DASH = '—'
const MB = 1024 * 1024
const DAY_MS = 24 * 60 * 60 * 1000

// Alert türünün kartı; null = hiçbir karta ait değil (çevrimdışı alert'i Durum kutusunda görünür).
export const ALERT_CARD: Record<AlertType, SummaryCardId | null> = {
  cpu: 'cpu',
  ram: 'bellek',
  oom_kill: 'bellek',
  disk: 'disk',
  disk_missing: 'disk',
  disk_latency: 'disk',
  fs_readonly: 'disk',
  raid_degraded: 'disk',
  temperature: 'sicaklik',
  docker_restart: 'container',
  container_unhealthy: 'container',
  container_oom: 'container',
  service_failed: 'servis',
  service_restart_loop: 'servis',
  time_sync: 'saat',
  reboot_required: 'bakim',
  security_updates: 'bakim',
  host_offline: null,
}

const LEVEL_RANK: Record<AlertLevel, number> = { info: 0, warning: 1, critical: 2 }
const TONE_RANK: Record<SummaryTone, number> = { neutral: 0, good: 0, warning: 1, critical: 2 }

// Üstteki "Açık alert'ler" kutusu: seviye başına sayı ve en eski açık alert'in yaşı ("1 gün 2 sa önce").
export function alertTally(alerts: readonly Pick<Alert, 'level' | 'created_at'>[], now: number): { critical: number; warning: number; info: number; oldest?: string } {
  const out: { critical: number; warning: number; info: number; oldest?: string } = { critical: 0, warning: 0, info: 0 }
  let oldest = Infinity
  for (const a of alerts) {
    out[a.level] += 1
    const t = Date.parse(a.created_at)
    if (t < oldest) oldest = t
  }
  if (Number.isFinite(oldest)) out.oldest = agoText(new Date(oldest).toISOString(), now)
  return out
}

// Kart başına açık alert sayısı ve en kötü seviyesi.
export function openAlertsByCard(alerts: readonly Pick<Alert, 'alert_type' | 'level'>[]): Partial<Record<SummaryCardId, { count: number; level: AlertLevel }>> {
  const out: Partial<Record<SummaryCardId, { count: number; level: AlertLevel }>> = {}
  for (const a of alerts) {
    const card = ALERT_CARD[a.alert_type]
    if (!card) continue
    const cur = out[card]
    out[card] = cur ? { count: cur.count + 1, level: LEVEL_RANK[a.level] > LEVEL_RANK[cur.level] ? a.level : cur.level } : { count: 1, level: a.level }
  }
  return out
}

// İki tondan kötüsü (iyi ile nötr eşit sayılır; ilki korunur).
const worse = (a: SummaryTone, b: SummaryTone): SummaryTone => (TONE_RANK[b] > TONE_RANK[a] ? b : a)

const levelTone = (level: AlertLevel): SummaryTone => (level === 'critical' ? 'critical' : level === 'warning' ? 'warning' : 'neutral')

// Tonun varsayılan çip metni; kart daha belirgin bir neden söyleyebiliyorsa ("salt okunur") onu kullanır.
const TONE_STATUS: Record<SummaryTone, string> = { good: 'normal', neutral: 'normal', warning: 'uyarı', critical: 'kritik' }

const NO_DATA = 'bilgi yok'

// Bir konunun Alert kurallarındaki sayılan kuralları (sunucu kapsamı).
const topicRules = (topic: TopicId): { topic: TopicId; items: RuleItem[] } => ({ topic, items: topicItems(topicInfo(topic), 'sunucu').filter(isRuleItem) })

// Protokol 4 verisi gelmeyen kartın açıklaması: eski agent'ta neden yok, yenisinde henüz gelmedi.
const missing = (host: SummaryContext['host']): string => (supportsHealth(host) ? 'Agent bu bilgiyi henüz bildirmedi.' : 'Bu agent sürümü göndermiyor; agent güncellenince görünür.')

const NO_REPORT = 'Henüz rapor gelmedi.'

// Verisi olmayan kart.
const empty = (note: string) => ({ value: DASH, status: NO_DATA, tone: 'neutral' as const, facts: [], note })

function cpuCard(ctx: SummaryContext): SummaryCard {
  const { latest, host } = ctx
  const base = { id: 'cpu' as const, title: 'CPU', link: { tab: 'performans' as const, params: { konu: 'cpu' } }, rules: { topic: 'cpu-bellek' as const, items: perfRuleItems('cpu') } }
  if (!latest) return { ...base, ...empty(NO_REPORT) }
  const tone = usageTone(latest.cpu_usage_pct, effectiveLevels(ctx.thresholds?.thresholds, 'cpu'))
  const load = host.host_info?.load_avg
  const facts: SummaryFact[] = []
  if (host.cpu_cores) facts.push({ label: 'Çekirdek', value: String(host.cpu_cores) })
  if (load && load.length === 3) facts.push({ label: 'Yük (1 · 5 · 15 dk)', value: load.map((v) => decimal(v, 2)).join(' · ') })
  return { ...base, value: formatPct(latest.cpu_usage_pct, 0), caption: 'kullanım', status: TONE_STATUS[tone], tone, facts }
}

function memoryCard(ctx: SummaryContext): SummaryCard {
  const { latest, host, now } = ctx
  const state = host.system_state
  const base = { id: 'bellek' as const, title: 'Bellek', link: { tab: 'performans' as const, params: { konu: 'bellek' } }, rules: { topic: 'cpu-bellek' as const, items: perfRuleItems('bellek') } }
  if (!latest) return { ...base, ...empty(NO_REPORT) }
  let tone: SummaryTone = usageTone(latest.ram_usage_pct, effectiveLevels(ctx.thresholds?.thresholds, 'ram'))
  let status = TONE_STATUS[tone]
  const facts: SummaryFact[] = []
  const used = formatRamUsage(latest.ram_usage_pct, host.ram_total_mb)
  if (used) facts.push({ label: 'Kullanılan', value: used })
  const swap = host.host_info?.swap
  if (swap) facts.push({ label: 'Swap', value: swap.total_mb === 0 ? 'yok' : `${swap.used_mb > 0 ? formatBytes(swap.used_mb * MB) : '0'} / ${formatBytes(swap.total_mb * MB)}` })
  if (state?.oom_last_increase_at && now - Date.parse(state.oom_last_increase_at) < DAY_MS) {
    facts.push({ label: 'Bellek yetmedi (OOM)', value: agoText(state.oom_last_increase_at, now), tone: 'warning' })
    if (tone === 'good') status = 'OOM'
    tone = worse(tone, 'warning')
  }
  return { ...base, value: formatPct(latest.ram_usage_pct, 0), caption: 'kullanım', status, tone, facts }
}

function diskCard(ctx: SummaryContext): SummaryCard {
  const { latest, host } = ctx
  const base = { id: 'disk' as const, title: 'Disk', link: { tab: 'performans' as const, params: { konu: 'disk' } }, rules: { topic: 'disk' as const, items: perfRuleItems('disk') } }
  const mounts = latest?.disk ?? []
  if (mounts.length === 0) return { ...base, ...empty(latest ? 'Agent mount bildirmedi.' : NO_REPORT) }
  const fullest = mounts.reduce((a, b) => (b.used_pct > a.used_pct ? b : a))
  let tone: SummaryTone = usageTone(fullest.used_pct, mountLevels(ctx.thresholds?.thresholds, ctx.thresholds?.mount_thresholds, fullest.mount))
  let status = TONE_STATUS[tone]
  const facts: SummaryFact[] = [{ label: 'Mount', value: String(mounts.length) }]
  const readOnly = mounts.filter((d) => d.read_only).map((d) => d.mount)
  if (readOnly.length > 0) {
    facts.push({ label: 'Salt okunur', value: readOnly.join(', '), tone: 'critical' })
    tone = 'critical'
    status = 'salt okunur'
  }
  const raid = (host.system_state?.raid ?? []).map((a) => ({ name: a.name, ...raidState(a) }))
  const bad = raid.find((r) => r.tone === 'critical') ?? raid.find((r) => r.tone === 'warning')
  if (bad && (bad.tone === 'critical' || bad.tone === 'warning')) {
    facts.push({ label: 'RAID', value: `${bad.name} ${bad.label}`, tone: bad.tone })
    tone = worse(tone, bad.tone)
    if (bad.tone === 'critical' && readOnly.length === 0) status = 'RAID bozuk'
  } else if (raid.length > 0) {
    facts.push({ label: 'RAID', value: `sağlıklı · ${raid.length} dizi` })
  }
  return { ...base, value: formatPct(fullest.used_pct, 0), caption: `en dolu mount · ${fullest.mount}`, status, tone, facts }
}

function networkCard(ctx: SummaryContext): SummaryCard {
  const { latest, host } = ctx
  const base = { id: 'ag' as const, title: 'Ağ', link: { tab: 'performans' as const, params: { konu: 'ag' } } }
  const net = latest?.net_io ?? []
  if (net.length === 0) return { ...base, ...empty(latest ? missing(host) : NO_REPORT) }
  const busiest = net.reduce((a, b) => (b.rx_bps + b.tx_bps > a.rx_bps + a.tx_bps ? b : a))
  const facts: SummaryFact[] = [{ label: 'En yoğun arayüz', value: busiest.interface }]
  const tcp = latest?.system?.tcp?.established
  if (tcp !== undefined) facts.push({ label: 'TCP bağlantısı', value: formatCount(tcp) })
  return { ...base, value: formatBitRate(net.reduce((sum, n) => sum + n.rx_bps + n.tx_bps, 0)), caption: 'gelen + giden', status: TONE_STATUS.neutral, tone: 'neutral', facts }
}

function temperatureCard(ctx: SummaryContext): SummaryCard {
  const { host } = ctx
  const base = { id: 'sicaklik' as const, title: 'Sıcaklık', link: { tab: 'performans' as const, params: { konu: 'sicaklik' } }, rules: { topic: 'sicaklik' as const, items: perfRuleItems('sicaklik') } }
  const temps = host.system_state?.temperatures ?? []
  if (temps.length === 0) return { ...base, ...empty(missing(host)) }
  const hottest = temps.reduce((a, b) => (b.celsius > a.celsius ? b : a))
  const levels = temperatureLevels(ctx.thresholds, hottest.sensor)
  const tone = temperatureTone(hottest, levels)
  const facts: SummaryFact[] = [
    { label: 'Sensör', value: hottest.sensor },
    { label: 'Sensör sayısı', value: String(temps.length) },
  ]
  if (levels) facts.push({ label: 'Eşik', value: `uyarı ${formatCelsius(levels.warning_level)} · kritik ${formatCelsius(levels.critical_level)}` })
  return { ...base, value: formatCelsius(hottest.celsius), caption: 'en sıcak sensör', status: TONE_STATUS[tone], tone, facts }
}

function serviceCard(ctx: SummaryContext): SummaryCard {
  const base = { id: 'servis' as const, title: 'Servisler', link: { tab: 'servisler' as const, params: { tur: 'sistem' } }, rules: topicRules('servis') }
  const s = ctx.services
  if (!s) return { ...base, ...empty('Servis listesi okunamadı.') }
  if (s.services.length === 0 && s.watched.length === 0) return { ...base, ...empty(missing(ctx.host)) }
  const problems = s.services.filter(isProblem).length
  const silent = notReported(s.watched, s.services).length
  const facts: SummaryFact[] = [
    { label: 'İzlenen', value: s.watched.length > 0 ? String(s.watched.length) : 'seçilmedi' },
    { label: 'Toplam servis', value: String(s.services.length) },
  ]
  if (silent > 0) facts.push({ label: 'Raporda olmayan', value: String(silent), tone: 'warning' })
  const tone: SummaryTone = problems > 0 ? 'critical' : silent > 0 ? 'warning' : s.watched.length > 0 ? 'good' : 'neutral'
  const status = problems > 0 ? 'çalışmıyor' : silent > 0 ? 'raporda yok' : TONE_STATUS[tone]
  return { ...base, value: String(problems), caption: 'sorunlu servis', status, tone, facts }
}

function containerCard(ctx: SummaryContext): SummaryCard {
  const base = { id: 'container' as const, title: 'Container', link: { tab: 'servisler' as const, params: { tur: 'docker' } }, rules: topicRules('container') }
  const list = ctx.containers
  if (list.length === 0) return { ...base, ...empty('Container bildirilmedi (Docker yok ya da boş).') }
  const running = list.filter((c) => c.status === 'running').length
  const unhealthy = list.filter((c) => c.health === 'unhealthy' || c.oom_killed).length
  const stopped = list.length - running
  const restarted = list.filter((c) => c.restart_count > 0).length
  const facts: SummaryFact[] = []
  if (unhealthy > 0) facts.push({ label: 'Sağlıksız', value: String(unhealthy), tone: 'critical' })
  // Durmuş container her zaman sorun değildir (tek seferlik işler); yalnızca bilgi olarak yazılır.
  facts.push({ label: 'Durmuş', value: String(stopped) })
  if (restarted > 0) facts.push({ label: 'Yeniden başlamış', value: String(restarted) })
  const tone: SummaryTone = unhealthy > 0 ? 'critical' : 'good'
  return { ...base, value: `${running}/${list.length}`, caption: 'çalışıyor', status: unhealthy > 0 ? 'sağlıksız' : TONE_STATUS[tone], tone, facts }
}

function clockCard(ctx: SummaryContext): SummaryCard {
  const { host, now } = ctx
  const base = { id: 'saat' as const, title: 'Saat', link: { tab: 'envanter' as const }, rules: topicRules('saat') }
  const t = host.system_state?.time_sync
  const synced = t?.synchronized ?? host.host_info?.time_synced
  if (synced === undefined) return { ...base, ...empty(missing(host)) }
  const facts: SummaryFact[] = []
  if (t?.offset_ms !== undefined) facts.push({ label: 'Fark', value: formatOffsetMs(t.offset_ms) })
  if (t?.daemon) facts.push({ label: 'Servis', value: daemonLabel(t.daemon) })
  if (t?.stratum !== undefined) facts.push({ label: 'Stratum', value: String(t.stratum) })
  const issues = t ? timeSyncIssues(t, now).filter((i) => i !== 'saat senkron değil') : []
  if (issues.length > 0) facts.push({ label: 'Sorun', value: issues[0], tone: 'warning' })
  const tone: SummaryTone = !synced ? 'critical' : issues.length > 0 ? 'warning' : 'good'
  return { ...base, value: synced ? 'senkron' : 'senkron değil', caption: 'NTP', status: !synced ? 'senkron değil' : issues.length > 0 ? 'sorun' : TONE_STATUS[tone], tone, facts }
}

function upkeepCard(ctx: SummaryContext): SummaryCard {
  const { host } = ctx
  const base = { id: 'bakim' as const, title: 'Sistem bakımı', link: { tab: 'envanter' as const }, rules: topicRules('bakim') }
  const updates = host.system_state?.updates
  const reboot = host.host_info?.reboot_required
  const uptime = host.host_info?.uptime_seconds
  const uptimeFact: SummaryFact[] = uptime !== undefined ? [{ label: 'Çalışma süresi', value: formatUptime(uptime) }] : []
  if (!updates && reboot === undefined) return { ...base, ...empty(missing(host)), facts: uptimeFact }
  const security = updates?.security ?? 0
  const facts: SummaryFact[] = []
  if (updates) facts.push({ label: 'Güvenlik güncellemesi', value: formatCount(security), ...(security > 0 ? { tone: 'warning' as const } : {}) })
  if (reboot !== undefined) facts.push({ label: 'Yeniden başlatma', value: reboot ? 'gerekli' : 'gerekmiyor', ...(reboot ? { tone: 'warning' as const } : {}) })
  facts.push(...uptimeFact)
  const tone: SummaryTone = security > 0 || reboot ? 'warning' : 'good'
  const status = security > 0 ? 'güvenlik' : reboot ? 'yeniden başlat' : TONE_STATUS[tone]
  // Güncelleme bilgisi yoksa (eski agent) büyük değer yeniden başlatma durumudur.
  if (!updates) return { ...base, value: reboot ? 'gerekli' : 'gerekmiyor', caption: 'yeniden başlatma', status, tone, facts: facts.filter((f) => f.label !== 'Yeniden başlatma'), note: 'Güncelleme bilgisi agent güncellenince görünür.' }
  return { ...base, value: formatCount(updates.pending), caption: 'bekleyen güncelleme', status, tone, facts }
}

// Genel'in konu kartları, Alert kurallarındaki konu sırasıyla (CPU ve bellek ikiye, Ağ Disk'in ardına eklenmiş). Açık
// alert'i olan kartın rengi en az alert'in seviyesidir. Her kartın bir durum çipi vardır.
export function summaryCards(ctx: SummaryContext): SummaryCard[] {
  const open = openAlertsByCard(ctx.openAlerts)
  const cards = [cpuCard(ctx), memoryCard(ctx), diskCard(ctx), networkCard(ctx), temperatureCard(ctx), serviceCard(ctx), containerCard(ctx), clockCard(ctx), upkeepCard(ctx)]
  return cards.map((card) => {
    const o = open[card.id]
    if (!o) return card
    const tone = worse(card.tone, levelTone(o.level))
    // Rengi alert yükselttiyse çip de alert'in seviyesini söyler.
    return { ...card, open: o, tone, status: tone !== card.tone ? TONE_STATUS[tone] : card.status }
  })
}

// Kural satırlarının sayısı: etkin, toplam ve bu sunucuya özel olanlar.
export function ruleCounts(lines: readonly Pick<RuleLine, 'on' | 'source'>[]): { on: number; total: number; custom: number } {
  return { on: lines.filter((l) => l.on).length, total: lines.length, custom: lines.filter((l) => l.source === 'Bu sunucu').length }
}

// Sunucu kapsamında sayılan bütün kurallar (alttaki toplam).
export const ALL_RULE_ITEMS: RuleItem[] = TOPICS.flatMap((t) => topicItems(t, 'sunucu').filter(isRuleItem))

// Bildirim önizlemesinin özeti: kaç alıcıya, hangi kanaldan gideceği. Sistem kurallarını göremeyen kullanıcıda sistem
// kaynaklı alıcılar yalnızca sayı olarak gelir; onlar da sayılır.
export function recipientSummary(p: Pick<NotificationPreview, 'recipients' | 'system_summary' | 'in_maintenance'>): { total: number; channels: string } {
  const counts = new Map<string, number>()
  const add = (channel: string, n: number) => counts.set(channel, (counts.get(channel) ?? 0) + n)
  for (const r of p.recipients) add(r.channel, 1)
  for (const c of p.system_summary ?? []) if (!c.reason) add(c.channel, c.count)
  let total = 0
  const parts: string[] = []
  for (const [channel, n] of counts) {
    total += n
    parts.push(`${channelLabel(channel)} ${n}`)
  }
  return { total, channels: parts.join(' · ') }
}

// Kartların üstündeki satır: değerlerin ne kadar taze olduğu. Sunucu çevrimdışıysa değerler son bilinen hâldir ve kartlar
// renksiz gösterilir (kritik kırmızı "şu an kritik" sanılmasın).
export function freshness(status: Host['status'], latest: Pick<MetricPoint, 'timestamp'> | null, now: number): { stale: boolean; text: string } | null {
  if (!latest) return null
  if (status === 'online') return { stale: false, text: `Son rapor · ${formatDateTime(latest.timestamp)}` }
  return { stale: true, text: `Son değerler · ${agoText(latest.timestamp, now)} · sunucu çevrimdışı, değerler güncel değil` }
}

// Çekirdek sınırlarından %80'ini geçenler (Sistem sınırları ayrı kart değildir; yalnızca dolmaya yaklaşınca görünür).
export function capacityWarnings(state: Host['system_state']): { tone: 'warning' | 'critical'; text: string }[] {
  return capacityRows(state?.capacity)
    .filter((r) => !r.unlimited && r.pct >= 80)
    .map((r) => ({ tone: r.pct >= 95 ? ('critical' as const) : ('warning' as const), text: `${r.label} sınırın ${formatPct(r.pct, 0)} kadarında` }))
}
