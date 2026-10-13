import { useEffect, useState } from 'react'
import { Link } from 'react-router-dom'
import {
  Activity,
  BellRing,
  CircleArrowUp,
  CircleCheck,
  Clock,
  Cog,
  Container,
  Cpu,
  Download,
  HardDrive,
  History,
  MemoryStick,
  Network,
  Thermometer,
  TriangleAlert,
  Upload,
  Wrench,
  type LucideIcon,
} from 'lucide-react'
import { alertsApi, hostsApi, notificationsApi } from '../api/endpoints'
import { useAuth } from '../auth/AuthContext'
import type { Alert, DockerContainerReport, Host, HostServices, HostThresholdsResponse, MetricPoint, NotificationPreview } from '../types/api'
import { AgentBadge } from '../components/AgentBadge'
import { StatTile } from '../components/StatTile'
import { StatusBadge } from '../components/StatusBadge'
import { useNow } from '../components/useNow'
import { agentHint, agentKind, needsUpdate, unsupportedFieldsNotice, type AgentPolicy } from './agentStatus'
import { statusDuration } from './hostStatus'
import { alertLevelTone, hostStatusLabel } from '../labels'
import { formatDateTime } from './datetime'
import { ALL_RULE_ITEMS, alertTally, capacityWarnings, freshness, recipientSummary, ruleCounts, summaryCards, type SummaryCard, type SummaryCardId } from './hostSummary'
import { useHostRules } from './useHostRules'
import { alertRulesPath } from '../navigation'

// Genel'deki açık alert listesi bu kadar uzun olabilir; kartların alert noktaları da bundan sayılır.
const OPEN_ALERTS_FETCH = 200

interface OpenAlerts {
  items: Alert[]
  total: number
}

const CARD_ICON: Record<SummaryCardId, LucideIcon> = {
  cpu: Cpu,
  bellek: MemoryStick,
  disk: HardDrive,
  ag: Network,
  sicaklik: Thermometer,
  servis: Cog,
  container: Container,
  saat: Clock,
  bakim: Wrench,
}

