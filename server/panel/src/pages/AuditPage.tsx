import { useEffect, useRef, useState } from 'react'
import { auditApi } from '../api/endpoints'
import type { AuditLogEntry } from '../types/api'
import { PageHeader } from '../components/PageHeader'
import { EmptyState } from '../components/EmptyState'
import { ScrollText } from 'lucide-react'

// Filtreler eylem önekleridir ve server tarafından eşlenir (starts_with).
const CATEGORIES: { value: string; label: string }[] = [
  { value: '', label: 'Tümü' },
  { value: 'auth.', label: 'Oturum' },
  { value: 'organization.', label: 'Organizasyon' },
  { value: 'host.', label: 'Sunucu' },
  { value: 'user.', label: 'Kullanıcı' },
  { value: 'threshold.', label: 'Eşik' },
  { value: 'alert.', label: 'Alert' },
]

const PAGE_SIZE = 50

export function AuditPage() {
  const [category, setCategory] = useState('')
  const [entries, setEntries] = useState<AuditLogEntry[]>([])
  const [nextCursor, setNextCursor] = useState<string | null>(null)
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState<string | null>(null)

  // Yalnızca en yeni istek durumu güncelleyebilir; böylece filtreleri hızlıca değiştirmek daha
  // yavaş, eski bir yanıtın mevcut listenin üzerine yazmasına izin vermez.
  const latest = useRef(0)

  function load(cursor?: string) {
    const id = ++latest.current
    setLoading(true)
    setError(null)
    auditApi
      .list({ action: category || undefined, cursor, limit: PAGE_SIZE })
      .then((page) => {
        if (id !== latest.current) return
        setEntries((prev) => (cursor ? [...prev, ...page.items] : page.items))
        setNextCursor(page.next_cursor)
      })
      .catch((err) => {
        if (id !== latest.current) return
        setError(err instanceof Error ? err.message : 'denetim kaydı yüklenemedi')
      })
      .finally(() => {
        if (id === latest.current) setLoading(false)
      })
  }

  // eslint-disable-next-line react-hooks/exhaustive-deps
  useEffect(() => load(), [category])

  return (
    <div>
      <PageHeader title="Denetim Kaydı" subtitle="Panelde ve API'de yapılan yönetim işlemleri" />
      {error && <div className="error-banner">{error}</div>}

      <div className="toolbar">
        <div className="segmented" role="group" aria-label="Kategori süzgeci">
          {CATEGORIES.map((c) => (
            <button key={c.value} type="button" aria-pressed={category === c.value} onClick={() => setCategory(c.value)}>
              {c.label}
            </button>
          ))}
        </div>
      </div>

      <div className="card table-card">
        <table className="stack">
          <thead>
            <tr>
              <th>Zaman</th>
              <th>Kullanıcı</th>
              <th>İşlem</th>
              <th>Hedef</th>
              <th>Ayrıntı</th>
            </tr>
          </thead>
          <tbody>
            {entries.map((e) => (
              <tr key={e.id}>
                <td className="muted nowrap" data-label="Zaman">
                  {new Date(e.created_at).toLocaleString()}
                </td>
                <td className="primary">{e.actor_email}</td>
                <td data-label="İşlem">
                  <code>{e.action}</code>
                </td>
                <td className="muted audit-target" data-label="Hedef">
                  {e.target_type}
                  {e.target_id ? ` · ${e.target_id}` : ''}
                </td>
                <td className="muted mono audit-details" data-label="Ayrıntı">
                  {e.details ? JSON.stringify(e.details) : ''}
                </td>
              </tr>
            ))}
            {entries.length === 0 && !loading && (
              <tr>
                <td colSpan={5} className="empty-cell">
                  <EmptyState icon={ScrollText}>Kayıt bulunamadı.</EmptyState>
                </td>
              </tr>
            )}
          </tbody>
        </table>
      </div>

      {nextCursor && (
        <div style={{ marginTop: 14 }}>
          <button className="btn" disabled={loading} onClick={() => load(nextCursor)}>
            {loading ? 'Yükleniyor…' : 'Daha eski kayıtlar'}
          </button>
        </div>
      )}
    </div>
  )
}
