import { useEffect, useState, type ReactNode } from 'react'
import { BellOff, Send, Server, Wrench } from 'lucide-react'
import { notificationsApi } from '../api/endpoints'
import type { AlertLevel, NotificationPreview, OverviewHost, PreviewRecipient } from '../types/api'
import { EmptyState } from '../components/EmptyState'
import { InfoTip } from '../components/InfoTip'
import { HostPicker } from '../components/ScopePickers'
import { StatusBadge } from '../components/StatusBadge'
import { useAuth } from '../auth/AuthContext'
import { alertLevelLabel } from '../labels'
import { LEVEL_CHOICES, channelLabel } from './notificationRules'
import { alsoLabel, nobodyNotified, ownerCounts, skipReasonLabel, sourceLabel } from './notificationPreview'

// "Bu alert kime gider?": bir sunucu ve seviye seçilince bildirimin şu an kime gideceği, kime neden gitmeyeceği. Alıcıları
// server gerçek gönderimin kurallarıyla hesaplar (sahipler + organizasyon zinciri + sunucu kuralları).

// NotificationPreviewCard, Bildirim kuralları sekmesindeki önizlemedir: sunucu seçilir. Sekmede bir sunucu seçiliyse
// önizleme onunla başlar ve onu izler; kullanıcı başka bir sunucu da seçebilir.
export function NotificationPreviewCard({
  hosts,
  orgNames,
  scopeHostId,
}: {
  hosts: OverviewHost[]
  orgNames: Map<string, string>
  scopeHostId?: string
}) {
  // Kullanıcının seçimi, seçildiği andaki sekme sunucusuyla birlikte tutulur: sekmede sunucu değişince önizleme onu izler.
  const [picked, setPicked] = useState<{ scope?: string; id: string } | null>(null)
  const hostId = picked && picked.scope === scopeHostId ? picked.id : (scopeHostId ?? picked?.id ?? '')
  return (
    <PreviewCard
      hostId={hostId}
      hostTitle={hosts.find((h) => h.id === hostId)?.title}
      picker={
        <HostPicker idPrefix="preview" hosts={hosts} orgNames={orgNames} value={hostId || undefined} onChange={(id) => setPicked({ scope: scopeHostId, id })} />
      }
    />
  )
}

// HostNotificationPreview, sunucu sayfasının Ayarlar → Bildirimler bölümündeki önizlemedir: sunucu sabittir.
export function HostNotificationPreview({ hostId, hostTitle, footer }: { hostId: string; hostTitle: string; footer?: ReactNode }) {
  return <PreviewCard hostId={hostId} hostTitle={hostTitle} footer={footer} />
}

function PreviewCard({ hostId, hostTitle, picker, footer }: { hostId: string; hostTitle?: string; picker?: ReactNode; footer?: ReactNode }) {
  const { can } = useAuth()
  const [level, setLevel] = useState<AlertLevel>('critical')
  const [result, setResult] = useState<{ key: string; preview?: NotificationPreview; error?: string } | null>(null)
  const key = `${hostId}|${level}`

  useEffect(() => {
    if (!hostId) return
    let cancelled = false
    notificationsApi
      .preview(hostId, level)
      .then((preview) => !cancelled && setResult({ key, preview }))
      .catch((err) => !cancelled && setResult({ key, error: err instanceof Error ? err.message : 'önizleme alınamadı' }))
    return () => {
      cancelled = true
    }
  }, [hostId, level, key])

  const current = result?.key === key ? result : null

  return (
    <section className="card">
      <h2 className="card-title">
        <Send size={16} strokeWidth={1.75} />
        Bu alert kime gider?
        <InfoTip label="Bu alert kime gider?">
          {picker ? 'Seçilen sunucuda' : 'Bu sunucuda'} bu seviyede bir alert açılsa bildirimin şu an kime gideceği: sistem
          sahipleri, sunucunun organizasyonu ile üst organizasyonların kuralları ve sunucunun kendi kuralları birleştirilir.
          Aynı adres tek ileti alır.
        </InfoTip>
      </h2>
      <div className="toolbar" style={{ justifyContent: 'flex-start' }}>
        {picker}
        <div className="segmented" role="group" aria-label="Seviye">
          {LEVEL_CHOICES.map((l) => (
            <button key={l} type="button" aria-pressed={level === l} onClick={() => setLevel(l)}>
              {alertLevelLabel(l)}
            </button>
          ))}
        </div>
      </div>

      {!hostId ? (
        <EmptyState icon={Server}>Önizleme için bir sunucu seçin.</EmptyState>
      ) : current?.error ? (
        <div className="error-banner">{current.error}</div>
      ) : !current?.preview ? (
        <p className="muted">Yükleniyor…</p>
      ) : (
        <PreviewResult preview={current.preview} hostTitle={hostTitle} canManageOwners={can('settings.view')} />
      )}
      {footer && <div className="preview-footer">{footer}</div>}
    </section>
  )
}

