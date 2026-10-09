import { useEffect, useState } from 'react'
import { Link } from 'react-router-dom'
import { CircleAlert, Eye, ListChecks, Play, Plus, Trash2, TriangleAlert } from 'lucide-react'
import { hostsApi } from '../api/endpoints'
import { useAuth } from '../auth/AuthContext'
import { EmptyState } from '../components/EmptyState'
import { SearchInput } from '../components/SearchInput'
import { useNow } from '../components/useNow'
import { StatTile } from '../components/StatTile'
import { StatusBadge } from '../components/StatusBadge'
import { alertRulesPath } from '../navigation'
import type { Host, HostService, HostStatusRuleView } from '../types/api'
import { formatUptime, healthEmptyText } from './inventory'
import { enabledLabel, filterServices, notReported, parseServiceName, serviceCounts, serviceState, toggleWatched } from './services'

// "Servisler" sekmesindeki sistem servisleri (systemd, protokol 4): agent'ın son bildirdiği liste ve izlenen servis
// seçimi. İzlenen bir servis çalışmazsa "Servis çalışmıyor" durum kuralına göre alert açılır; seçim sunucu bazındadır.
export function SystemServices({ host }: { host: Host }) {
  const { can } = useAuth()
  const canEdit = can('host.update')
  const [services, setServices] = useState<HostService[] | null>(null)
  const now = useNow(60_000)
  const [watched, setWatched] = useState<string[]>([])
  const [ruleOff, setRuleOff] = useState(false)
  const [q, setQ] = useState('')
  const [problemsOnly, setProblemsOnly] = useState(false)
  const [watchedOnly, setWatchedOnly] = useState(false)
  const [typed, setTyped] = useState('')
  const [typedError, setTypedError] = useState<string | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [saving, setSaving] = useState(false)

  useEffect(() => {
    let cancelled = false
    hostsApi
      .services(host.id)
      .then((res) => {
        if (cancelled) return
        setServices(res.services)
        setWatched(res.watched)
      })
      .catch((err) => !cancelled && setError(err instanceof Error ? err.message : 'servisler yüklenemedi'))
    // Kural kapalıysa izlemek alert üretmez; bunu söylemek için kuralın bu sunucudaki değeri (yetki yoksa söylenmez).
    hostsApi
      .statusRules(host.id)
      .then((views: HostStatusRuleView[]) => {
        const v = views.find((x) => x.rule === 'service_failed')
        const level = (v?.custom ?? v?.default)?.level ?? 'off'
        if (!cancelled) setRuleOff(level === 'off')
      })
      .catch(() => undefined)
    return () => {
      cancelled = true
    }
  }, [host.id])

  async function save(next: string[]) {
    setSaving(true)
    setError(null)
    try {
      const res = await hostsApi.setWatchedServices(host.id, next)
      setServices(res.services)
      setWatched(res.watched)
      return true
    } catch (err) {
      setError(err instanceof Error ? err.message : 'izlenen servisler kaydedilemedi')
      return false
    } finally {
      setSaving(false)
    }
  }

  async function addTyped() {
    const parsed = parseServiceName(typed, watched)
    if ('error' in parsed) {
      setTypedError(parsed.error)
      return
    }
    if (await save(toggleWatched(watched, parsed.name, true))) {
      setTyped('')
      setTypedError(null)
    }
  }

  if (!services) {
    return <div className="card">{error ? <div className="error-banner">{error}</div> : <div className="muted">Yükleniyor…</div>}</div>
  }

  const counts = serviceCounts(services, watched)
  const rows = filterServices(services, { q, problemsOnly, watchedOnly })
  const missing = notReported(watched, services)

  return (
    <div>
      <div className="stat-grid">
        <StatTile label="Servis" icon={ListChecks} tone="accent" value={counts.total} />
        <StatTile label="Çalışan" icon={Play} tone="good" value={counts.running} />
        <StatTile label="Sorunlu" icon={CircleAlert} tone={counts.problems > 0 ? 'critical' : undefined} value={counts.problems} hint="hata, yeniden başlama döngüsü ya da çalışmayan izlenen servis" />
        <StatTile label="İzlenen" icon={Eye} tone="accent" value={counts.watched} />
      </div>

      {ruleOff && watched.length > 0 && (
        <div className="notice notice-warning">
          <div className="notice-title">
            <TriangleAlert size={16} strokeWidth={1.9} />
            İzlenen servisler alert üretmiyor
          </div>
          “Servis çalışmıyor” durum kuralı bu sunucu için kapalı: izlenen bir servis dursa da alert açılmaz.{' '}
          <Link to={alertRulesPath({ kind: 'sunucu', id: host.id }, 'servis')}>Alert kurallarında aç →</Link>
        </div>
      )}
      {error && <div className="error-banner">{error}</div>}

      {services.length === 0 ? (
        <div className="card">
          <EmptyState icon={ListChecks}>{healthEmptyText(host, 'Servis listesi')}</EmptyState>
        </div>
      ) : (
        <>
          <div className="toolbar" style={{ justifyContent: 'flex-start' }}>
            <SearchInput value={q} onChange={setQ} placeholder="Servis ara…" />
            <label className="check-row">
              <input type="checkbox" checked={problemsOnly} onChange={(e) => setProblemsOnly(e.target.checked)} />
              Yalnızca sorunlu
            </label>
            <label className="check-row">
              <input type="checkbox" checked={watchedOnly} onChange={(e) => setWatchedOnly(e.target.checked)} />
              Yalnızca izlenen
            </label>
          </div>

          <div className="card table-card">
            <table className="stack">
              <thead>
                <tr>
                  <th>Servis</th>
                  <th>Durum</th>
                  <th>Ne zamandan beri</th>
                  <th>Yeniden başlatma</th>
                  <th>Açılışta</th>
                  <th>İzle</th>
                </tr>
              </thead>
              <tbody>
                {rows.map((s) => {
                  const st = serviceState(s)
                  const since = s.since ? Math.max(0, (now - new Date(s.since).getTime()) / 1000) : undefined
                  return (
                    <tr key={s.name}>
                      <td className="primary">
                        <span className="mono">{s.name}</span>
                        {s.description && s.description !== s.name && <div className="form-hint">{s.description}</div>}
                      </td>
                      <td data-label="Durum">
                        <StatusBadge tone={st.tone}>{st.label}</StatusBadge>
                      </td>
                      <td className="tnum" data-label="Ne zamandan beri" title={s.since ? new Date(s.since).toLocaleString('tr-TR') : undefined}>
                        {since === undefined ? '—' : formatUptime(since)}
                      </td>
                      <td className="tnum" data-label="Yeniden başlatma">
                        {s.restarts ?? '—'}
                      </td>
                      <td data-label="Açılışta">{enabledLabel(s.enabled)}</td>
                      <td data-label="İzle">
                        <input
                          type="checkbox"
                          aria-label={`${s.name} izlensin`}
                          checked={s.watched}
                          disabled={!canEdit || saving}
                          onChange={(e) => save(toggleWatched(watched, s.name, e.target.checked))}
                        />
                      </td>
                    </tr>
                  )
                })}
                {rows.length === 0 && (
                  <tr>
                    <td colSpan={6} className="empty-cell">
                      <EmptyState icon={ListChecks}>Süzgece uyan servis yok.</EmptyState>
                    </td>
                  </tr>
                )}
              </tbody>
            </table>
          </div>
        </>
      )}

      {(missing.length > 0 || canEdit) && (
        <div className="card">
          <h2 className="card-title">
            <Eye size={16} strokeWidth={1.75} />
            Listede olmayan izlenen servisler
          </h2>
          <p className="card-desc">
            Agent’ın son bildirdiği listede olmayan servisler de izlenebilir (ör. henüz kurulmamış ya da geçici olarak görünmeyen).
            Raporlanmayan izlenen bir servis alert üretmez; listede yeniden görününce değerlendirilir.
          </p>
          {missing.length === 0 ? (
            <p className="form-hint">Yok.</p>
          ) : (
            <div className="row row-tight" style={{ marginBottom: 10 }}>
              {missing.map((name) => (
                <span key={name} className="row row-tight">
                  <code>{name}</code>
                  <StatusBadge tone="neutral">raporlanmıyor</StatusBadge>
                  {canEdit && (
                    <button type="button" className="btn btn-sm btn-danger" disabled={saving} onClick={() => save(toggleWatched(watched, name, false))}>
                      <Trash2 size={13} strokeWidth={1.9} />
                      Bırak
                    </button>
                  )}
                </span>
              ))}
            </div>
          )}
          {canEdit && (
            <>
              <div className="row row-tight row-nowrap">
                <input
                  aria-label="İzlenecek servis adı"
                  placeholder="nginx ya da nginx.service"
                  value={typed}
                  style={{ flex: 1 }}
                  onChange={(e) => {
                    setTyped(e.target.value)
                    setTypedError(null)
                  }}
                  onKeyDown={(e) => {
                    if (e.key === 'Enter') {
                      e.preventDefault()
                      addTyped()
                    }
                  }}
                />
                <button type="button" className="btn" onClick={addTyped} disabled={saving}>
                  <Plus size={14} strokeWidth={2} />
                  İzlemeye ekle
                </button>
              </div>
              {typedError && <div className="field-error flush">{typedError}</div>}
            </>
          )}
        </div>
      )}
    </div>
  )
}
