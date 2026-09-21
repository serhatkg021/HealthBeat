import { alertReading } from './alertText'
import { useEffect, useState } from 'react'
import { Link } from 'react-router-dom'
import { alertsApi, hostsApi } from '../api/endpoints'
import { usePagedQuery } from '../api/usePagedQuery'
import type { AlertStatus } from '../types/api'
import { StatusBadge } from '../components/StatusBadge'
import { alertLevelLabel, alertLevelTone, alertMetricLabel, alertStatusLabel } from '../labels'
import { PageHeader } from '../components/PageHeader'
import { EmptyState } from '../components/EmptyState'
import { SearchInput } from '../components/SearchInput'
import { Pagination } from '../components/Pagination'
import { BellOff, Check } from 'lucide-react'

const STATUS_TABS: { value: AlertStatus | ''; label: string }[] = [
  { value: 'open', label: 'Açık' },
  { value: 'acknowledged', label: 'Onaylanmış' },
  { value: 'resolved', label: 'Çözülmüş' },
  { value: '', label: 'Tümü' },
]

const PAGE_SIZE = 20

export function AlertsPage() {
  const [status, setStatus] = useState<AlertStatus | ''>('open')
  const [hostTitles, setHostTitles] = useState<Record<string, string>>({})

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
  } = usePagedQuery((p) => alertsApi.list({ status: status || undefined, q: p.q, limit: p.limit, offset: p.offset }), PAGE_SIZE, [
    status,
  ])

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
  const columnCount = 5 + Number(showAcknowledged) + Number(showResolved) + 1

  return (
    <div>
      <PageHeader title="Alert'ler" subtitle="Eşik aşımları ve çevrimdışı sunucular" />
      {error && <div className="error-banner">{error}</div>}

      <div className="toolbar">
        <div className="segmented" role="group" aria-label="Durum süzgeci">
          {STATUS_TABS.map((tab) => (
            <button key={tab.value} type="button" aria-pressed={status === tab.value} onClick={() => setStatus(tab.value)}>
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
                  {a.subject && <span className="muted"> · {a.subject}</span>}
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
                <td className="actions">
                  {a.status === 'open' && (
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
    </div>
  )
}