function PreviewResult({ preview, hostTitle, canManageOwners }: { preview: NotificationPreview; hostTitle?: string; canManageOwners: boolean }) {
  const owners = ownerCounts(preview.owners_summary)
  return (
    <>
      {preview.in_maintenance && (
        <div className="maintenance-strip" role="status">
          <Wrench size={15} strokeWidth={1.9} />
          <strong>Bakımda</strong>
          <span>Sunucu şu an bakımda: bildirim bakım bitince gider.</span>
        </div>
      )}

      {nobodyNotified(preview) ? (
        <EmptyState icon={BellOff}>
          Bu alert kimseye gitmez.
          <div className="muted">
            {canManageOwners
              ? 'Bildirim → Sistem sahipleri’nden bir sahip ya da yukarıdan bu kapsama bir kural ekleyin.'
              : 'Bu seviyede sistem sahibi ya da kural yok; yöneticiniz bir kural ekleyebilir.'}
          </div>
        </EmptyState>
      ) : (
        <table className="stack">
          <thead>
            <tr>
              <th>Alıcı</th>
              <th>Nereden</th>
              <th>Kanal</th>
            </tr>
          </thead>
          <tbody>
            {owners.sent && (
              <tr>
                <td className="primary">
                  Sistem sahipleri
                  <div className="muted preview-owners-note">Adları yalnızca sistem ayarlarını görebilenlere gösterilir.</div>
                </td>
                <td className="muted" data-label="Nereden">
                  Sistem sahibi
                </td>
                <td data-label="Kanal">{owners.sent}</td>
              </tr>
            )}
            {preview.recipients.map((r) => (
              <RecipientRow key={`${r.channel}|${r.address}`} r={r} hostTitle={hostTitle} />
            ))}
          </tbody>
        </table>
      )}

      {(preview.skipped.length > 0 || owners.skipped.length > 0) && (
        <>
          <h3 className="section-title preview-skipped-title">
            <BellOff size={15} strokeWidth={1.75} />
            Gitmeyecekler
          </h3>
          <table className="stack">
            <thead>
              <tr>
                <th>Alıcı</th>
                <th>Nereden</th>
                <th>Kanal</th>
                <th>Neden</th>
              </tr>
            </thead>
            <tbody>
              {owners.skipped.map((text) => (
                <tr key={text}>
                  <td className="primary">Sistem sahipleri</td>
                  <td className="muted" data-label="Nereden">
                    Sistem sahibi
                  </td>
                  <td colSpan={2} data-label="Kanal">
                    {text}
                  </td>
                </tr>
              ))}
              {preview.skipped.map((r) => (
                <RecipientRow key={`${r.channel}|${r.address}`} r={r} hostTitle={hostTitle} reason />
              ))}
            </tbody>
          </table>
        </>
      )}
    </>
  )
}

function RecipientRow({ r, hostTitle, reason }: { r: PreviewRecipient; hostTitle?: string; reason?: boolean }) {
  const also = alsoLabel(r.also, hostTitle)
  return (
    <tr>
      <td className="primary">
        {r.name || '—'}
        <div className="muted mono">{r.address}</div>
      </td>
      <td className="muted" data-label="Nereden">
        <div>
          {sourceLabel(r.source, hostTitle)}
          {also && <div className="preview-also">{also}</div>}
        </div>
      </td>
      <td data-label="Kanal">{channelLabel(r.channel)}</td>
      {reason && (
        <td data-label="Neden">
          <StatusBadge tone="warning">{skipReasonLabel(r.reason)}</StatusBadge>
        </td>
      )}
    </tr>
  )
}
