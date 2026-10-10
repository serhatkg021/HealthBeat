import { useEffect, useState, type ReactNode } from 'react'
import { Link } from 'react-router-dom'
import { alertsApi } from '../api/endpoints'
import type { Alert, AlertNotification, NotificationStatus } from '../types/api'
import { Drawer } from '../components/Drawer'
import { StatusBadge } from '../components/StatusBadge'
import {
  alertEventLabel,
  alertLevelLabel,
  alertLevelTone,
  alertMetricLabel,
  alertStatusLabel,
  alertSubjectText,
  channelLabel,
  notificationStatusLabel,
  notificationStatusTone,
} from '../labels'
import { alertReading } from './alertText'
import { attemptsText, deliveryTime, groupNotifications, groupSummary, recipientsText } from './alertNotifications'

const when = (iso?: string): string => (iso ? new Date(iso).toLocaleString() : '—')

function Row({ label, children }: { label: string; children: ReactNode }) {
  return (
    <div className="info-row">
      <dt>{label}</dt>
      <dd>{children}</dd>
    </div>
  )
}

// Alert detayı: alert'in bilgileri ve bildirim geçmişi (hangi olay için, hangi kanaldan, kimlere, ne zaman gitti ya da
// neden gitmedi). Alert listelerinden açılır; alert null ise kapalıdır.
export function AlertDetail({ alert, hostTitle, onClose }: { alert: Alert | null; hostTitle?: string; onClose: () => void }) {
  // Sonuç, ait olduğu alert'in kimliğiyle tutulur: başka bir alert açılınca eski sonuç görünmez (yükleniyor sayılır).
  const [result, setResult] = useState<{ alertId: string; items?: AlertNotification[]; error?: string } | null>(null)

  useEffect(() => {
    if (!alert) return
    let cancelled = false
    alertsApi
      .notifications(alert.id)
      .then((list) => !cancelled && setResult({ alertId: alert.id, items: list }))
      .catch(
        (err) => !cancelled && setResult({ alertId: alert.id, error: err instanceof Error ? err.message : 'bildirimler alınamadı' }),
      )
    return () => {
      cancelled = true
    }
  }, [alert])

  const current = alert && result?.alertId === alert.id ? result : null
  const items = current?.items ?? null
  const error = current?.error ?? ''

  return (
    <Drawer open={alert !== null} title="Alert ayrıntısı" onClose={onClose}>
      {alert && (
        <>
          <dl className="info-list single">
            <Row label="Sunucu">
              <Link to={`/hosts/${alert.host_id}`}>{hostTitle ?? alert.host_id}</Link>
            </Row>
            <Row label="Metrik">
              {alertMetricLabel(alert.alert_type)}
              {alert.subject && <span className="muted"> · {alertSubjectText(alert.alert_type, alert.subject)}</span>}
              {alertReading(alert) && <div className="muted tnum">{alertReading(alert)}</div>}
            </Row>
            <Row label="Seviye">
              <StatusBadge tone={alertLevelTone(alert.level)}>{alertLevelLabel(alert.level)}</StatusBadge>
            </Row>
            <Row label="Durum">{alertStatusLabel(alert.status)}</Row>
            <Row label="Oluşturulma">{when(alert.created_at)}</Row>
            {alert.acknowledged_at && <Row label="Onaylanma">{when(alert.acknowledged_at)}</Row>}
            {alert.resolved_at && <Row label="Çözülme">{when(alert.resolved_at)}</Row>}
          </dl>

          <h3 className="section-title">Bildirimler</h3>
          {error && <div className="error-banner">{error}</div>}
          {!error && items === null && <p className="muted">Yükleniyor…</p>}
          {items?.length === 0 && (
            <p className="muted">Bu alert için bildirim yazılmadı (sistem sahibi ya da uygun ek alıcı yoktu, ya da kanal kapalıydı).</p>
          )}
          {items && items.length > 0 && (
            <ol className="notification-timeline">
              {groupNotifications(items).map((g) => {
                const visible = g.items.some((n) => n.recipients && n.recipients.length > 0)
                return (
                  <li key={g.key}>
                    <div className="notification-head">
                      <time className="tnum" dateTime={g.created_at}>
                        {when(g.created_at)}
                      </time>
                      <span>{alertEventLabel(g.event)}</span>
                      <StatusBadge tone={alertLevelTone(g.level)}>{alertLevelLabel(g.level)}</StatusBadge>
                      <span className="muted">{groupSummary(g.items)}</span>
                    </div>
                    {visible && (
                      <ul className="delivery-list">
                        {g.items.map((n) => {
                          const done = deliveryTime(n)
                          const attempts = attemptsText(n)
                          return (
                            <li key={n.id}>
                              <ul className="meta-list">
                                <li>{recipientsText(n)}</li>
                                <li>{channelLabel(n.channel)}</li>
                                <li>
                                  <StatusBadge tone={notificationStatusTone(n.status)}>{notificationStatusLabel(n.status)}</StatusBadge>
                                </li>
                                <li className="tnum">
                                  {done.label}: {when(done.at)}
                                </li>
                                {attempts && <li>{attempts}</li>}
                              </ul>
                              {n.last_error && n.status !== 'sent' && <p className="notification-error">Son hata: {n.last_error}</p>}
                            </li>
                          )
                        })}
                      </ul>
                    )}
                    <details className="notification-content">
                      <summary>İçeriği göster</summary>
                      <pre>
                        {g.subject}
                        {'\n\n'}
                        {g.body}
                      </pre>
                    </details>
                  </li>
                )
              })}
            </ol>
          )}
        </>
      )}
    </Drawer>
  )
}

// NotificationBadge, alert listelerindeki "Bildirim" sütunudur: bildirimlerin toplu durumu ya da (hiç bildirim yoksa) "—".
// deferred: bir olayının bildirimi sunucu bakımdayken ertelendi; bakım bitince, alert hâlâ açıksa gönderilir.
export function NotificationBadge({ status, deferred }: { status?: NotificationStatus; deferred?: boolean }) {
  if (deferred) {
    return (
      <StatusBadge tone="neutral" title="Sunucu bakımda: bildirim bakım bitince, alert hâlâ açıksa gönderilir.">
        Bakım bitince
      </StatusBadge>
    )
  }
  if (!status) return <span className="muted" title="Bu alert için bildirim yazılmadı">—</span>
  return <StatusBadge tone={notificationStatusTone(status)}>{notificationStatusLabel(status)}</StatusBadge>
}
