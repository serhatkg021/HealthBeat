// Agent'ın son raporundaki anlık durumların (Host.system_state, protokol 4) panelde gösterimi — saf mantık. Bunların
// geçmişi tutulmaz; Envanter'de ve Genel'deki uyarı şeridinde son bildirilen hâl gösterilir. React yok: Node'un
// çalıştırıcısıyla birim test edilir.
import type { Capacity, HostThresholdsResponse, MetricPoint, RAIDArray, SystemState, Temperature, ThresholdLevels, TimeSync } from '../types/api.ts'
import { formatByteRate, formatMs, formatPct } from './units.ts'

export type Tone = 'good' | 'warning' | 'critical' | 'neutral'

// Bir sensörün geçerli sıcaklık eşiği: kendi değeri, yoksa sunucunun (devralınan ya da özel) değeri; yoksa null.
export function temperatureLevels(res: HostThresholdsResponse | null | undefined, sensor: string): ThresholdLevels | null {
  const own = res?.subject_thresholds?.find((t) => t.metric_type === 'temperature' && t.subject === sensor)
  if (own) return own.custom
  const v = res?.thresholds.find((t) => t.metric_type === 'temperature')
  return v?.custom ?? v?.default ?? null
}

// Rengi alert eşiği belirler (alert'le tutarlı); eşik yoksa nötr. Donanım sınırı yalnızca bilgi olarak yazılır.
export function temperatureTone(t: Pick<Temperature, 'celsius'>, levels: ThresholdLevels | null): Tone {
  if (!levels) return 'neutral'
  if (t.celsius >= levels.critical_level) return 'critical'
  if (t.celsius >= levels.warning_level) return 'warning'
  return 'good'
}

export interface CapacityRow {
  label: string
  used: number
  max: number
  pct: number
  hint: string
}

// Çekirdeğin sınırlı tablolarının doluluğu; ikisi de bilinmeyen satır atlanır.
export function capacityRows(c: Capacity | undefined): CapacityRow[] {
  if (!c) return []
  const rows: CapacityRow[] = []
  const add = (label: string, used: number | undefined, max: number | undefined, hint: string) => {
    if (used === undefined || !max) return
    rows.push({ label, used, max, pct: (used / max) * 100, hint })
  }
  add('Dosya tanıtıcısı', c.file_handles, c.file_handles_max, 'açık dosya ve soket; dolunca yeni dosya açılamaz')
  add('Bağlantı izleme (conntrack)', c.conntrack, c.conntrack_max, 'güvenlik duvarının izlediği bağlantılar; dolunca yeni bağlantı düşer')
  add('Süreç ve iş parçacığı', c.tasks, c.pid_max, 'pid_max sınırına göre; dolunca yeni süreç başlatılamaz')
  return rows
}

const RAID_STATE: Record<string, { label: string; tone: Tone }> = {
  clean: { label: 'sağlıklı', tone: 'good' },
  active: { label: 'sağlıklı', tone: 'good' },
  checking: { label: 'denetleniyor', tone: 'neutral' },
  degraded: { label: 'bozuk', tone: 'critical' },
  inactive: { label: 'devre dışı', tone: 'critical' },
  failed: { label: 'arızalı', tone: 'critical' },
  recovering: { label: 'yeniden kuruluyor', tone: 'warning' },
  resyncing: { label: 'eşitleniyor', tone: 'warning' },
  reshaping: { label: 'yeniden şekilleniyor', tone: 'warning' },
}

export const raidState = (a: Pick<RAIDArray, 'state'>): { label: string; tone: Tone } => RAID_STATE[a.state] ?? { label: a.state, tone: 'neutral' }

const SOURCE_STATE: Record<string, { label: string; tone: Tone }> = {
  selected: { label: 'kullanılıyor', tone: 'good' },
  candidate: { label: 'aday', tone: 'neutral' },
  falseticker: { label: 'yanlış saat veriyor', tone: 'critical' },
  unreachable: { label: 'ulaşılamıyor', tone: 'warning' },
  unusable: { label: 'kullanılamıyor', tone: 'warning' },
}

