import { alertReading } from './alertText'
import { useEffect, useState } from 'react'
import { Link } from 'react-router-dom'
import { useTab } from '../components/useTab'
import { alertsApi, hostsApi } from '../api/endpoints'
import { useAuth } from '../auth/AuthContext'
import { usePagedQuery } from '../api/usePagedQuery'
import type { Alert, AlertLevel, AlertStatus } from '../types/api'
import { StatusBadge } from '../components/StatusBadge'
import { alertLevelLabel, alertLevelTone, alertMetricLabel, alertStatusLabel, alertSubjectText } from '../labels'
import { PageHeader } from '../components/PageHeader'
import { EmptyState } from '../components/EmptyState'
import { SearchInput } from '../components/SearchInput'
import { Pagination } from '../components/Pagination'
import { BellOff, Check, Info } from 'lucide-react'
import { AlertDetail, NotificationBadge } from './AlertDetail'

const STATUS_TABS: { value: AlertStatus | ''; label: string }[] = [
  { value: 'open', label: 'Açık' },
  { value: 'acknowledged', label: 'Onaylanmış' },
  { value: 'resolved', label: 'Çözülmüş' },
  { value: '', label: 'Tümü' },
]

// Seviye süzgeci adreste tutulur (`?seviye=critical`): üst çubuktaki sayaçlar doğrudan o seviyeye bağlanır.
const LEVEL_PARAM = 'seviye'
const LEVEL_ALL = 'tumu'
const LEVEL_TABS: { value: AlertLevel | typeof LEVEL_ALL; label: string }[] = [
  { value: LEVEL_ALL, label: 'Tüm seviyeler' },
  { value: 'info', label: 'Bilgi' },
  { value: 'warning', label: 'Uyarı' },
  { value: 'critical', label: 'Kritik' },
]
const LEVEL_IDS = LEVEL_TABS.map((t) => t.value)

const PAGE_SIZE = 20