// Sunucu sayfasının "Genel" sekmesi bir özettir: durum ve agent, açık sorunlar, ardından Alert kurallarındaki konular
// birer kart (son rapordaki durum, açık alert, etkin kural sayısı) ve bildirimin kime gideceği. Ayrıntı ilgili sekmededir;
// kart oraya götürür. Kart içerikleri hostSummary.ts'te hesaplanır.
export function HostOverview({
  host,
  latest,
  thresholds,
  containers,
  policy,
}: {
  host: Host
  latest: MetricPoint | null
  thresholds: HostThresholdsResponse | null
  containers: DockerContainerReport[]
  policy: AgentPolicy
}) {
  const { can } = useAuth()
  const online = host.status === 'online'
  // Çevrimiçiyse uptime, çevrimdışıysa son veriden bu yana geçen süre (canlı akar); bkz. statusDuration.
  const now = useNow(5000)
  const duration = statusDuration(host, now)
  // Son görülme Durum kutusunun alt satırında: çevrimdışıyken süre zaten son veriden bu yana olduğu için yalnızca zaman.
  const seenAt = host.last_seen ? formatDateTime(host.last_seen) : ''
  let statusHint = duration?.hint
  if (seenAt) statusHint = online && duration ? `${duration.hint} · son görülme ${seenAt}` : `son görülme ${seenAt}`
  const kind = agentKind(host, policy)

  const canSeeAlerts = can('alert.view')
  const [open, setOpen] = useState<OpenAlerts | null>(null)
  const [acknowledged, setAcknowledged] = useState(0)
  const [services, setServices] = useState<HostServices | null>(null)
  const rules = useHostRules(host, thresholds)

  useEffect(() => {
    let cancelled = false
    if (canSeeAlerts) {
      alertsApi
        .list({ status: 'open', hostId: host.id, q: '', limit: OPEN_ALERTS_FETCH, offset: 0 })
        .then((page) => !cancelled && setOpen({ items: page.items, total: page.total }))
        .catch(() => !cancelled && setOpen({ items: [], total: 0 }))
      alertsApi
        .list({ status: 'acknowledged', hostId: host.id, q: '', limit: 1, offset: 0 })
        .then((page) => !cancelled && setAcknowledged(page.total))
        .catch(() => undefined)
    }
    hostsApi
      .services(host.id)
      .then((s) => !cancelled && setServices(s))
      .catch(() => undefined)
    return () => {
      cancelled = true
    }
  }, [host.id, canSeeAlerts])

  const fresh = freshness(host.status, latest, now)
  const stale = fresh?.stale ?? false
  const cards = summaryCards({ host, latest, thresholds, containers, services, openAlerts: open?.items ?? [], now })
  const limits = capacityWarnings(host.system_state)
  const ruleText = (card: SummaryCard): string | null => {
    if (!card.rules || !rules.ready) return null
    const c = ruleCounts(rules.linesFor(card.rules.items))
    return `${c.on}/${c.total} kural etkin`
  }

  return (
    <div>
      {needsUpdate(kind) && (
        <div className="notice" role="status">
          <div className="notice-title">
            <CircleArrowUp size={16} strokeWidth={1.9} />
            Agent güncellenmeli
          </div>
          {agentHint(kind, policy)}
        </div>
      )}
      {unsupportedFieldsNotice(host.unsupported_fields) && (
        <div className="notice" role="status">
          <div className="notice-title">
            <CircleArrowUp size={16} strokeWidth={1.9} />
            Server güncellenmeli
          </div>
          {unsupportedFieldsNotice(host.unsupported_fields)}
        </div>
      )}

      <div className="stat-grid host-top">
        <StatTile
          label="Durum"
          icon={Activity}
          tone={online ? 'good' : 'critical'}
          small
          value={
            <span className="tile-inline">
              <StatusBadge tone={online ? 'good' : 'critical'}>{hostStatusLabel(host.status)}</StatusBadge>
              {duration && (
                <span className={`tile-uptime${online ? '' : ' tile-offline'}`} title={duration.title}>
                  {duration.text}
                </span>
              )}
            </span>
          }
          hint={statusHint}
        />
        {open && <AlertsTile open={open} acknowledged={acknowledged} now={now} />}
        <StatTile
          label="Agent"
          icon={host.mode === 'push' ? Upload : Download}
          tone="accent"
          small
          value={<AgentBadge host={host} policy={policy} />}
          // Çalışma biçimi ve rapor aralığı tek satırda.
          hint={host.mode === 'push' ? `Push · ${host.interval_seconds} sn'de bir rapor` : `Pull · ${host.interval_seconds} sn'de bir sorgu`}
        />
      </div>

      <NotifySummary host={host} rules={rules.ready ? ruleCounts(rules.linesFor(ALL_RULE_ITEMS)) : null} />

      {fresh && (
        <p className={`summary-freshness${stale ? ' stale' : ''}`} role={stale ? 'status' : undefined}>
          <History size={14} strokeWidth={1.9} />
          {fresh.text}
        </p>
      )}
      {limits.length > 0 && (
        <div className="state-warnings" role="status">
          {limits.map((w) => (
            <StatusBadge key={w.text} tone={stale ? 'neutral' : w.tone}>
              <TriangleAlert size={13} strokeWidth={2} /> {w.text}
            </StatusBadge>
          ))}
        </div>
      )}

      <div className="summary-grid">
        {cards.map((card) => (
          <SummaryTile key={card.id} card={card} stale={stale} rules={ruleText(card)} rulesTo={card.rules && alertRulesPath({ kind: 'sunucu', id: host.id }, card.rules.topic)} />
        ))}
      </div>
    </div>
  )
}

const TAB_LABEL: Record<SummaryCard['link']['tab'], string> = { performans: 'Performans', servisler: 'Servisler', envanter: 'Envanter' }

// Konu kartı: başlıkta simge, ad, durum çipi ve açık alert sayısı; ortada büyük değer ve ne olduğu; altında etiket–değer
// satırları; en altta etkin kural sayısı ve kartın götürdüğü sekme. Kartın tamamı ilgili sekmeye götürür (başlık
// bağlantısı kartı kaplar); kural sayısı ayrı bağlantıdır, Alert kurallarında o konuyu açar. Uyarı ve kritikte kenar
// durum renginde ışır (sabit, animasyonsuz); değerler eskiyse (sunucu çevrimdışı) renk, ışıma ve durum çipi gösterilmez.
function SummaryTile({ card, stale, rules, rulesTo }: { card: SummaryCard; stale: boolean; rules: string | null; rulesTo?: string }) {
  const Icon = CARD_ICON[card.id]
  const tone = stale ? 'neutral' : card.tone
  const noData = card.value === '—'
  const search = new URLSearchParams({ sekme: card.link.tab, ...card.link.params })
  return (
    <div className={`card summary-card tone-${tone}${noData ? ' no-data' : ''}`}>
      <div className="summary-card-head">
        <span className="summary-card-icon">
          <Icon size={16} strokeWidth={1.8} />
        </span>
        <Link className="summary-card-link" to={{ search: `?${search}` }}>
          {card.title}
        </Link>
        <span className="summary-card-badges">
          {card.open && (
            <StatusBadge tone={alertLevelTone(card.open.level)} title={`${card.open.count} açık alert`}>
              {card.open.count} alert
            </StatusBadge>
          )}
          {!stale && <span className={`summary-chip chip-${noData ? 'none' : tone}`}>{card.status}</span>}
        </span>
      </div>
      <div className="summary-card-value">
        <span className="summary-card-number">{card.value}</span>
        {card.caption && <span className="summary-card-caption">{card.caption}</span>}
      </div>
      {card.facts.length > 0 && (
        <dl className="summary-card-facts">
          {card.facts.map((f) => (
            <div key={f.label} className={f.tone && !stale ? `fact-${f.tone}` : undefined}>
              <dt>{f.label}</dt>
              <dd>{f.value}</dd>
            </div>
          ))}
        </dl>
      )}
      {card.note && <p className="summary-card-note">{card.note}</p>}
      <div className="summary-card-foot">
        {rules && rulesTo ? (
          <Link className="summary-card-rules" to={rulesTo}>
            {rules}
          </Link>
        ) : (
          <span />
        )}
        <span className="summary-card-goto" aria-hidden="true">
          {TAB_LABEL[card.link.tab]} ›
        </span>
      </div>
    </div>
  )
}

