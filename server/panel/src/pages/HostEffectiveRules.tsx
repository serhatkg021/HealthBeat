import { useEffect, useState } from 'react'
import { Link } from 'react-router-dom'
import { SlidersHorizontal } from 'lucide-react'
import { hostsApi } from '../api/endpoints'
import { StatusBadge } from '../components/StatusBadge'
import { useAuth } from '../auth/AuthContext'
import { alertRulesPath, notificationsPath } from '../navigation'
import { diskAlertSummary, effectiveRules, type EffectiveRule, type RuleSource } from './effectiveRules'

const SOURCE: Record<RuleSource, { label: string; tone: 'warning' | 'neutral' }> = {
  custom: { label: 'bu sunucuya özel', tone: 'warning' },
  inherited: { label: 'devralındı', tone: 'neutral' },
  none: { label: '—', tone: 'neutral' },
}

// Sunucu ayarlarındaki salt okunur "Geçerli alert kuralları": her kuralın şu an geçerli değeri ve nereden geldiği. Düzenleme
// Alert kuralları sayfasında, bu sunucunun kapsamında yapılır (organizasyon zincirini görebilenler orada değerin hangi
// organizasyondan geldiğini de görür).
export function HostEffectiveRules({ hostId }: { hostId: string }) {
  const { can } = useAuth()
  const [rules, setRules] = useState<EffectiveRule[] | null>(null)
  const [disks, setDisks] = useState<string | null>(null)
  const [error, setError] = useState<string | null>(null)

  useEffect(() => {
    let cancelled = false
    hostsApi
      .thresholds(hostId)
      .then((res) => !cancelled && setRules(effectiveRules(res)))
      .catch((err) => !cancelled && setError(err instanceof Error ? err.message : 'kurallar yüklenemedi'))
    hostsApi
      .diskAlerts(hostId)
      .then((d) => !cancelled && setDisks(diskAlertSummary(d)))
      .catch(() => undefined)
    return () => {
      cancelled = true
    }
  }, [hostId])

  return (
    <div className="card table-card">
      <div className="card-title-row">
        <h2 className="card-title">
          <SlidersHorizontal size={16} strokeWidth={1.75} />
          Geçerli alert kuralları
        </h2>
        <span className="row">
          {can('notification.view') && (
            <Link className="btn btn-sm" to={notificationsPath('kurallar', { kind: 'sunucu', id: hostId })}>
              Bildirim kuralları →
            </Link>
          )}
          <Link className="btn btn-sm" to={alertRulesPath({ kind: 'sunucu', id: hostId })}>
            Alert kurallarında düzenle →
          </Link>
        </span>
      </div>
      {error && <div className="error-banner">{error}</div>}
      {!rules && !error && <div className="muted">Yükleniyor…</div>}
      {rules && (
        <table className="stack">
          <thead>
            <tr>
              <th>Kural</th>
              <th>Geçerli değer</th>
              <th>Kaynak</th>
            </tr>
          </thead>
          <tbody>
            {rules.map((r) => (
              <tr key={r.key}>
                <td className="primary">{r.label}</td>
                <td className={r.value ? 'tnum' : 'muted'} data-label="Geçerli değer">
                  {r.value ?? 'Tanımlı değil — alert üretilmez'}
                </td>
                <td data-label="Kaynak">{r.source === 'none' ? <span className="muted">—</span> : <StatusBadge tone={SOURCE[r.source].tone}>{SOURCE[r.source].label}</StatusBadge>}</td>
              </tr>
            ))}
            {disks && (
              <tr>
                <td className="primary">Disk alert’i üreten mount’lar</td>
                <td data-label="Geçerli değer">{disks}</td>
                <td data-label="Kaynak">
                  <span className="muted">—</span>
                </td>
              </tr>
            )}
          </tbody>
        </table>
      )}
    </div>
  )
}
