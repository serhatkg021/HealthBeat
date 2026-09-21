import { useEffect, useState } from 'react'
import { Link } from 'react-router-dom'
import { meApi } from '../api/endpoints'
import type { Host } from '../types/api'
import { StatusBadge } from '../components/StatusBadge'
import { hostStatusLabel } from '../labels'
import { PageHeader } from '../components/PageHeader'
import { EmptyState } from '../components/EmptyState'
import { Server } from 'lucide-react'

export function MyHostsPage() {
  const [hosts, setHosts] = useState<Host[]>([])
  const [error, setError] = useState<string | null>(null)

  useEffect(() => {
    meApi
      .hosts()
      .then(setHosts)
      .catch((err) => setError(err instanceof Error ? err.message : 'sunucular yüklenemedi'))
  }, [])

  return (
    <div>
      <PageHeader title="Sunucularım" subtitle="Size atanmış sunucular" />
      {error && <div className="error-banner">{error}</div>}
      <div className="card table-card">
        <table className="stack">
          <thead>
            <tr>
              <th>Sunucu</th>
              <th>IP</th>
              <th>Mod</th>
              <th>Durum</th>
              <th>Son görülme</th>
            </tr>
          </thead>
          <tbody>
            {hosts.map((c) => (
              <tr key={c.id} className="clickable">
                <td className="primary">
                  <Link to={`/hosts/${c.id}`}>{c.title}</Link>
                </td>
                <td className="muted mono" data-label="IP">{c.ip}</td>
                <td className="muted" data-label="Mod">{c.mode}</td>
                <td data-label="Durum">
                  <StatusBadge tone={c.status === 'online' ? 'good' : 'critical'}>{hostStatusLabel(c.status)}</StatusBadge>
                </td>
                <td className="muted" data-label="Son görülme">{c.last_seen ? new Date(c.last_seen).toLocaleString() : '—'}</td>
              </tr>
            ))}
            {hosts.length === 0 && (
              <tr>
                <td colSpan={5} className="empty-cell">
                  <EmptyState icon={Server}>Size atanmış bir sunucu yok.</EmptyState>
                </td>
              </tr>
            )}
          </tbody>
        </table>
      </div>
    </div>
  )
}
