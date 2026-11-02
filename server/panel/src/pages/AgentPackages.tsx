import { Fragment, useCallback, useEffect, useRef, useState, type ChangeEvent } from 'react'
import { CheckCircle2, ChevronRight, Copy, Loader2, Package, RefreshCw, Trash2, Upload, X, XCircle } from 'lucide-react'
import { ApiError } from '../api/client'
import { agentPackagesApi, settingsApi } from '../api/endpoints'
import { useAuth } from '../auth/AuthContext'
import { CommandLine } from '../components/CommandLine'
import { EmptyState } from '../components/EmptyState'
import { InfoTip } from '../components/InfoTip'
import { Modal } from '../components/Modal'
import { RowMenu } from '../components/RowMenu'
import { StatusBadge } from '../components/StatusBadge'
import type { AgentPackage, AgentPackageArch, AgentPackageDownload, AgentPackageList, SettingsResponse } from '../types/api'
import {
  HEADER_BYTES,
  downloadBase,
  downloadUrl,
  draftProblems,
  draftSummary,
  formatLabel,
  groupByVersion,
  isValidVersion,
  newDraft,
  packageKind,
  releaseFileName,
  uploadable,
  type DraftProblems,
  type UploadDraft,
} from './agentPackages'
import { formatDateTime } from './datetime'
import { formatBytes } from './hardwareTotals'

const message = (err: unknown, fallback: string) => (err instanceof Error ? err.message : fallback)

