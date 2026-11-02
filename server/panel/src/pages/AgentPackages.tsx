import { Fragment, useCallback, useEffect, useRef, useState, type ChangeEvent } from 'react'
import { CheckCircle2, ChevronRight, Copy, Package, RefreshCw, Trash2, Upload, XCircle } from 'lucide-react'
import { agentPackagesApi, settingsApi } from '../api/endpoints'
import { useAuth } from '../auth/AuthContext'
import { CommandLine } from '../components/CommandLine'
import { EmptyState } from '../components/EmptyState'
import { FieldLabel } from '../components/FieldLabel'
import { InfoTip } from '../components/InfoTip'
import { Modal } from '../components/Modal'
import { RowMenu } from '../components/RowMenu'
import { StatusBadge } from '../components/StatusBadge'
import type { AgentPackage, AgentPackageDownload, AgentPackageList, AgentPackageUploadResult, SettingsResponse } from '../types/api'
import { downloadBase, downloadUrl, groupByVersion, packageKind, splitUploadable, uploadSummary } from './agentPackages'
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
  const [results, setResults] = useState<AgentPackageUploadResult[] | null>(null)
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
    const files = Array.from(e.target.files ?? [])
    e.target.value = '' // aynı dosya yeniden seçilebilsin
    if (files.length === 0) return
    const { ok, rejected } = splitUploadable(files)
    const local: AgentPackageUploadResult[] = rejected.map((f) => ({
      filename: f.name,
      error: 'agent paketi değil: yalnızca release’teki .deb, .rpm ve .tar.gz paketleri yüklenebilir',
    }))
    setResults(null)
    setError(null)
    if (ok.length === 0) {
      setResults(local)
      return
    }
    setUploading(true)
    try {
      const r = await agentPackagesApi.upload(ok)
      setResults([...r.results, ...local])
      load()
    } catch (err) {
      setError(message(err, 'paketler yüklenemedi'))
      if (local.length > 0) setResults(local)
    } finally {
      setUploading(false)
    }
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

  return (
    <div className="card form-card settings-card agent-packages">
      <h2 className="card-title">
        Agent paketleri
        {!active && <StatusBadge tone="neutral">kapalı</StatusBadge>}
        <InfoTip label="Agent paketleri">
          Release’teki agent paketleri (.deb, .rpm, .tar.gz) buraya yüklenir; makineler onları agent’ların bağlandığı server adresinden indirir.
          İndirme adresi giriş gerektirmez; her indirme IP adresiyle kaydedilir.
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
        <div className="form-row">
          <FieldLabel
            htmlFor="agent-download-pattern"
            label="İndirme adresi"
            hint="Agent’ların server’a bağlandığı adres (API portu). Agent’lar farklı bir alan adı ya da portla bağlanıyorsa adresin başını ona göre değiştirin."
          />
          <input id="agent-download-pattern" className="mono" readOnly value={`${base}${path}<sürüm>/<dosya>`} />
        </div>

        <div className="toolbar">
          <div className="toolbar-group">
            <input ref={fileInput} type="file" multiple hidden accept=".deb,.rpm,.gz" onChange={onFiles} />
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

        {results && (
          <div className="upload-results" role="status">
            <p className="upload-summary">{uploadSummary(results)}</p>
            <ul>
              {results.map((r) => (
                <li key={r.filename} className={r.package ? 'ok' : 'failed'}>
                  {r.package ? <CheckCircle2 size={14} strokeWidth={2} /> : <XCircle size={14} strokeWidth={2} />}
                  <span className="mono">{r.filename}</span>
                  {r.error && <span className="muted">{r.error}</span>}
                </li>
              ))}
            </ul>
          </div>
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
