import { useState, type FormEvent, type ReactNode } from 'react'
import { useNavigate } from 'react-router-dom'
import { BellRing, HardDrive, KeyRound, Plug, RotateCw, Save, SlidersHorizontal, Trash2, TriangleAlert, type LucideIcon } from 'lucide-react'
import { hostsApi } from '../api/endpoints'
import { SecretNotice } from '../components/SecretNotice'
import { useTab } from '../components/useTab'
import type { Host } from '../types/api'
import { HostThresholdSettings } from './HostThresholdSettings'
import { DiskAlertSettings } from './DiskAlertSettings'
import { NotificationRules } from './NotificationRules'

const SECTION_PARAM = 'bolum'

interface Section {
  id: string
  label: string
  icon: LucideIcon
  // Yalnızca düzenleme yetkisi olanlara görünür (server ile aynı kural: host.update).
  editorsOnly?: boolean
}

const SECTIONS: Section[] = [
  { id: 'baglanti', label: 'Bağlantı ve kimlik', icon: Plug, editorsOnly: true },
  { id: 'esikler', label: 'Eşikler', icon: SlidersHorizontal },
  { id: 'disk', label: 'Disk alert’leri', icon: HardDrive },
  { id: 'bildirim', label: 'Bildirim kuralları', icon: BellRing },
  { id: 'tehlike', label: 'Tehlikeli bölge', icon: TriangleAlert, editorsOnly: true },
]

// Sunucu sayfasının "Ayarlar" sekmesi: kaydedilebilen her ayar burada, bölümlere ayrılmış bir iç menüyle.
// Bölümler açıkken de bağlı kalır (keepMounted); böylece yarım kalmış bir düzenleme bölüm değişince
// kaybolmaz. Yetkisiz kullanıcı eşikleri ve disk seçimini salt okunur görür; bağlantı/kimlik/silme yoktur.
export function HostSettings({
  host,
  canEdit,
  onChanged,
  onError,
  onThresholdsSaved,
}: {
  host: Host
  canEdit: boolean
  onChanged: () => void
  onError: (message: string) => void
  onThresholdsSaved: () => void
}) {
  const navigate = useNavigate()
  const visible = SECTIONS.filter((s) => canEdit || !s.editorsOnly)
  const ids = visible.map((s) => s.id)
  const [section, setSection] = useTab(ids, ids[0], SECTION_PARAM)

  const [revealedSecret, setRevealedSecret] = useState<string | null>(null)

  async function handleRotate() {
    try {
      const updated = await hostsApi.rotateCredentials(host.id)
      setRevealedSecret(updated.api_token ?? updated.pull_secret ?? null)
    } catch (err) {
      onError(err instanceof Error ? err.message : 'kimlik bilgisi yenilenemedi')
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
        {canEdit &&
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
                <button className="btn" type="button" onClick={handleRotate}>
                  <RotateCw size={15} strokeWidth={1.9} />
                  Kimlik bilgisini yenile
                </button>
              </div>
            </div>,
          )}

        {panel('esikler', <HostThresholdSettings hostId={host.id} canEdit={canEdit} onSaved={onThresholdsSaved} />)}
        {panel('disk', <DiskAlertSettings hostId={host.id} canEdit={canEdit} />)}
        {panel('bildirim', <NotificationRules scope={{ hostId: host.id }} canEdit={canEdit} />)}

        {canEdit &&
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