export function AlertsPage() {
  const { can } = useAuth()
  const [status, setStatus] = useState<AlertStatus | ''>('open')
  const [levelTab, setLevelTab] = useTab(LEVEL_IDS, LEVEL_ALL, LEVEL_PARAM)
  const level = levelTab === LEVEL_ALL ? undefined : (levelTab as AlertLevel)
  const [hostTitles, setHostTitles] = useState<Record<string, string>>({})
  const [selected, setSelected] = useState<Alert | null>(null)

  const {
    items: alerts,
    total,
    page,
    setPage,
    pageSize,
    setPageSize,
    q,
    setSearch,
    error,
    setError,
    reload,
  } = usePagedQuery((p) => alertsApi.list({ status: status || undefined, level, q: p.q, limit: p.limit, offset: p.offset }), PAGE_SIZE, [
    status,
    level,
  ])

  // Süzgeç değişince (üst çubuktaki sayaçtan da gelebilir) ilk sayfaya dönülür.
  useEffect(() => setPage(1), [status, level, setPage])

  // Görüntülenen sayfadaki host_id'ler için sunucu adı çöz — yalnızca henüz bilinmeyenler için.
  useEffect(() => {
    const missing = [...new Set(alerts.map((a) => a.host_id))].filter((id) => !(id in hostTitles))
    if (missing.length === 0) return
    let cancelled = false
    Promise.all(
      missing.map(async (id) => {
        try {
          const c = await hostsApi.get(id)
          return [id, c.title] as const
        } catch {
          return [id, id] as const
        }
      }),
    ).then((entries) => {
      if (!cancelled) setHostTitles((prev) => ({ ...prev, ...Object.fromEntries(entries) }))
    })
    return () => {
      cancelled = true
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [alerts])

  async function handleAcknowledge(id: string) {
    try {
      await alertsApi.acknowledge(id)
      reload()
    } catch (err) {
      setError(err instanceof Error ? err.message : 'alert onaylanamadı')
    }
  }

  // "Açık" filtresinde ikisi de her zaman boştur (henüz ne onaylandı ne çözüldü); "Onaylanmış"ta
  // çözülme her zaman boştur (onaylanan bir alert asla kendiliğinden çözülmez — bkz. HostAlerts).
  const showAcknowledged = status !== 'open'
  const showResolved = status === 'resolved' || status === ''
  const columnCount = 6 + Number(showAcknowledged) + Number(showResolved) + 1

  return (
    <div>
      <PageHeader title="Alert'ler" />
      {error && <div className="error-banner">{error}</div>}

      <div className="toolbar">
        <div className="segmented" role="group" aria-label="Durum süzgeci">
          {STATUS_TABS.map((tab) => (
            <button key={tab.value} type="button" aria-pressed={status === tab.value} onClick={() => setStatus(tab.value)}>
              {tab.label}
            </button>
          ))}
        </div>
        <div className="segmented" role="group" aria-label="Seviye süzgeci">
          {LEVEL_TABS.map((tab) => (
            <button key={tab.value} type="button" aria-pressed={levelTab === tab.value} onClick={() => setLevelTab(tab.value)}>
              {tab.label}
            </button>
          ))}
        </div>
        <SearchInput value={q} onChange={setSearch} placeholder="Sunucu, metrik ya da mount ara…" />
      </div>

      <div className="card table-card">
        <Pagination page={page} pageSize={pageSize} total={total} onPageChange={setPage} onPageSizeChange={setPageSize} />
        <table className="stack">
          <thead>
            <tr>
              <th>Sunucu</th>
              <th>Metrik</th>
              <th>Seviye</th>
              <th>Durum</th>
              <th>Oluşturulma</th>
              {showAcknowledged && <th>Onaylanma</th>}
              {showResolved && <th>Çözülme</th>}
              <th>Bildirim</th>
              <th className="actions" />
            </tr>
          </thead>
          <tbody>
            {alerts.map((a) => (
              <tr key={a.id}>
                <td className="primary">
                  <Link to={`/hosts/${a.host_id}`}>{hostTitles[a.host_id] ?? a.host_id}</Link>
                </td>
                <td data-label="Metrik">
                  {alertMetricLabel(a.alert_type)}
                  {a.subject && <span className="muted"> · {alertSubjectText(a.alert_type, a.subject)}</span>}
                  {alertReading(a) && <div className="muted tnum">{alertReading(a)}</div>}
                </td>
                <td data-label="Seviye">
                  <StatusBadge tone={alertLevelTone(a.level)}>{alertLevelLabel(a.level)}</StatusBadge>
                </td>
                <td className="muted" data-label="Durum">{alertStatusLabel(a.status)}</td>
                <td className="muted" data-label="Oluşturulma">{new Date(a.created_at).toLocaleString()}</td>
                {showAcknowledged && (
                  <td className="muted" data-label="Onaylanma">{a.acknowledged_at ? new Date(a.acknowledged_at).toLocaleString() : '—'}</td>
                )}
                {showResolved && (
                  <td className="muted" data-label="Çözülme">{a.resolved_at ? new Date(a.resolved_at).toLocaleString() : '—'}</td>
                )}
                <td data-label="Bildirim">
                  <NotificationBadge status={a.notification_status} deferred={a.notify_pending} />
                </td>
                <td className="actions">
                  <button className="btn btn-sm btn-ghost" onClick={() => setSelected(a)}>
                    <Info size={14} strokeWidth={2} />
                    Ayrıntı
                  </button>
                  {a.status === 'open' && can('alert.acknowledge') && (
                    <button className="btn btn-sm" onClick={() => handleAcknowledge(a.id)}>
                      <Check size={14} strokeWidth={2} />
                      Onayla
                    </button>
                  )}
                </td>
              </tr>
            ))}
            {alerts.length === 0 && (
              <tr>
                <td colSpan={columnCount} className="empty-cell">
                  <EmptyState icon={BellOff}>Alert bulunamadı.</EmptyState>
                </td>
              </tr>
            )}
          </tbody>
        </table>
      </div>

      <AlertDetail
        alert={selected}
        hostTitle={selected ? hostTitles[selected.host_id] : undefined}
        onClose={() => setSelected(null)}
      />
    </div>
  )
}
