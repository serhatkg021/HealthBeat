import { useEffect, useRef, useState } from 'react'
import { CheckCircle2, Clock, Hourglass, Inbox, RefreshCw, RotateCw, XCircle } from 'lucide-react'
import { systemApi } from '../api/endpoints'
import type { QueueItem, QueueKind, QueueSummary } from '../types/api'
import { EmptyState } from '../components/EmptyState'
import { StatTile } from '../components/StatTile'
import { StatusBadge } from '../components/StatusBadge'
import { UsageBar } from '../components/UsageBar'
import { useNow } from '../components/useNow'
import { alertEventLabel, alertLevelLabel, channelLabel } from '../labels'
import {
  QUEUE_FILTERS,
  QUEUE_KINDS,
  activeCount,
  attemptsText,
  itemTime,
  oldestAgeText,
  poolPct,
  queueKindLabel,
  queueStatus,
  type QueueFilter,
} from './queue'

const PAGE_SIZE = 50

// Kuyruk Durumu: bildirim kuyruğunun (alert bildirimleri ve hesap e-postaları) anlık özeti ve satırları. Salt okunur;
// ileti gövdeleri gösterilmez. Kuyruk bir veritabanı tablosudur, sabit bir kapasitesi yoktur: "doluluk" olarak satır
// başına deneme sayısı ve veritabanı bağlantı havuzunun kullanımı gösterilir.
export function SystemQueue() {
  const [summary, setSummary] = useState<QueueSummary | null>(null)
  const [filter, setFilter] = useState<QueueFilter>('active')
  const [kind, setKind] = useState<QueueKind | ''>('')
  const [items, setItems] = useState<QueueItem[]>([])
  const [nextCursor, setNextCursor] = useState<string | null>(null)
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const now = useNow(1000)

  // Yalnızca en yeni istek durumu güncelleyebilir (bkz. AuditPage).
  const latest = useRef(0)

  function load(cursor?: string) {
    const id = ++latest.current
    setLoading(true)
    setError(null)
    Promise.all([cursor ? null : systemApi.queue(), systemApi.queueItems({ status: filter || undefined, kind: kind || undefined, cursor, limit: PAGE_SIZE })])
      .then(([sum, page]) => {
        if (id !== latest.current) return
        if (sum) setSummary(sum)
        setItems((prev) => (cursor ? [...prev, ...page.items] : page.items))
        setNextCursor(page.next_cursor)
      })
      .catch((err) => {
        if (id !== latest.current) return
        setError(err instanceof Error ? err.message : 'kuyruk durumu yüklenemedi')
      })
      .finally(() => {
        if (id === latest.current) setLoading(false)
      })
  }

  // eslint-disable-next-line react-hooks/exhaustive-deps
  useEffect(() => load(), [filter, kind])

  const maxAttempts = summary?.max_attempts ?? 0

  return (
    <div>
      {error && <div className="error-banner">{error}</div>}

      {summary && (
        <>
          <div className="stat-grid">
            <StatTile label="Kuyrukta" value={activeCount(summary)} icon={Inbox} tone="accent" hint="teslim bekleyen" />
            <StatTile label="Yeniden denenecek" value={summary.retrying} icon={RotateCw} tone={summary.retrying > 0 ? 'warning' : undefined} hint="en az bir denemesi başarısız" />
            <StatTile label="En eski bekleyen" value={oldestAgeText(summary.oldest_active_at, now)} icon={Hourglass} small hint="kuyrukta geçen süre" />
            <StatTile label="Gönderilen" value={summary.sent} icon={CheckCircle2} tone="good" hint="kayıtlı" />
            <StatTile label="Vazgeçilen" value={summary.failed} icon={XCircle} tone={summary.failed > 0 ? 'critical' : undefined} hint="kayıtlı" />
          </div>

          <div className="card queue-pool">
            <div className="queue-pool-head">
              <span className="queue-pool-title">Veritabanı bağlantı havuzu</span>
              <span className="queue-pool-value">
                {summary.db_pool.acquired} / {summary.db_pool.max} kullanılıyor
              </span>
            </div>
            <UsageBar pct={poolPct(summary.db_pool)} label="Veritabanı bağlantı havuzu" size="sm" />
            <p className="form-hint queue-pool-hint">
              {summary.db_pool.total} bağlantı açık ({summary.db_pool.idle} boşta); en çok {summary.db_pool.max} (DB_MAX_CONNS). Kuyruk bir veritabanı
              tablosudur, sabit bir kapasitesi yoktur. Bir satır en çok {summary.max_attempts} kez denenir; gönderilmiş ve vazgeçilmiş satırlar{' '}
              {summary.retain_finished_days} gün sonra silinir (alert bildirimleri alert’leri durdukça kalır).
            </p>
          </div>
        </>
      )}

      <div className="toolbar">
        <div className="segmented" role="group" aria-label="Durum süzgeci">
          {QUEUE_FILTERS.map((f) => (
            <button key={f.value} type="button" aria-pressed={filter === f.value} onClick={() => setFilter(f.value)}>
              {f.label}
            </button>
          ))}
        </div>
        <div className="toolbar-group">
          <select value={kind} aria-label="Tür süzgeci" onChange={(e) => setKind(e.target.value as QueueKind | '')}>
            {QUEUE_KINDS.map((k) => (
              <option key={k.value} value={k.value}>
                {k.label}
              </option>
            ))}
          </select>
          <button className="btn" type="button" disabled={loading} onClick={() => load()}>
            <RefreshCw size={15} strokeWidth={1.9} />
            Yenile
          </button>
        </div>
      </div>

      <div className="card table-card">
        <table className="stack">
          <thead>
            <tr>
              <th>Oluşturulma</th>
              <th>Tür</th>
              <th>Alıcı</th>
              <th>Konu</th>
              <th>Durum</th>
              <th>Deneme</th>
              <th>Son hata</th>
            </tr>
          </thead>
          <tbody>
            {items.map((i) => {
              const status = queueStatus(i.status)
              const time = itemTime(i, now)
              return (
                <tr key={i.id}>
                  <td className="muted nowrap" data-label="Oluşturulma">
                    {new Date(i.created_at).toLocaleString()}
                  </td>
                  <td data-label="Tür">
                    {queueKindLabel(i.kind)}
                    <div className="cell-sub nowrap">
                      {i.alert_event && i.alert_level ? `${alertEventLabel(i.alert_event)} · ${alertLevelLabel(i.alert_level)} · ` : ''}
                      {channelLabel(i.channel)}
                    </div>
                  </td>
                  <td data-label="Alıcı">{i.recipients.join(', ')}</td>
                  <td className="primary">{i.subject}</td>
                  <td data-label="Durum">
                    <StatusBadge tone={status.tone}>{status.label}</StatusBadge>
                    <div className="cell-sub nowrap" title={time.at ? new Date(time.at).toLocaleString() : undefined}>
                      {time.at && i.status !== 'pending' && i.status !== 'retrying' ? new Date(time.at).toLocaleString() : time.label}
                    </div>
                  </td>
                  <td className="nowrap" data-label="Deneme">
                    {attemptsText(i.attempts, maxAttempts)}
                  </td>
                  <td className="muted mono queue-error" data-label="Son hata">
                    {i.last_error}
                  </td>
                </tr>
              )
            })}
            {items.length === 0 && !loading && (
              <tr>
                <td colSpan={7} className="empty-cell">
                  <EmptyState icon={filter === 'active' ? CheckCircle2 : Clock}>
                    {filter === 'active' ? 'Kuyruk boş: bekleyen bildirim yok.' : 'Bu süzgece uyan satır yok.'}
                  </EmptyState>
                </td>
              </tr>
            )}
          </tbody>
        </table>
      </div>

      {nextCursor && (
        <div style={{ marginTop: 14 }}>
          <button className="btn" disabled={loading} onClick={() => load(nextCursor)}>
            {loading ? 'Yükleniyor…' : 'Daha eski satırlar'}
          </button>
        </div>
      )}
    </div>
  )
}
