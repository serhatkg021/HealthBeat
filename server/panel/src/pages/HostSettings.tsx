import { useState, type FormEvent, type ReactNode } from 'react'
import { Navigate, useNavigate, useSearchParams } from 'react-router-dom'
import { BellRing, KeyRound, Plug, RotateCw, Save, SlidersHorizontal, Trash2, TriangleAlert, type LucideIcon } from 'lucide-react'
import { hostsApi } from '../api/endpoints'
import { useAuth } from '../auth/AuthContext'
import type { Permission } from '../auth/permissions'
import { Modal } from '../components/Modal'
import { SecretNotice } from '../components/SecretNotice'
import { useTab } from '../components/useTab'
import type { Host } from '../types/api'
import { alertRulesPath } from '../navigation'
import { HostEffectiveRules } from './HostEffectiveRules'
import { NotificationRules } from './NotificationRules'

const SECTION_PARAM = 'bolum'

interface Section {
  id: string
  label: string
  icon: LucideIcon
  // Yalnızca bu izni olanlara görünür (server'ın o bölümdeki yazma uçlarıyla aynı izin).
  requires?: Permission
}

const SECTIONS: Section[] = [
  { id: 'baglanti', label: 'Bağlantı ve kimlik', icon: Plug, requires: 'host.update' },
  { id: 'kurallar', label: 'Geçerli alert kuralları', icon: SlidersHorizontal, requires: 'threshold.view' },
  { id: 'bildirim', label: 'Bildirim kuralları', icon: BellRing },
  { id: 'tehlike', label: 'Tehlikeli bölge', icon: TriangleAlert, requires: 'host.delete' },
]

// Sunucu sayfasının "Ayarlar" sekmesi: bu sunucunun ayarları, bölümlere ayrılmış bir iç menüyle. Alert kuralları
// (eşikler, disk seçimi) burada salt okunur özetlenir ve Alert kuralları sayfasında düzenlenir. Bildirim kuralları izni
// olmayana salt okunur görünür; bağlantı/kimlik ve silme bölümleri hiç görünmez.
export function HostSettings({
  host,
  onChanged,
  onError,
}: {
  host: Host
  onChanged: () => void
  onError: (message: string) => void
}) {
  const navigate = useNavigate()
  const { can } = useAuth()
  const visible = SECTIONS.filter((s) => !s.requires || can(s.requires))
  const ids = visible.map((s) => s.id)
  const [section, setSection] = useTab(ids, ids[0], SECTION_PARAM)
  const [params] = useSearchParams()

  const [revealedSecret, setRevealedSecret] = useState<string | null>(null)
  // Yenileme eski kimlik bilgisini hemen geçersiz kıldığı için önce onay penceresi açılır.
  const [confirmRotate, setConfirmRotate] = useState(false)
  const [rotating, setRotating] = useState(false)
  const credentialName = host.mode === 'pull' ? 'pull secret' : 'API token'
  const credentialField = host.mode === 'pull' ? 'pull_secret' : 'api_token'

  async function handleRotate() {
    setRotating(true)
    try {
      const updated = await hostsApi.rotateCredentials(host.id)
      setRevealedSecret(updated.api_token ?? updated.pull_secret ?? null)
    } catch (err) {
      onError(err instanceof Error ? err.message : 'kimlik bilgisi yenilenemedi')
    } finally {
      setRotating(false)
      setConfirmRotate(false)
    }
  }

  async function handleDelete() {
    if (!confirm('Bu sunucuyu silmek istediğinize emin misiniz?')) return
    try {
      await hostsApi.remove(host.id)
      navigate(`/organizations/${host.organization_id}`)
    } catch (err) {
      onError(err instanceof Error ? err.message : 'sunucu silinemedi')
    }
  }

  // Eski "Eşikler" ve "Disk alert'leri" bölümleri artık Alert kuralları sayfasında bu sunucunun kapsamıdır.
  const legacy = params.get(SECTION_PARAM)
  if (legacy === 'esikler' || legacy === 'disk') return <Navigate to={alertRulesPath({ kind: 'sunucu', id: host.id })} replace />

  const panel = (id: string, children: ReactNode) => (
    <div id={`settings-${id}`} hidden={section !== id} className="settings-panel">
      {children}
    </div>
  )

  return (
    <div className="settings-layout">
      <nav className="settings-nav" aria-label="Ayar bölümleri">
        {visible.map((s) => (
          <button
            key={s.id}
            type="button"
            className={`settings-nav-item${s.id === section ? ' active' : ''}${s.id === 'tehlike' ? ' danger' : ''}`}
            aria-current={s.id === section ? 'page' : undefined}
            onClick={() => setSection(s.id)}
          >
            <s.icon size={15} strokeWidth={1.75} />
            {s.label}
          </button>
        ))}
      </nav>

      <div className="settings-content">
        {can('host.update') &&
          panel(
            'baglanti',
            <div className="grid-2">
              {/* Sunucudan gelen (normalleşmiş) değerler değişince form yeniden kurulup onları gösterir. */}
              <ConnectionCard key={`${host.title}|${host.ip}|${host.interval_seconds}`} host={host} onChanged={onChanged} onError={onError} />

              <div className="card form-card">
                <h2 className="card-title">
                  <KeyRound size={16} strokeWidth={1.75} />
                  Kimlik bilgisi
                </h2>
                <p className="card-desc">
                  Yeni bir kimlik bilgisi üretmek eskisini hemen geçersiz kılar; agent'ın yapılandırmasını güncellemeniz gerekir.
                </p>
                {revealedSecret && (
                  <SecretNotice title="Yeni kimlik bilgisi (bir daha gösterilmeyecek)" text={revealedSecret} onClose={() => setRevealedSecret(null)} />
                )}
                <button className="btn" type="button" onClick={() => setConfirmRotate(true)}>
                  <RotateCw size={15} strokeWidth={1.9} />
                  Kimlik bilgisini yenile
                </button>
              </div>

              <Modal open={confirmRotate} size="sm" title="Kimlik bilgisi yenilensin mi?" onClose={() => !rotating && setConfirmRotate(false)}>
                <p className="confirm-text">
                  <strong>{host.title}</strong> sunucusunun şu anki {credentialName} değeri hemen geçersiz olur. Agent, yeni değer{' '}
                  <code>/etc/healthbeat/agent.json</code> dosyasındaki <code>{credentialField}</code> alanına yazılıp servis yeniden
                  başlatılana kadar {host.mode === 'pull' ? 'sorgulanamaz' : 'rapor gönderemez'}.
                </p>
                <p className="confirm-text">Yeni değer yalnızca bir kez gösterilir.</p>
                <div className="form-actions">
                  <button className="btn btn-danger" type="button" disabled={rotating} onClick={handleRotate}>
                    <RotateCw size={15} strokeWidth={1.9} />
                    Yenile
                  </button>
                  <button className="btn" type="button" disabled={rotating} onClick={() => setConfirmRotate(false)}>
                    Vazgeç
                  </button>
                </div>
              </Modal>
            </div>,
          )}

        {can('threshold.view') && panel('kurallar', <HostEffectiveRules hostId={host.id} />)}
        {panel('bildirim', <NotificationRules scope={{ hostId: host.id }} canEdit={can('notification.edit')} />)}

        {can('host.delete') &&
          panel(
            'tehlike',
            <div className="card form-card card-danger">
              <h2 className="card-title">
                <TriangleAlert size={16} strokeWidth={1.9} />
                Tehlikeli bölge
              </h2>
              <p className="card-desc">Sunucuyu silmek geçmiş metriklerini ve alert'lerini de siler; geri alınamaz.</p>
              <button className="btn btn-danger" type="button" onClick={handleDelete}>
                <Trash2 size={15} strokeWidth={1.9} />
                Sunucuyu sil
              </button>
            </div>,
          )}
      </div>
    </div>
  )
}