// Ayarlar → Agent sürümleri → Paketler: server'a yüklenen agent paketleri. Görmek ve yönetmek agent.package.manage,
// indirmeyi açıp kapatmak settings.manage ister (ikisi de varsayılan olarak yalnızca süper admin).
export function AgentPackages({ settings, onSettingsSaved }: { settings: SettingsResponse; onSettingsSaved: (s: SettingsResponse) => void }) {
  const { can } = useAuth()
  const [list, setList] = useState<AgentPackageList | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [loading, setLoading] = useState(true)
  const [uploading, setUploading] = useState(false)
  // drafts, yükleme listesidir: dosyalar seçilince sürüm ve mimari burada düzenlenir, sonra sırayla yüklenir.
  const [drafts, setDrafts] = useState<UploadDraft[]>([])
  const files = useRef(new Map<string, File>())
  const nextKey = useRef(0)
  const [expanded, setExpanded] = useState<Set<string>>(new Set())
  const [downloads, setDownloads] = useState<Record<string, AgentPackageDownload[] | undefined>>({})
  const [deleting, setDeleting] = useState<AgentPackage | null>(null)
  const [toggleBusy, setToggleBusy] = useState(false)
  // pendingToggle, onay bekleyen aç/kapa isteğidir (true: aç, false: kapat).
  const [pendingToggle, setPendingToggle] = useState<boolean | null>(null)
  const fileInput = useRef<HTMLInputElement>(null)

  const fetchList = useCallback(
    () =>
      agentPackagesApi
        .list()
        .then((l) => {
          setList(l)
          setError(null)
        })
        .catch((err) => setError(message(err, 'paketler yüklenemedi')))
        .finally(() => setLoading(false)),
    [],
  )
  const load = () => {
    setLoading(true)
    void fetchList()
  }

  useEffect(() => {
    void fetchList()
  }, [fetchList])

  const loadDownloads = (id: string) =>
    agentPackagesApi
      .downloads(id)
      .then((r) => setDownloads((d) => ({ ...d, [id]: r.downloads })))
      .catch((err) => setError(message(err, 'indirmeler yüklenemedi')))

  function toggle(id: string) {
    const next = new Set(expanded)
    if (next.has(id)) {
      next.delete(id)
    } else {
      next.add(id)
      void loadDownloads(id)
    }
    setExpanded(next)
  }

  async function onFiles(e: ChangeEvent<HTMLInputElement>) {
    const picked = Array.from(e.target.files ?? [])
    e.target.value = '' // aynı dosya yeniden seçilebilsin
    if (picked.length === 0) return
    setError(null)
    const added = await Promise.all(
      picked.map(async (f) => {
        const key = String(nextKey.current++)
        files.current.set(key, f)
        return newDraft(key, f.name, f.size, new Uint8Array(await f.slice(0, HEADER_BYTES).arrayBuffer()))
      }),
    )
    setDrafts((d) => [...d, ...added])
  }

  // editDraft, satırı değiştirir; değişen alanın server hatası silinir, başarısız satır yeniden denenebilir olur.
  function editDraft(key: string, patch: Partial<Pick<UploadDraft, 'version' | 'arch'>>) {
    setDrafts((list) =>
      list.map((d) => {
        if (d.key !== key) return d
        const fieldErrors = { ...d.fieldErrors }
        for (const f of Object.keys(patch) as ('version' | 'arch')[]) delete fieldErrors[f]
        return { ...d, ...patch, fieldErrors, status: d.status === 'failed' ? 'pending' : d.status, error: undefined }
      }),
    )
  }

  function removeDraft(key: string) {
    files.current.delete(key)
    setDrafts((list) => list.filter((d) => d.key !== key))
  }

  function closeDrafts() {
    files.current.clear()
    setDrafts([])
  }

  const setDraft = (key: string, patch: Partial<UploadDraft>) => setDrafts((list) => list.map((d) => (d.key === key ? { ...d, ...patch } : d)))

  // uploadAll, hazır satırları sırayla yükler; her satır kendi sonucunu gösterir.
  async function uploadAll(ready: UploadDraft[]) {
    setUploading(true)
    setError(null)
    let any = false
    for (const d of ready) {
      const file = files.current.get(d.key)
      if (!file || !d.arch) continue
      setDraft(d.key, { status: 'uploading', error: undefined, fieldErrors: undefined })
      try {
        await agentPackagesApi.upload(file, d.version.trim(), d.arch)
        files.current.delete(d.key)
        setDraft(d.key, { status: 'done' })
        any = true
      } catch (err) {
        const fields = err instanceof ApiError ? err.fields : undefined
        setDraft(d.key, {
          status: 'failed',
          error: message(err, 'yüklenemedi'),
          fieldErrors: fields ? { version: fields.version, arch: fields.arch } : undefined,
        })
      }
    }
    setUploading(false)
    if (any) load()
  }

  async function confirmToggle() {
    if (pendingToggle === null) return
    setToggleBusy(true)
    setError(null)
    try {
      onSettingsSaved(await settingsApi.update({ agent_download_enabled: pendingToggle }))
    } catch (err) {
      setError(message(err, 'ayar kaydedilemedi'))
    } finally {
      setToggleBusy(false)
      setPendingToggle(null)
    }
  }

  async function confirmDelete() {
    if (!deleting) return
    try {
      await agentPackagesApi.remove(deleting.id)
      setDeleting(null)
      load()
    } catch (err) {
      setDeleting(null)
      setError(message(err, 'paket silinemedi'))
    }
  }

  const storage = list?.storage_available ?? true
  const base = downloadBase(window.HEALTHBEAT_CONFIG?.apiBaseUrl, window.location.hostname, window.HEALTHBEAT_CONFIG?.apiPort)
  const path = list?.download_path ?? '/agent/download/'
  const groups = groupByVersion(list?.packages ?? [])
  const latest = settings.values.latest_agent_version
  const enabled = settings.values.agent_download_enabled
  const active = enabled && storage
  const problems = draftProblems(drafts, list?.packages ?? [])
  const ready = uploadable(drafts, problems)
  const blocked = Object.keys(problems).length > 0
  const pending = drafts.some((d) => d.format && d.status !== 'done')

  return (
    <div className="card form-card settings-card agent-packages">
      <h2 className="card-title">
        Agent paketleri
        {!active && <StatusBadge tone="neutral">kapalı</StatusBadge>}
        <InfoTip label="Agent paketleri">
          Agent paketleri (.deb, .rpm, .tar.gz) buraya yüklenir; sürüm ve mimari yüklerken seçilir, dosya adından okunabiliyorsa dolu gelir. Makineler
          paketleri agent’ların bağlandığı server adresinden indirir; adres giriş gerektirmez, her indirme IP adresiyle kaydedilir.
        </InfoTip>
      </h2>
      {error && <div className="error-banner">{error}</div>}
      {!storage && (
        <div className="notice">
          Paket dizini kullanılamıyor (AGENT_PACKAGES_DIR): yükleme ve indirme kapalı. Kayıtlı paketler aşağıda listelenir.
        </div>
      )}

      <label className="check-row">
        <input
          type="checkbox"
          checked={enabled}
          disabled={!can('settings.manage') || toggleBusy}
          onChange={(e) => setPendingToggle(e.target.checked)}
        />
        Paket indirme açık
        <InfoTip label="Paket indirme">
          Kapalıyken indirme adresleri “bulunamadı” döner; paketler ve kayıtları silinmez. Yalnızca push modundaki agent’lar server’a bağlandığı için
          onlar içindir.
        </InfoTip>
      </label>
      {storage && !enabled && (
        <div className="notice">Paket indirme kapalı: indirme adresleri “bulunamadı” döner. Paketler ve kayıtları duruyor.</div>
      )}

      <div className={`agent-packages-body${active ? '' : ' is-off'}`}>
        <div className="toolbar">
          <div className="toolbar-group">
            <input ref={fileInput} type="file" multiple hidden onChange={(e) => void onFiles(e)} />
            <button className="btn btn-primary" type="button" disabled={!storage || uploading} onClick={() => fileInput.current?.click()}>
              <Upload size={15} strokeWidth={1.9} />
              {uploading ? 'Yükleniyor…' : 'Paket yükle'}
            </button>
            <button className="btn" type="button" disabled={loading} onClick={load}>
              <RefreshCw size={15} strokeWidth={1.9} />
              Yenile
            </button>
          </div>
        </div>

        {drafts.length > 0 && (
          <UploadDrafts
            drafts={drafts}
            problems={problems}
            uploading={uploading}
            onEdit={editDraft}
            onRemove={removeDraft}
            onUpload={() => void uploadAll(ready)}
            onClose={closeDrafts}
            canUpload={ready.length > 0 && !blocked}
            readyCount={ready.length}
            pending={pending}
          />
        )}

        {list && groups.length === 0 && <EmptyState icon={Package}>Henüz paket yüklenmedi.</EmptyState>}

        {groups.map((g) => (
          <div key={g.version} className="package-group">
            <h3 className="package-group-title">
              {g.version}
              {g.version === latest && <StatusBadge tone="good">en güncel</StatusBadge>}
              <span className="muted">
                {g.packages.length} dosya · {g.downloads} indirme
              </span>
            </h3>
            <div className="table-card">
              <table className="stack package-table">
                <thead>
                  <tr>
                    <th className="expand-col" />
                    <th>Dosya</th>
                    <th>Boyut</th>
                    <th>Yükleme</th>
                    <th>İndirme</th>
                    <th className="actions" />
                  </tr>
                </thead>
                <tbody>
                  {g.packages.map((p) => {
                    const open = expanded.has(p.id)
                    const url = downloadUrl(base, path, p)
                    return (
                      <Fragment key={p.id}>
                        <tr className={`package-row${open ? ' open' : ''}`} onClick={() => toggle(p.id)}>
                          <td className="expand-col">
                            <button
                              type="button"
                              className="icon-btn expand-button"
                              aria-expanded={open}
                              aria-label={`${p.filename}: ayrıntı`}
                              onClick={(e) => {
                                e.stopPropagation()
                                toggle(p.id)
                              }}
                            >
                              <ChevronRight size={16} />
                            </button>
                          </td>
                          <td className="primary">
                            <span className="mono">{p.filename}</span>
                            <div className="cell-sub">{packageKind(p)}</div>
                          </td>
                          <td className="nowrap tnum" data-label="Boyut">
                            {formatBytes(p.size_bytes)}
                          </td>
                          <td data-label="Yükleme">
                            <div className="cell-stack">
                              <span className="nowrap">{formatDateTime(p.uploaded_at)}</span>
                              {p.uploaded_by_email && <div className="cell-sub">{p.uploaded_by_email}</div>}
                            </div>
                          </td>
                          <td className="nowrap" data-label="İndirme">
                            <div className="cell-stack">
                              <span className="tnum">{p.download_count}</span>
                              <div className="cell-sub">
                                {p.last_downloaded_at ? `son: ${formatDateTime(p.last_downloaded_at)}` : 'hiç indirilmedi'}
                              </div>
                            </div>
                          </td>
                          <td className="actions" onClick={(e) => e.stopPropagation()}>
                            <RowMenu
                              label={p.filename}
                              items={[
                                {
                                  label: 'İndirme adresini kopyala',
                                  icon: Copy,
                                  onSelect: () => void navigator.clipboard?.writeText(url),
                                },
                                {
                                  label: 'Sil',
                                  icon: Trash2,
                                  danger: true,
                                  separated: true,
                                  onSelect: () => setDeleting(p),
                                },
                              ]}
                            />
                          </td>
                        </tr>
                        {open && (
                          <tr className="package-detail">
                            <td colSpan={6}>
                              <dl className="detail-grid">
                                {p.original_filename && p.original_filename !== p.filename && (
                                  <>
                                    <dt>Yüklenen dosya</dt>
                                    <dd className="mono">{p.original_filename}</dd>
                                  </>
                                )}
                                <dt>İndirme adresi</dt>
                                <dd>
                                  <CommandLine value={url} name="İndirme adresini" />
                                </dd>
                                <dt>SHA256</dt>
                                <dd>
                                  <CommandLine value={p.sha256} name="SHA256’yı" />
                                </dd>
                                <dt>Son indirmeler</dt>
                                <dd>
                                  <DownloadList downloads={downloads[p.id]} />
                                </dd>
                              </dl>
                            </td>
                          </tr>
                        )}
                      </Fragment>
                    )
                  })}
                </tbody>
              </table>
            </div>
          </div>
        ))}
      </div>

      <Modal
        open={pendingToggle !== null}
        title={pendingToggle ? 'Paket indirmeyi aç' : 'Paket indirmeyi kapat'}
        size="sm"
        onClose={() => setPendingToggle(null)}
      >
        <p>
          {pendingToggle
            ? 'Yüklü paketler indirme adreslerinden giriş gerektirmeden indirilebilir hâle gelecek. Açılsın mı?'
            : 'İndirme adresleri “bulunamadı” dönecek; paketler, kayıtları ve indirme geçmişi silinmez. Kapatılsın mı?'}
        </p>
        <div className="form-actions">
          <button
            className={pendingToggle ? 'btn btn-primary' : 'btn btn-danger'}
            type="button"
            disabled={toggleBusy}
            onClick={() => void confirmToggle()}
          >
            {pendingToggle ? 'Aç' : 'Kapat'}
          </button>
          <button className="btn" type="button" onClick={() => setPendingToggle(null)}>
            Vazgeç
          </button>
        </div>
      </Modal>

      <Modal open={deleting !== null} title="Paketi sil" size="sm" onClose={() => setDeleting(null)}>
        <p>
          <span className="mono">{deleting?.filename}</span> silinsin mi? Dosya, kaydı ve indirme geçmişi silinir; bu adresten artık indirilemez.
        </p>
        <div className="form-actions">
          <button className="btn btn-danger" type="button" onClick={() => void confirmDelete()}>
            <Trash2 size={15} strokeWidth={1.9} />
            Sil
          </button>
          <button className="btn" type="button" onClick={() => setDeleting(null)}>
            Vazgeç
          </button>
        </div>
      </Modal>
    </div>
  )
}