export const timeSourceState = (state: string): { label: string; tone: Tone } => SOURCE_STATE[state] ?? { label: state, tone: 'neutral' }

// Son 8 denemenin kaçı başarılı (reach bit maskesi).
export function reachText(reach: number | undefined): string {
  if (reach === undefined) return '—'
  let n = 0
  for (let r = reach & 0xff; r; r >>= 1) n += r & 1
  return `${n}/8`
}

const DAEMON: Record<string, string> = { timesyncd: 'systemd-timesyncd', chrony: 'chrony', ntpd: 'ntpd', none: 'yok' }
export const daemonLabel = (d?: string): string => (d ? (DAEMON[d] ?? d) : '—')

// Saat senkronundaki sorunlar (server'ın time_sync alert'iyle aynı ölçütler; burada yalnızca bilgi).
export function timeSyncIssues(t: TimeSync, nowMs: number): string[] {
  const issues: string[] = []
  if (t.enabled === false) issues.push('NTP kapalı')
  if (t.daemon === 'none') issues.push('saat senkron servisi yok')
  if (t.synchronized === false) issues.push('saat senkron değil')
  if (t.ignored) issues.push('saat sunucusunun son yanıtı geçersiz sayıldı')
  if (t.leap === 'alarm') issues.push('saat sunucusu senkron olmadığını bildiriyor (leap alarm)')
  if (t.stratum !== undefined && t.stratum >= 16) issues.push('saat sunucusu senkron değil (stratum 16)')
  if (t.sources && t.sources.length > 0 && t.sources.every((s) => s.reach === 0)) issues.push('hiçbir saat kaynağına ulaşılamıyor')
  if (t.last_sync && t.poll_s) {
    const age = (nowMs - new Date(t.last_sync).getTime()) / 1000
    if (age > 2 * t.poll_s) issues.push('son senkron beklenenden eski')
  }
  if (t.local_rtc) issues.push('donanım saati yerel saatte (UTC önerilir)')
  return issues
}

const DAY_MS = 24 * 60 * 60 * 1000

export interface StateWarning {
  tone: Tone
  text: string
}

// Genel sekmesinin uyarı şeridi: hemen bakılması gereken anlık durumlar. Alert açılıp açılmadığından bağımsızdır
// (durum kuralları kapalı olabilir); bilgi içindir.
export function stateWarnings(state: SystemState | undefined, latest: MetricPoint | null, nowMs: number): StateWarning[] {
  const out: StateWarning[] = []
  for (const a of state?.raid ?? []) {
    const st = raidState(a)
    if (st.tone === 'critical') out.push({ tone: 'critical', text: `RAID ${a.name} ${st.label} (${a.active}/${a.devices} disk etkin)` })
    else if (st.tone === 'warning') out.push({ tone: 'warning', text: `RAID ${a.name} ${st.label}${a.sync_pct !== undefined ? ` (${formatPct(a.sync_pct)})` : ''}` })
  }
  const ro = (latest?.disk ?? []).filter((d) => d.read_only).map((d) => d.mount)
  if (ro.length > 0) out.push({ tone: 'critical', text: `Salt okunur bağlı: ${ro.join(', ')}` })
  if (state?.oom_last_increase_at && nowMs - new Date(state.oom_last_increase_at).getTime() < DAY_MS) {
    out.push({ tone: 'warning', text: 'Son 24 saatte bellek yetmediği için süreç öldürüldü (OOM)' })
  }
  return out
}

// Genel'deki disk kartının G/Ç satırları: fiziksel disk başına son değerler.
export function ioSummary(latest: MetricPoint | null): { name: string; text: string }[] {
  return [...(latest?.disk_io ?? [])]
    .sort((a, b) => a.name.localeCompare(b.name))
    .map((d) => ({
      name: d.name,
      text: `gecikme ${formatMs(d.await_ms)} · okuma ${formatByteRate(d.read_bps)} · yazma ${formatByteRate(d.write_bps)} · meşgul ${formatPct(d.util_pct)}`,
    }))
}