// Ad/IP/aralık formu. Başlangıç değerlerini bir kez alır; güncel değerlere dönmesi için üst bileşen
// key ile yeniden kurar.
function ConnectionCard({ host, onChanged, onError }: { host: Host; onChanged: () => void; onError: (message: string) => void }) {
  const [form, setForm] = useState({ title: host.title, ip: host.ip, interval_seconds: host.interval_seconds })

  async function handleUpdate(e: FormEvent) {
    e.preventDefault()
    try {
      await hostsApi.update(host.id, form)
      onChanged()
    } catch (err) {
      onError(err instanceof Error ? err.message : 'sunucu güncellenemedi')
    }
  }

  return (
    <div className="card form-card">
      <h2 className="card-title">
        <Plug size={16} strokeWidth={1.75} />
        Bağlantı ayarları
      </h2>
      <form onSubmit={handleUpdate}>
        <div className="form-row">
          <label htmlFor="edit-title">Sunucu adı</label>
          <input id="edit-title" value={form.title} onChange={(e) => setForm({ ...form, title: e.target.value })} />
        </div>
        <div className="form-row">
          <label htmlFor="edit-ip">IP</label>
          <input id="edit-ip" value={form.ip} onChange={(e) => setForm({ ...form, ip: e.target.value })} />
        </div>
        <div className="form-row">
          <label htmlFor="edit-interval">Aralık (saniye)</label>
          <input
            id="edit-interval"
            type="number"
            min={1}
            value={form.interval_seconds}
            onChange={(e) => setForm({ ...form, interval_seconds: Number(e.target.value) })}
          />
        </div>
        <button className="btn btn-primary" type="submit">
          <Save size={15} strokeWidth={1.9} />
          Kaydet
        </button>
      </form>
    </div>
  )
}
