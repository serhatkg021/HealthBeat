import { Fragment, useCallback, useEffect, useMemo, useState } from 'react'
import { CalendarClock, CalendarPlus, ChevronRight, CircleStop, Pencil, SkipForward, TimerOff, Trash2 } from 'lucide-react'
import { maintenanceApi } from '../api/endpoints'
import { useAuth } from '../auth/AuthContext'
import { EmptyState } from '../components/EmptyState'
import { InfoTip } from '../components/InfoTip'
import { Modal } from '../components/Modal'
import { PageHeader } from '../components/PageHeader'
import { RowMenu, type RowMenuItem } from '../components/RowMenu'
import { SearchInput } from '../components/SearchInput'
import { StatusBadge } from '../components/StatusBadge'
import { TabPanel, Tabs, type TabItem } from '../components/Tabs'
import { useScopeData } from '../components/useScopeData'
import { useNow } from '../components/useNow'
import { useServerClock } from '../components/useServerClock'
import { useTab } from '../components/useTab'
import type { MaintenanceWindow } from '../types/api'
import {
  emptyDraft,
  filterWindows,
  fromWindow,
  localStamp,
  occurrenceText,
  ruleText,
  scopeCount,
  shownOccurrence,
  statusOf,
  typeLabel,
  upcomingLabel,
  validityText,
  type MaintenanceFilter,
} from './maintenance'
import { MaintenanceForm } from './MaintenanceForm'
import { OCCURRENCE_LIMIT, OccurrenceGrid } from './OccurrenceGrid'

const FILTERS: { value: MaintenanceFilter; label: string }[] = [
  { value: 'all', label: 'Tümü' },
  { value: 'active', label: 'Sürüyor' },
  { value: 'scheduled', label: 'Planlı' },
  { value: 'past', label: 'Geçmiş' },
]

type Pending = { kind: 'end' | 'delete' | 'skip' | 'end-occurrence'; window: MaintenanceWindow }

