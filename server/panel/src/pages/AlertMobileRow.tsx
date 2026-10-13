import { Check } from 'lucide-react'
import type { Alert } from '../types/api'
import { StatusBadge } from '../components/StatusBadge'
import { alertLevelLabel, alertLevelTone, alertMetricLabel, alertStatusLabel, alertSubjectText } from '../labels'
import { alertReading } from './alertText'
import { agoText } from './cache'
import { NotificationBadge } from './AlertDetail'

// Alert listelerinin dar ekrandaki iki satırı (compact-mobile tablolar): üstte seviye, (genel listede) sunucu ve ne
// kadar önce açıldığı; altta ne olduğu ve okuması, gerekirse durum ve sorunlu bildirim rozeti, açık alert'te "Onayla".
// Satıra dokunmak ayrıntıyı açar. Masaüstünde gizlidir; tablonun ilk hücresi olarak verilir.
export function AlertMobileRow({
  alert: a,
  hostTitle,
  showStatus,
  canAcknowledge,
  onOpen,
  onAcknowledge,
  now,
}: {
  alert: Alert
  // Genel listede sunucunun adı; sunucunun kendi sekmesinde verilmez.
  hostTitle?: string
  showStatus: boolean
  canAcknowledge: boolean
  onOpen: () => void
  onAcknowledge: () => void
  now: number
}) {
  const what = [alertMetricLabel(a.alert_type), a.subject ? alertSubjectText(a.alert_type, a.subject) : null, alertReading(a) || null].filter(Boolean).join(' · ')
  const notifyIssue = a.notify_pending || (a.notification_status && a.notification_status !== 'sent')
  return (
    <td className="mobile-row">
      <div className="mobile-row-top">
        <button type="button" className="mobile-row-link mobile-row-title" onClick={onOpen} aria-label={`Ayrıntı: ${what}`}>
          <StatusBadge tone={alertLevelTone(a.level)}>{alertLevelLabel(a.level)}</StatusBadge>
          {hostTitle ?? what}
        </button>
        <span className="mobile-row-badges muted">{agoText(a.created_at, now)}</span>
      </div>
      <div className="mobile-row-sub">
        <span className="mobile-row-what">
          {[hostTitle ? what : null, showStatus ? alertStatusLabel(a.status) : null].filter(Boolean).join(' · ')}
        </span>
        <span className="mobile-row-badges">
          {notifyIssue && <NotificationBadge status={a.notification_status} deferred={a.notify_pending} />}
          {a.status === 'open' && canAcknowledge && (
            <button className="btn btn-sm" type="button" onClick={onAcknowledge}>
              <Check size={14} strokeWidth={2} />
              Onayla
            </button>
          )}
        </span>
      </div>
    </td>
  )
}