const STATUS_TEXT: Record<UploadDraft['status'], string> = { pending: 'hazır', uploading: 'yükleniyor…', done: 'yüklendi', failed: 'yüklenemedi' }

// UploadDrafts, yüklemeden önceki listedir: her dosyada sürüm ve mimari düzenlenir, biçim içerikten gelir.
function UploadDrafts({
  drafts,
  problems,
  uploading,
  onEdit,
  onRemove,
  onUpload,
  onClose,
  canUpload,
  readyCount,
  pending,
}: {
  drafts: UploadDraft[]
  problems: Record<string, DraftProblems>
  uploading: boolean
  onEdit: (key: string, patch: Partial<Pick<UploadDraft, 'version' | 'arch'>>) => void
  onRemove: (key: string) => void
  onUpload: () => void
  onClose: () => void
  canUpload: boolean
  readyCount: number
  pending: boolean
}) {
  const summary = draftSummary(drafts)
  return (
    <div className="upload-drafts">
      <div className="upload-drafts-head">
        <h3>Yüklenecek dosyalar</h3>
        <span className="muted">Sürüm ve mimari dosya adından okunabildiyse dolduruldu; gerekirse değiştirin.</span>
      </div>
      <div className="table-card">
        <table className="stack draft-table">
          <thead>
            <tr>
              <th>Dosya</th>
              <th>Sürüm</th>
              <th>Mimari</th>
              <th>Durum</th>
              <th className="actions" />
            </tr>
          </thead>
          <tbody>
            {drafts.map((d) => {
              const p = problems[d.key]
              const locked = uploading || d.status === 'done' || d.status === 'uploading'
              const target =
                d.format && d.arch && isValidVersion(d.version.trim())
                  ? releaseFileName({ version: d.version.trim(), arch: d.arch, format: d.format })
                  : null
              return (
                <tr key={d.key} className={`draft-row is-${d.format ? d.status : 'skipped'}`}>
                  <td className="primary">
                    <span className="mono">{d.name}</span>
                    <div className="cell-sub">
                      {formatBytes(d.size)} · {d.format ? formatLabel(d.format) : 'agent paketi değil (.deb, .rpm ya da .tar.gz olmalı)'}
                      {target && target !== d.name && (
                        <>
                          {' '}
                          · adresteki adı: <span className="mono">{target}</span>
                        </>
                      )}
                    </div>
                    {(p?.file || d.error) && <p className="field-error flush">{p?.file ?? d.error}</p>}
                  </td>
                  <td data-label="Sürüm">
                    {d.format ? (
                      <div className="draft-field">
                        <input
                          className="mono"
                          aria-label={`${d.name}: sürüm`}
                          aria-invalid={Boolean(p?.version)}
                          placeholder="1.2.0"
                          value={d.version}
                          disabled={locked}
                          onChange={(e) => onEdit(d.key, { version: e.target.value })}
                        />
                        {p?.version && <p className="field-error flush">{p.version}</p>}
                      </div>
                    ) : (
                      <span className="muted">—</span>
                    )}
                  </td>
                  <td data-label="Mimari">
                    {d.format ? (
                      <div className="draft-field">
                        <select
                          aria-label={`${d.name}: mimari`}
                          aria-invalid={Boolean(p?.arch)}
                          value={d.arch}
                          disabled={locked}
                          onChange={(e) => onEdit(d.key, { arch: e.target.value as AgentPackageArch | '' })}
                        >
                          <option value="">Seçin</option>
                          <option value="amd64">amd64 (x86_64)</option>
                          <option value="arm64">arm64 (aarch64)</option>
                        </select>
                        {p?.arch && <p className="field-error flush">{p.arch}</p>}
                      </div>
                    ) : (
                      <span className="muted">—</span>
                    )}
                  </td>
                  <td className="nowrap" data-label="Durum">
                    <DraftStatus draft={d} problem={p} />
                  </td>
                  <td className="actions">
                    {d.status !== 'done' && (
                      <button
                        type="button"
                        className="icon-btn"
                        aria-label={`${d.name}: listeden çıkar`}
                        disabled={uploading}
                        onClick={() => onRemove(d.key)}
                      >
                        <X size={15} />
                      </button>
                    )}
                  </td>
                </tr>
              )
            })}
          </tbody>
        </table>
      </div>
      {summary && (
        <p className="upload-summary" role="status">
          {summary}
        </p>
      )}
      <div className="form-actions">
        {pending && (
          <button className="btn btn-primary" type="button" disabled={!canUpload || uploading} onClick={onUpload}>
            <Upload size={15} strokeWidth={1.9} />
            {uploading ? 'Yükleniyor…' : `Yükle (${readyCount})`}
          </button>
        )}
        <button className="btn" type="button" disabled={uploading} onClick={onClose}>
          {pending ? 'Vazgeç' : 'Kapat'}
        </button>
      </div>
    </div>
  )
}