// Bakım pencereleri: sürerken kapsamındaki sunucuların alert'leri kaydedilir ama bildirimi gönderilmez; bakım bitince
// açık kalanlar bildirilir. Saatler kurulumun saat dilimindedir (server saati sağ üstte).
export function MaintenancePage() {
  const { can } = useAuth()
  const canManage = can('maintenance.manage')
  const clock = useServerClock()
  const browserNow = useNow(60_000)
  const year = clock ? Number(clock.nowLocal.slice(0, 4)) : undefined
  const { orgs, hosts } = useScopeData(can('organization.view'))

  const [windows, setWindows] = useState<MaintenanceWindow[] | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [filter, setFilter] = useState<MaintenanceFilter>('all')
  const [query, setQuery] = useState('')
  const [editing, setEditing] = useState<MaintenanceWindow | null>(null)
  const [pending, setPending] = useState<Pending | null>(null)
  const [busy, setBusy] = useState(false)
  // Açık detay satırları ve ayrıntıları (sonraki tekrarlar ayrıntı yanıtında gelir; açılınca okunur).
  const [expanded, setExpanded] = useState<Set<string>>(new Set())
  const [details, setDetails] = useState<Record<string, MaintenanceWindow>>({})
  // Yeni form her açılışta sıfırdan başlar.
  const [formKey, setFormKey] = useState(0)

  const reload = useCallback(() => {
    maintenanceApi
      .list()
      .then((l) => {
        setWindows(l.windows)
        setDetails({}) // açık ayrıntılar yeniden okunur (ör. atlanan tekrar)
        setError(null)
      })
      .catch((err) => setError(err instanceof Error ? err.message : 'bakım pencereleri alınamadı'))
  }, [])
  useEffect(reload, [reload])

  const tabItems: TabItem[] = [
    { id: 'liste', label: 'Pencereler', badge: windows?.length, icon: CalendarClock },
    ...(canManage ? [{ id: 'yeni', label: 'Yeni pencere', icon: CalendarPlus }] : []),
    ...(editing ? [{ id: 'duzenle', label: `Düzenle — ${editing.title}`, icon: Pencil }] : []),
  ]
  const [tab, setTab] = useTab(
    tabItems.map((t) => t.id),
    'liste',
  )

  const shown = useMemo(() => filterWindows(windows ?? [], filter, query), [windows, filter, query])
  const counts = useMemo(() => {
    const c: Record<MaintenanceFilter, number> = { all: 0, active: 0, scheduled: 0, past: 0 }
    for (const w of windows ?? []) {
      c.all++
      c[w.status]++
    }
    return c
  }, [windows])

  const nowLocal = clock?.nowLocal ?? localStamp(browserNow)

  // Açık satırların ayrıntısı (sonraki tekrarlar) henüz okunmadıysa okunur.
  useEffect(() => {
    for (const id of expanded) {
      if (details[id]) continue
      maintenanceApi
        .get(id)
        .then((w) => setDetails((d) => ({ ...d, [id]: w })))
        .catch(() => undefined)
    }
  }, [expanded, details])

  const toggle = (id: string) =>
    setExpanded((prev) => {
      const next = new Set(prev)
      if (next.has(id)) next.delete(id)
      else next.add(id)
      return next
    })

  const actions = (w: MaintenanceWindow): RowMenuItem[] => {
    if (!w.can_manage) return []
    const live = !w.ended_at && w.status !== 'past'
    return [
      ...(live ? [{ label: 'Düzenle', icon: Pencil, onSelect: () => { setEditing(w); setTab('duzenle') } }] : []),
      ...(live && w.recurrence !== 'once' && w.status === 'active'
        ? [{ label: 'Bu tekrarı bitir', icon: TimerOff, onSelect: () => setPending({ kind: 'end-occurrence', window: w }) }]
        : []),
      ...(live && w.recurrence !== 'once' && w.status !== 'active' && w.next
        ? [{ label: 'Sıradaki tekrarı atla', icon: SkipForward, onSelect: () => setPending({ kind: 'skip', window: w }) }]
        : []),
      ...(live ? [{ label: 'Pencereyi bitir', icon: CircleStop, onSelect: () => setPending({ kind: 'end', window: w }) }] : []),
      { label: 'Sil', icon: Trash2, danger: true, separated: live, onSelect: () => setPending({ kind: 'delete', window: w }) },
    ]
  }

  function saved() {
    setEditing(null)
    setFormKey((k) => k + 1)
    setTab('liste')
    reload()
  }

  async function confirmPending() {
    if (!pending) return
    setBusy(true)
    try {
      const id = pending.window.id
      if (pending.kind === 'delete') await maintenanceApi.remove(id)
      else if (pending.kind === 'end') await maintenanceApi.end(id)
      else if (pending.kind === 'skip') await maintenanceApi.skipNext(id)
      else await maintenanceApi.endOccurrence(id)
      setPending(null)
      reload()
    } catch (err) {
      setError(err instanceof Error ? err.message : 'işlem yapılamadı')
      setPending(null)
    } finally {
      setBusy(false)
    }
  }

  const confirmText = (p: Pending): { title: string; body: string; action: string; danger: boolean } => {
    const t = p.window.title
    switch (p.kind) {
      case 'delete':
        return { title: 'Bakım penceresi silinsin mi?', body: `“${t}” ve geçmişi silinir; geri alınamaz.`, action: 'Sil', danger: true }
      case 'end':
        return {
          title: 'Pencere bitirilsin mi?',
          body: `“${t}” şimdi biter${p.window.recurrence === 'once' ? '' : ' ve bir daha tekrarlanmaz'}. Bildirimler hemen normale döner; bakımda açık kalan alert'ler birkaç saniye içinde bildirilir.`,
          action: 'Pencereyi bitir',
          danger: true,
        }
      case 'skip':
        return {
          title: 'Sıradaki tekrar atlansın mı?',
          body: `“${t}” penceresinin ${p.window.next ? occurrenceText(p.window.next.start_local, p.window.next.end_local) : 'sıradaki'} tekrarı yapılmaz; sonraki tekrarlar değişmez.`,
          action: 'Atla',
          danger: false,
        }
      default:
        return {
          title: 'Bu tekrar bitirilsin mi?',
          body: `“${t}” penceresinin süren tekrarı şimdi biter; seri devam eder.`,
          action: 'Bu tekrarı bitir',
          danger: false,
        }
    }
  }

  return (
    <div>
      <PageHeader title="Bakım pencereleri" />
      {error && <div className="error-banner">{error}</div>}

      <Tabs items={tabItems} active={tab} onChange={setTab} label="Bakım pencereleri bölümleri" />

      <TabPanel id="liste" active={tab}>
        <div className="toolbar">
          <div className="toolbar-group">
            <div className="segmented" role="group" aria-label="Duruma göre süz">
              {FILTERS.map((f) => (
                <button key={f.value} type="button" aria-pressed={filter === f.value} onClick={() => setFilter(f.value)}>
                  {f.label} <span className="muted">{counts[f.value]}</span>
                </button>
              ))}
            </div>
            <SearchInput value={query} onChange={setQuery} placeholder="Açıklama ya da kapsam ara…" />
          </div>
          {clock && (
            <span className="muted server-clock-inline tnum" title="Bakım saatleri kurulumun saat dilimine göredir">
              Server saati {clock.time} · {clock.timezone} ({clock.offset})
            </span>
          )}
        </div>
        <div className="card table-card">
          <table className="stack maintenance-table">
            <thead>
              <tr>
                <th className="expand-col" aria-label="Ayrıntı" />
                <th>Açıklama</th>
                <th>Tür</th>
                <th>Kapsam</th>
                <th>
                  <span className="th-with-tip">
                    Zaman
                    <InfoTip label="Zaman">
                      Süren pencerede o anki tekrar, planlı pencerede sıradaki tekrar, geçmiş pencerede son tekrar. Saatler kurulumun
                      saat dilimindedir; ertesi gün biten tekrarda bitişin yanında +1 yazar.
                    </InfoTip>
                  </span>
                </th>
                <th>Durum</th>
                <th className="actions" />
              </tr>
            </thead>
            <tbody>
              {shown.map((w) => {
                const status = statusOf(w)
                const occ = shownOccurrence(w)
                const open = expanded.has(w.id)
                const detail = details[w.id]
                return (
                  <Fragment key={w.id}>
                    <tr className={`maintenance-row${open ? ' open' : ''}`} onClick={() => toggle(w.id)}>
                      <td className="expand-col">
                        <button
                          type="button"
                          className="icon-btn expand-button"
                          aria-expanded={open}
                          aria-label={`${w.title}: ayrıntı`}
                          onClick={(e) => {
                            e.stopPropagation()
                            toggle(w.id)
                          }}
                        >
                          <ChevronRight size={16} />
                        </button>
                      </td>
                      <td className="primary">{w.title}</td>
                      <td className="muted nowrap" data-label="Tür">
                        {typeLabel(w)}
                      </td>
                      <td className="muted" data-label="Kapsam">
                        {scopeCount(w)}
                      </td>
                      <td className="tnum nowrap" data-label="Zaman">
                        {occ ? (
                          <>
                            <span className="time-kind">{occ.kind}</span>
                            {occurrenceText(occ.start_local, occ.end_local)}
                          </>
                        ) : (
                          '—'
                        )}
                      </td>
                      <td data-label="Durum">
                        <StatusBadge tone={status.tone}>{status.text}</StatusBadge>
                      </td>
                      <td className="actions" onClick={(e) => e.stopPropagation()}>
                        <RowMenu label={w.title} items={actions(w)} />
                      </td>
                    </tr>
                    {open && (
                      <tr className="maintenance-detail">
                        <td colSpan={7}>
                          <dl className="detail-grid">
                            <dt>Kural</dt>
                            <dd>{ruleText(w)}</dd>
                            {validityText(w) && (
                              <>
                                <dt>Geçerlilik</dt>
                                <dd>{validityText(w)}</dd>
                              </>
                            )}
                            <dt>Kapsam</dt>
                            <dd>
                              <span className="chip-list">
                                {w.organizations.map((o) => (
                                  <span key={o.id} className="chip-static">
                                    Organizasyon: {o.name}
                                  </span>
                                ))}
                                {w.hosts.map((h) => (
                                  <span key={h.id} className="chip-static">
                                    {h.name}
                                  </span>
                                ))}
                                {w.hidden_scope > 0 && <span className="muted">+{w.hidden_scope} görülemeyen</span>}
                              </span>
                            </dd>
                            {w.recurrence !== 'once' && (
                              <>
                                <dt>{upcomingLabel(Math.min(detail?.upcoming?.length ?? 0, OCCURRENCE_LIMIT))}</dt>
                                <dd>
                                  {detail ? (
                                    <OccurrenceGrid occurrences={detail.upcoming ?? []} skipped={detail.skipped_local} currentYear={year} />
                                  ) : (
                                    <span className="muted">Yükleniyor…</span>
                                  )}
                                </dd>
                              </>
                            )}
                          </dl>
                        </td>
                      </tr>
                    )}
                  </Fragment>
                )
              })}
              {windows !== null && shown.length === 0 && (
                <tr>
                  <td colSpan={7} className="empty-cell">
                    <EmptyState icon={CalendarClock}>{windows.length === 0 ? 'Henüz bakım penceresi yok.' : 'Süzgece uyan pencere yok.'}</EmptyState>
                  </td>
                </tr>
              )}
              {windows === null && !error && (
                <tr>
                  <td colSpan={7} className="muted">
                    Yükleniyor…
                  </td>
                </tr>
              )}
            </tbody>
          </table>
        </div>
      </TabPanel>

      {canManage && (
        <TabPanel id="yeni" active={tab}>
          <MaintenanceForm
            key={formKey}
            initial={emptyDraft(nowLocal)}
            clock={clock}
            orgs={orgs}
            hosts={hosts}
            onSaved={saved}
            onCancel={() => {
              setFormKey((k) => k + 1)
              setTab('liste')
            }}
          />
        </TabPanel>
      )}

      {editing && (
        <TabPanel id="duzenle" active={tab}>
          <MaintenanceForm
            key={editing.id}
            initial={fromWindow(editing, nowLocal)}
            editingId={editing.id}
            clock={clock}
            orgs={orgs}
            hosts={hosts}
            onSaved={saved}
            onCancel={() => {
              setEditing(null)
              setTab('liste')
            }}
          />
        </TabPanel>
      )}

      {pending &&
        (() => {
          const c = confirmText(pending)
          return (
            <Modal open size="sm" title={c.title} onClose={() => !busy && setPending(null)}>
              <p className="confirm-text">{c.body}</p>
              <div className="form-actions">
                <button className={c.danger ? 'btn btn-danger' : 'btn btn-primary'} type="button" disabled={busy} onClick={confirmPending}>
                  {c.action}
                </button>
                <button className="btn" type="button" disabled={busy} onClick={() => setPending(null)}>
                  Vazgeç
                </button>
              </div>
            </Modal>
          )
        })()}
    </div>
  )
}