// Bildirim ve kurallar: kritik bir alert'in şu an kaç alıcıya gideceği (gerçek gönderimle aynı hesap) ve bu sunucuda
// etkin alert kuralı sayısı. Ayrıntıları Ayarlar sekmesindedir. İzni olmayan satır çıkmaz; ikisi de yoksa kart yok.
function NotifySummary({ host, rules }: { host: Host; rules: { on: number; total: number; custom: number } | null }) {
  const { can } = useAuth()
  const canSeeNotify = can('notification.view')
  const [preview, setPreview] = useState<NotificationPreview | null>(null)

  useEffect(() => {
    if (!canSeeNotify) return
    let cancelled = false
    notificationsApi
      .preview(host.id, 'critical')
      .then((p) => !cancelled && setPreview(p))
      .catch(() => undefined)
    return () => {
      cancelled = true
    }
  }, [host.id, canSeeNotify])

  if (!preview && !rules) return null
  const r = preview ? recipientSummary(preview) : null
  return (
    <div className="card summary-facts">
      {r && (
        <div className="summary-fact">
          <span className="summary-fact-label">
            <BellRing size={14} strokeWidth={1.8} />
            Kritik bir alert
          </span>
          <span className="summary-fact-value">
            {r.total === 0 ? (
              <StatusBadge tone="warning">şu an kimseye gitmiyor</StatusBadge>
            ) : (
              <>
                {r.total} alıcıya gider <span className="muted">· {r.channels}</span>
              </>
            )}
            {preview?.in_maintenance && <span className="muted"> · sunucu bakımda, bakım bitince gider</span>}
          </span>
          <Link className="summary-fact-link" to={{ search: '?sekme=ayarlar&bolum=bildirimler' }}>
            Kime gider →
          </Link>
        </div>
      )}
      {rules && (
        <div className="summary-fact">
          <span className="summary-fact-label">
            <Activity size={14} strokeWidth={1.8} />
            Alert kuralları
          </span>
          <span className="summary-fact-value">
            {rules.on}/{rules.total} etkin
            {rules.custom > 0 && <span className="muted"> · {rules.custom} kural bu sunucuya özel</span>}
          </span>
          <Link className="summary-fact-link" to={{ search: '?sekme=ayarlar&bolum=kurallar' }}>
            Geçerli kurallar →
          </Link>
        </div>
      )}
    </div>
  )
}

// Üst satırdaki "Açık alert'ler" kutusu: seviye başına sayı (sıfır olan sönük), en eski açık alert'in yaşı ve onaylanmış
// alert sayısı. Kutunun tamamı Alert'ler sekmesine götürür; tek tek alert'ler ilgili konu kartında da işaretlidir.
function AlertsTile({ open, acknowledged, now }: { open: OpenAlerts; acknowledged: number; now: number }) {
  const t = alertTally(open.items, now)
  const tone = t.critical > 0 ? 'critical' : t.warning > 0 ? 'warning' : 'good'
  const hint = [t.oldest && `en eskisi ${t.oldest}`, acknowledged > 0 && `${acknowledged} onaylanmış`].filter(Boolean).join(' · ')
  return (
    <StatTile
      label="Açık alert’ler"
      icon={open.total > 0 ? BellRing : CircleCheck}
      tone={tone}
      small
      className="alerts-tile"
      to="?sekme=alertler"
      value={
        open.total === 0 ? (
          <span className="muted">Açık alert yok</span>
        ) : (
          <span className="tile-inline">
            {(['critical', 'warning', 'info'] as const).map((level) => (
              <span key={level} className={t[level] === 0 ? 'level-zero' : undefined}>
                <StatusBadge tone={t[level] === 0 ? 'neutral' : alertLevelTone(level)}>
                  {t[level]} {LEVEL_WORD[level]}
                </StatusBadge>
              </span>
            ))}
          </span>
        )
      }
      hint={hint || (open.total === 0 && acknowledged === 0 ? 'her şey yolunda' : undefined)}
    />
  )
}

const LEVEL_WORD = { critical: 'kritik', warning: 'uyarı', info: 'bilgi' } as const