function DraftStatus({ draft, problem }: { draft: UploadDraft; problem: DraftProblems | undefined }) {
  if (!draft.format) return <span className="muted">yüklenmez</span>
  if (draft.status === 'pending' && problem) return <span className="muted">{problem.version || problem.arch ? 'eksik' : 'yüklenemez'}</span>
  const Icon = draft.status === 'done' ? CheckCircle2 : draft.status === 'failed' ? XCircle : draft.status === 'uploading' ? Loader2 : null
  return (
    <span className={`draft-status ${draft.status}`}>
      {Icon && <Icon size={14} strokeWidth={2} className={draft.status === 'uploading' ? 'spin' : undefined} />}
      {STATUS_TEXT[draft.status]}
    </span>
  )
}

function DownloadList({ downloads }: { downloads: AgentPackageDownload[] | undefined }) {
  if (!downloads) return <span className="muted">Yükleniyor…</span>
  if (downloads.length === 0) return <span className="muted">Henüz indirilmedi.</span>
  return (
    <ul className="download-list">
      {downloads.map((d, i) => (
        <li key={i}>
          <span className="tnum nowrap">{formatDateTime(d.downloaded_at)}</span>
          <span className="mono">{d.client_ip}</span>
          {d.user_agent && <span className="muted">{d.user_agent}</span>}
        </li>
      ))}
    </ul>
  )
}
