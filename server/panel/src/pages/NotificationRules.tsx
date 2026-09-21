import { useCallback, useEffect, useState, type FormEvent } from 'react'
import { BellRing, Plus, Trash2 } from 'lucide-react'
import { notificationsApi, type RouteScope } from '../api/endpoints'
import type { AlertLevel, NotificationChannel, NotificationRoute, RecipientCandidate } from '../types/api'
import { StatusBadge } from '../components/StatusBadge'
import { EmptyState } from '../components/EmptyState'
import { alertLevelLabel } from '../labels'
import { CHANNELS, LEVEL_CHOICES, candidateKey, candidateLabel, candidatesForNewRoute, describeCoverage } from './notificationRules'

// Bir kapsamın (organizasyon ya da sunucu) bildirim kuralları. Kural yoksa varsayılan alıcılar kullanılır; kural
// varsa yalnızca kurallardaki alıcılar bilgilendirilir. Sunucu kuralı organizasyon kurallarını, alt organizasyonun kuralı
// üst şirketinkini o kapsamda geçersiz kılar.
export function NotificationRules({ scope, canEdit }: { scope: RouteScope; canEdit: boolean }) {
  const isHost = 'hostId' in scope
  const scopeId = isHost ? scope.hostId : scope.organizationId
  const [routes, setRoutes] = useState<NotificationRoute[] | null>(null)
  const [candidates, setCandidates] = useState<RecipientCandidate[]>([])
  const [error, setError] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)
  const [recipient, setRecipient] = useState('')
  const [channel, setChannel] = useState<NotificationChannel>('email')
  const [minLevel, setMinLevel] = useState<AlertLevel>('warning')

  const load = useCallback(() => {
    const s: RouteScope = isHost ? { hostId: scopeId } : { organizationId: scopeId }
    notificationsApi
      .list(s)
      .then(setRoutes)
      .catch((err) => setError(err instanceof Error ? err.message : 'bildirim kuralları yüklenemedi'))
    if (canEdit) notificationsApi.candidates(s).then(setCandidates).catch(() => undefined)
  }, [isHost, scopeId, canEdit])

  useEffect(load, [load])

  async function run(action: () => Promise<unknown>, failure: string) {
    setBusy(true)
    setError(null)
    try {
      await action()
      load()
    } catch (err) {
      setError(err instanceof Error ? err.message : failure)
    } finally {
      setBusy(false)
    }
  }

  async function handleAdd(e: FormEvent) {
    e.preventDefault()
    const chosen = candidates.find((c) => candidateKey(c) === recipient)
    if (!chosen) return
    await run(
      () =>
        notificationsApi.create(scope, {
          ...(chosen.user_id ? { user_id: chosen.user_id } : { contact_id: chosen.contact_id }),
          channel,
          min_level: minLevel,
        }),
      'kural eklenemedi',
    )
    setRecipient('')
  }

  const list = routes ?? []
  const available = candidatesForNewRoute(candidates, list, channel)

  return (
    <div className="card form-card rule-card">
      <h2 className="card-title">
        <BellRing size={16} strokeWidth={1.75} />
        Bildirim kuralları
      </h2>
      <p className="card-desc">{describeCoverage(isHost ? 'host' : 'organization', list.length)}</p>
      {error && <div className="error-banner">{error}</div>}

      {routes !== null && list.length === 0 ? (
        <EmptyState icon={BellRing}>Kural yok — varsayılan alıcılar bilgilendirilir.</EmptyState>
      ) : (
        <table className="stack">
          <thead>
            <tr>
              <th>Alıcı</th>
              <th>Kanal</th>
              <th>En düşük seviye</th>
              {canEdit && <th className="actions" />}
            </tr>
          </thead>
          <tbody>
            {list.map((r) => (
              <tr key={r.id}>
                <td className="primary">
                  {r.recipient_name ?? '—'}
                  <div className="muted mono">{r.recipient_target ?? ''}</div>
                </td>
                <td className="muted" data-label="Kanal">{CHANNELS.find((c) => c.id === r.channel)?.label ?? r.channel}</td>
                <td data-label="En düşük seviye">
                  {canEdit ? (
                    <select
                      aria-label={`${r.recipient_name ?? 'Alıcı'} için en düşük seviye`}
                      value={r.min_level}
                      disabled={busy}
                      onChange={(e) => run(() => notificationsApi.update(r.id, { min_level: e.target.value as AlertLevel }), 'kural güncellenemedi')}
                    >
                      {LEVEL_CHOICES.map((l) => (
                        <option key={l} value={l}>
                          {alertLevelLabel(l)} ve üstü
                        </option>
                      ))}
                    </select>
                  ) : (
                    <StatusBadge tone="neutral">{alertLevelLabel(r.min_level)} ve üstü</StatusBadge>
                  )}
                </td>
                {canEdit && (
                  <td className="actions">
                    <button className="btn btn-sm btn-danger" disabled={busy} onClick={() => run(() => notificationsApi.remove(r.id), 'kural silinemedi')}>
                      <Trash2 size={14} strokeWidth={1.9} />
                      Sil
                    </button>
                  </td>
                )}
              </tr>
            ))}
          </tbody>
        </table>
      )}

      {canEdit && (
        <form onSubmit={handleAdd} className="rule-form">
          <div className="form-row">
            <label htmlFor="rule-recipient">Alıcı</label>
            <select id="rule-recipient" value={recipient} onChange={(e) => setRecipient(e.target.value)} required>
              <option value="">Seçin…</option>
              {available.map((c) => (
                <option key={candidateKey(c)} value={candidateKey(c)}>
                  {candidateLabel(c)}
                </option>
              ))}
            </select>
            <p className="form-hint">
              Yalnızca bu kapsamdaki yöneticiler{isHost ? ', bu sunucuya atanmış operatörler' : ''} ve organizasyonun iletişim kişileri seçilebilir.
            </p>
          </div>
          <div className="form-row">
            <label htmlFor="rule-channel">Kanal</label>
            <select id="rule-channel" value={channel} onChange={(e) => setChannel(e.target.value as NotificationChannel)}>
              {CHANNELS.map((c) => (
                <option key={c.id} value={c.id} disabled={!c.implemented}>
                  {c.label}
                  {c.implemented ? '' : ' (yakında)'}
                </option>
              ))}
            </select>
          </div>
          <div className="form-row">
            <label htmlFor="rule-level">En düşük seviye</label>
            <select id="rule-level" value={minLevel} onChange={(e) => setMinLevel(e.target.value as AlertLevel)}>
              {LEVEL_CHOICES.map((l) => (
                <option key={l} value={l}>
                  {alertLevelLabel(l)} ve üstü
                </option>
              ))}
            </select>
          </div>
          <button className="btn btn-primary" type="submit" disabled={busy || recipient === ''}>
            <Plus size={15} strokeWidth={1.9} />
            Kural ekle
          </button>
        </form>
      )}
    </div>
  )
}

