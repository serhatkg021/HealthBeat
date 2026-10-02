import { Fragment, useEffect, useRef, useState, type FormEvent } from 'react'
import { ChevronDown, ChevronRight, Download, FileText, RefreshCw, Search, X } from 'lucide-react'
import { systemApi } from '../api/endpoints'
import type { LogEntry, LogFiles } from '../types/api'
import { EmptyState } from '../components/EmptyState'
import { StatusBadge } from '../components/StatusBadge'
import { UsageBar } from '../components/UsageBar'
import {
  LOG_LEVELS,
  NO_FILTERS,
  clockToInstant,
  dayOptionLabel,
  diskUsage,
  entryTime,
  filtersActive,
  levelBadge,
  prettyValue,
  requestIdOf,
  summaryAttrs,
  type LogFilters,
} from './logs'

const PAGE_SIZE = 200

// Log Analiz: server'ın kalıcı log dosyalarını (LOG_FILE) sunucuya girmeden, gün gün inceler. Okuma, süzme ve sayfalama
// server'da yapılır; buraya yalnızca istenen sayfa gelir (en yeni satır üstte). Salt okunur; bir günün açılması ve
// indirilmesi denetim kaydına yazılır.
export function SystemLogs() {
  const [files, setFiles] = useState<LogFiles | null>(null)
  const [day, setDay] = useState('')
  // draft formdaki, applied son gönderilen süzgeçlerdir: yazarken her tuşta istek atılmaz.
  const [draft, setDraft] = useState<LogFilters>(NO_FILTERS)
  const [applied, setApplied] = useState<LogFilters>(NO_FILTERS)
  const [entries, setEntries] = useState<LogEntry[]>([])
  const [nextBefore, setNextBefore] = useState(0)
  const [scanned, setScanned] = useState<number | null>(null)
  const [open, setOpen] = useState<Set<number>>(new Set())
  const [loading, setLoading] = useState(false)
  const [downloading, setDownloading] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const latest = useRef(0)

  function loadFiles() {
    systemApi
      .logFiles()
      .then((f) => {
        setFiles(f)
        setDay((cur) => (cur && f.days.some((d) => d.day === cur) ? cur : (f.days[0]?.day ?? '')))
      })
      .catch((err) => setError(err instanceof Error ? err.message : 'log dosyaları yüklenemedi'))
  }

  function load(filters: LogFilters, before?: number) {
    if (!day) return
    const id = ++latest.current
    setLoading(true)
    setError(null)
    systemApi
      .logEntries({
        day,
        level: filters.level || undefined,
        q: filters.q.trim() || undefined,
        request_id: filters.requestId.trim() || undefined,
        from: clockToInstant(day, filters.from),
        to: clockToInstant(day, filters.to),
        before,
        limit: PAGE_SIZE,
      })
      .then((page) => {
        if (id !== latest.current) return
        setEntries((prev) => (before ? [...prev, ...page.entries] : page.entries))
        setNextBefore(page.next_before)
        if (!before) {
          setScanned(page.scanned)
          setOpen(new Set())
        }
      })
      .catch((err) => {
        if (id !== latest.current) return
        setError(err instanceof Error ? err.message : 'log satırları yüklenemedi')
      })
      .finally(() => {
        if (id === latest.current) setLoading(false)
      })
  }

  useEffect(loadFiles, [])
  // eslint-disable-next-line react-hooks/exhaustive-deps
  useEffect(() => load(applied), [day, applied])

  function apply(next: LogFilters) {
    setDraft(next)
    setApplied(next)
  }

  function handleSubmit(e: FormEvent) {
    e.preventDefault()
    apply({ ...draft })
  }

  function toggle(line: number) {
    setOpen((prev) => {
      const next = new Set(prev)
      if (!next.delete(line)) next.add(line)
      return next
    })
  }

  async function handleDownload() {
    setDownloading(true)
    setError(null)
    try {
      await systemApi.downloadLog(day)
    } catch (err) {
      setError(err instanceof Error ? err.message : 'log indirilemedi')
    } finally {
      setDownloading(false)
    }
  }

  if (files && !files.enabled) {
    return (
      <div className="card">
        <EmptyState icon={FileText}>
          Server dosyaya loglamıyor (LOG_FILE boş ya da log dosyası açılamadı); burada gösterilecek bir şey yok. Loglar yalnızca server’ın standart
          çıktısında.
        </EmptyState>
      </div>
    )
  }

  const usage = files ? diskUsage(files) : null

  return (
    <div>
      {error && <div className="error-banner">{error}</div>}

      {files && usage && (
        <div className="card queue-pool">
          <div className="queue-pool-head">
            <span className="queue-pool-title">Log dosyaları</span>
            <span className="queue-pool-value">{usage.text}</span>
          </div>
          <UsageBar pct={usage.pct} label="Log dosyalarının kapladığı alan" size="sm" levels={{ warning_level: 80, critical_level: 95 }} />
          <p className="form-hint queue-pool-hint">
            {files.days.length} günün logu duruyor{files.max_age_days > 0 && `; ${files.max_age_days} günden eskiler ve toplam sınırı aşan en eski dosyalar silinir`}.
            Sınırlar Ayarlar → Sistem Ayarları → Loglama’dan değişir. Günler server’ın tarihine göre ayrılır; satır saatleri bu tarayıcının saatiyle
            gösterilir.
          </p>
        </div>
      )}

      <form className="log-filters" onSubmit={handleSubmit}>
        <label>
          <span>Gün</span>
          <select value={day} onChange={(e) => setDay(e.target.value)}>
            {files?.days.length === 0 && <option value="">log dosyası yok</option>}
            {files?.days.map((d) => (
              <option key={d.day} value={d.day}>
                {dayOptionLabel(d)}
              </option>
            ))}
          </select>
        </label>
        <label>
          <span>Seviye</span>
          <select value={draft.level} onChange={(e) => apply({ ...draft, level: e.target.value })}>
            {LOG_LEVELS.map((l) => (
              <option key={l.value} value={l.value}>
                {l.label}
              </option>
            ))}
          </select>
        </label>
        <label className="log-filter-wide">
          <span>Metin</span>
          <input value={draft.q} maxLength={200} placeholder="satırın herhangi bir yerinde" onChange={(e) => setDraft({ ...draft, q: e.target.value })} />
        </label>
        <label className="log-filter-wide">
          <span>İstek kimliği</span>
          <input value={draft.requestId} maxLength={200} placeholder="request_id" onChange={(e) => setDraft({ ...draft, requestId: e.target.value })} />
        </label>
        <label>
          <span>Saat (başlangıç)</span>
          <input type="time" value={draft.from} onChange={(e) => setDraft({ ...draft, from: e.target.value })} />
        </label>
        <label>
          <span>Saat (bitiş)</span>
          <input type="time" value={draft.to} onChange={(e) => setDraft({ ...draft, to: e.target.value })} />
        </label>
        <div className="log-filter-actions">
          <button className="btn btn-primary" type="submit" disabled={loading || !day}>
            <Search size={15} strokeWidth={1.9} />
            Süz
          </button>
          {filtersActive(applied) && (
            <button className="btn" type="button" onClick={() => apply(NO_FILTERS)}>
              <X size={15} strokeWidth={1.9} />
              Temizle
            </button>
          )}
          <button className="btn" type="button" disabled={loading || !day} onClick={() => load(applied)}>
            <RefreshCw size={15} strokeWidth={1.9} />
            Yenile
          </button>
          <button className="btn" type="button" disabled={downloading || !day} onClick={handleDownload} title="Günün logunu düz metin olarak indir">
            <Download size={15} strokeWidth={1.9} />
            {downloading ? 'İndiriliyor…' : 'İndir'}
          </button>
        </div>
      </form>

      <div className="card table-card">
        <table className="log-table">
          <thead>
            <tr>
              <th aria-label="Ayrıntı" />
              <th>Saat</th>
              <th>Seviye</th>
              <th>İleti</th>
            </tr>
          </thead>
          <tbody>
            {entries.map((e) => {
              const badge = levelBadge(e.level)
              const isOpen = open.has(e.line)
              const rid = requestIdOf(e)
              return (
                <Fragment key={e.line}>
                  <tr className={`log-row${isOpen ? ' open' : ''}`} onClick={() => toggle(e.line)}>
                    <td className="log-toggle">
                      <button type="button" className="icon-btn" aria-expanded={isOpen} aria-label={isOpen ? 'Ayrıntıyı kapat' : 'Ayrıntıyı aç'}>
                        {isOpen ? <ChevronDown size={15} strokeWidth={1.9} /> : <ChevronRight size={15} strokeWidth={1.9} />}
                      </button>
                    </td>
                    <td className="mono nowrap log-time" title={e.time ? new Date(e.time).toLocaleString() : undefined}>
                      {entryTime(e.time)}
                    </td>
                    <td>
                      <StatusBadge tone={badge.tone}>{badge.label}</StatusBadge>
                    </td>
                    <td className="log-message">
                      <span className={e.level ? '' : 'mono'}>{e.message}</span>
                      {summaryAttrs(e).map((a) => (
                        <span key={a.key} className="log-attr">
                          <span className="log-attr-key">{a.key}</span>
                          {a.value}
                        </span>
                      ))}
                      {e.truncated && <StatusBadge tone="neutral">satır kesildi</StatusBadge>}
                    </td>
                  </tr>
                  {isOpen && (
                    <tr className="log-detail">
                      <td />
                      <td colSpan={3}>
                        <dl className="log-fields">
                          <dt>satır</dt>
                          <dd className="mono">
                            {e.line}
                            {e.time && ` · ${e.time}`}
                          </dd>
                          {e.attrs.map((a, i) => (
                            <Fragment key={`${a.key}-${i}`}>
                              <dt>{a.key}</dt>
                              <dd>
                                <pre className="log-value">{prettyValue(a.value)}</pre>
                                {a.key === 'request_id' && a.value !== applied.requestId && (
                                  <button className="btn btn-sm" type="button" onClick={() => apply({ ...NO_FILTERS, requestId: a.value })}>
                                    Bu isteğin bütün satırları
                                  </button>
                                )}
                              </dd>
                            </Fragment>
                          ))}
                          {e.attrs.length === 0 && !rid && (
                            <>
                              <dt>alanlar</dt>
                              <dd className="muted">Bu satırda ek alan yok.</dd>
                            </>
                          )}
                        </dl>
                      </td>
                    </tr>
                  )}
                </Fragment>
              )
            })}
            {entries.length === 0 && !loading && (
              <tr>
                <td colSpan={4} className="empty-cell">
                  <EmptyState icon={FileText}>
                    {!day ? 'Henüz log dosyası yok.' : filtersActive(applied) ? 'Bu süzgece uyan satır yok.' : 'Bu günün logu boş.'}
                  </EmptyState>
                </td>
              </tr>
            )}
          </tbody>
        </table>
      </div>

      <div className="log-footer">
        <span className="muted">
          {loading ? 'Yükleniyor…' : scanned !== null && `${entries.length} satır gösteriliyor · ${scanned} satır tarandı`}
        </span>
        {nextBefore > 0 && (
          <button className="btn" disabled={loading} onClick={() => load(applied, nextBefore)}>
            Daha eski satırlar
          </button>
        )}
      </div>
    </div>
  )
}
