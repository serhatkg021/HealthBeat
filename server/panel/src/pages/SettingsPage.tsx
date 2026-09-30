import { useCallback, useEffect, useState, type FormEvent, type ReactNode } from 'react'
import { BellRing, Clock, Database, FileText, Globe, Mail, Pencil, Plus, RotateCcw, Save, Send, Trash2, Users, type LucideIcon } from 'lucide-react'
import { ApiError } from '../api/client'
import { channelsApi, ownersApi, settingsApi, type OwnerInput } from '../api/endpoints'
import { useAuth } from '../auth/AuthContext'
import { EmptyState } from '../components/EmptyState'
import { Modal } from '../components/Modal'
import { PageHeader } from '../components/PageHeader'
import { StatusBadge } from '../components/StatusBadge'
import { useTab } from '../components/useTab'
import { alertLevelLabel } from '../labels'
import type { AlertLevel, ChannelInfo, NotificationOwner, SettingsField, SettingsResponse } from '../types/api'
import { SETTINGS_CHANGED, SETTINGS_SECTIONS, buildPatch, channelStatus, defaultText, fieldMessage, toDisplay, type SectionDef } from './settingsForm'

const SECTION_PARAM = 'bolum'

const NAV: { id: string; label: string; icon: LucideIcon }[] = [
  { id: 'sahipler', label: 'Sistem sahipleri', icon: Users },
  { id: 'kanallar', label: 'Bildirim kanalları', icon: BellRing },
  { id: 'agent', label: 'Agent sürümleri', icon: Database },
  { id: 'saklama', label: 'Veri saklama', icon: Clock },
  { id: 'oturum', label: 'Oturum ve hız sınırları', icon: Clock },
  { id: 'panel', label: 'Panel adresi', icon: Globe },
  { id: 'log', label: 'Loglama', icon: FileText },
]

const LEVELS: AlertLevel[] = ['info', 'warning', 'critical']

const message = (err: unknown, fallback: string) => (err instanceof Error ? err.message : fallback)
const when = (iso: string | null | undefined) => (iso ? new Date(iso).toLocaleString() : '—')
const announce = () => window.dispatchEvent(new Event(SETTINGS_CHANGED))

// Sistem ayarları: kurulum genelindeki, yeniden başlatmadan değişen ayarlar. Okumak settings.view, değiştirmek
// settings.manage ister (varsayılan olarak yalnızca süper admin).
export function SettingsPage() {
  const { can } = useAuth()
  const canEdit = can('settings.manage')
  const ids = NAV.map((n) => n.id)
  const [section, setSection] = useTab(ids, ids[0], SECTION_PARAM)
  const [settings, setSettings] = useState<SettingsResponse | null>(null)
  const [error, setError] = useState<string | null>(null)

  useEffect(() => {
    settingsApi
      .get()
      .then(setSettings)
      .catch((err) => setError(message(err, 'ayarlar yüklenemedi')))
  }, [])

  const panel = (id: string, children: ReactNode) => (
    <div id={`settings-${id}`} hidden={section !== id} className="settings-panel">
      {children}
    </div>
  )

  return (
    <div>
      <PageHeader title="Ayarlar" subtitle="Kurulum genelindeki ayarlar; değişiklikler yeniden başlatmadan uygulanır" />
      {error && <div className="error-banner">{error}</div>}
      <div className="settings-layout">
        <nav className="settings-nav" aria-label="Ayar bölümleri">
          {NAV.map((n) => (
            <button
              key={n.id}
              type="button"
              className={`settings-nav-item${n.id === section ? ' active' : ''}`}
              aria-current={n.id === section ? 'page' : undefined}
              onClick={() => setSection(n.id)}
            >
              <n.icon size={15} strokeWidth={1.75} />
              {n.label}
            </button>
          ))}
        </nav>
        <div className="settings-content">
          {panel('sahipler', <OwnersSection canEdit={canEdit} />)}
          {panel('kanallar', <ChannelsSection canEdit={canEdit} />)}
          {settings &&
            SETTINGS_SECTIONS.map((s) => (
              <div key={s.id} id={`settings-${s.id}`} hidden={section !== s.id} className="settings-panel">
                <GeneralSection section={s} data={settings} canEdit={canEdit} onSaved={setSettings} />
              </div>
            ))}
        </div>
      </div>
    </div>
  )
}

// ---------------------------------------------------------------- genel ayarlar

function GeneralSection({
  section,
  data,
  canEdit,
  onSaved,
}: {
  section: SectionDef
  data: SettingsResponse
  canEdit: boolean
  onSaved: (s: SettingsResponse) => void
}) {
  const initial = useCallback(
    () => Object.fromEntries(section.fields.map((f) => [f.field, toDisplay(f, data.values[f.field])])) as Partial<Record<SettingsField, string>>,
    [section, data],
  )
  const [draft, setDraft] = useState(initial)
  const [errors, setErrors] = useState<Partial<Record<string, string>>>({})
  const [error, setError] = useState<string | null>(null)
  const [saved, setSaved] = useState(false)
  const [busy, setBusy] = useState(false)

  // Kaydedilen (server'ın normalleştirdiği) değerler gelince form onları gösterir.
  const [shown, setShown] = useState(data)
  if (shown !== data) {
    setShown(data)
    setDraft(initial())
  }

  async function run(action: () => Promise<SettingsResponse>) {
    setBusy(true)
    setError(null)
    setSaved(false)
    try {
      onSaved(await action())
      setErrors({})
      setSaved(true)
    } catch (err) {
      if (err instanceof ApiError && err.fields) {
        const shownErrors: Record<string, string> = {}
        for (const [f, m] of Object.entries(err.fields)) shownErrors[f] = fieldMessage(section.fields.find((d) => d.field === f), m)
        setErrors(shownErrors)
        const first = Object.values(shownErrors)[0]
        setError(first ?? message(err, 'ayarlar kaydedilemedi'))
      } else {
        setError(message(err, 'ayarlar kaydedilemedi'))
      }
    } finally {
      setBusy(false)
    }
  }

  function handleSave(e: FormEvent) {
    e.preventDefault()
    const { patch, errors: bad } = buildPatch(section, data.values, draft)
    if (Object.keys(bad).length > 0) {
      setErrors(bad)
      return
    }
    if (Object.keys(patch).length === 0) {
      setSaved(true)
      return
    }
    void run(() => settingsApi.update(patch))
  }

  return (
    <form className="card form-card settings-card" onSubmit={handleSave}>
      <h2 className="card-title">{section.title}</h2>
      <p className="card-desc">{section.description}</p>
      {error && <div className="error-banner">{error}</div>}
      {section.fields.map((f) => {
        const changed = data.changed.includes(f.field)
        const id = `setting-${f.field}`
        return (
          <div className="form-row" key={f.field}>
            <div className="setting-head">
              <label htmlFor={id}>{f.label}</label>
              {changed && <StatusBadge tone="neutral">değiştirildi</StatusBadge>}
            </div>
            <div className="input-with-unit">
              {f.kind === 'select' ? (
                <select id={id} value={draft[f.field] ?? ''} disabled={!canEdit} onChange={(e) => setDraft({ ...draft, [f.field]: e.target.value })}>
                  {f.options?.map((o) => (
                    <option key={o.value} value={o.value}>
                      {o.label}
                    </option>
                  ))}
                </select>
              ) : (
                <input
                  id={id}
                  value={draft[f.field] ?? ''}
                  disabled={!canEdit}
                  inputMode={f.kind === 'number' ? 'decimal' : undefined}
                  placeholder={f.placeholder}
                  onChange={(e) => setDraft({ ...draft, [f.field]: e.target.value })}
                />
              )}
              {f.unit && <span className="muted">{f.unit}</span>}
            </div>
            {errors[f.field] && <p className="field-error flush">{errors[f.field]}</p>}
            <p className="form-hint">
              {f.hint} <span className="muted">({defaultText(f, data.defaults[f.field])})</span>
              {canEdit && changed && (
                <button type="button" className="link-btn" disabled={busy} onClick={() => run(() => settingsApi.reset([f.field]))}>
                  <RotateCcw size={12} strokeWidth={2} />
                  Varsayılana dön
                </button>
              )}
            </p>
          </div>
        )
      })}
      {canEdit && (
        <div className="form-actions">
          <button className="btn btn-primary" type="submit" disabled={busy}>
            <Save size={15} strokeWidth={1.9} />
            Kaydet
          </button>
          {saved && <span className="muted save-note">Kaydedildi.</span>}
        </div>
      )}
      {data.updated_by && <p className="form-hint">Son değişiklik: {data.updated_by.name}, {when(data.updated_at)}</p>}
    </form>
  )
}

// ---------------------------------------------------------------- bildirim kanalları

function ChannelsSection({ canEdit }: { canEdit: boolean }) {
  const [channels, setChannels] = useState<ChannelInfo[] | null>(null)
  const [error, setError] = useState<string | null>(null)

  const load = useCallback(() => {
    channelsApi
      .list()
      .then(setChannels)
      .catch((err) => setError(message(err, 'bildirim kanalları yüklenemedi')))
  }, [])
  useEffect(load, [load])

  return (
    <div className="stack-col">
      {error && <div className="error-banner">{error}</div>}
      {channels?.map((ch) =>
        ch.provider === 'smtp' ? (
          <EmailChannelCard
            key={ch.channel}
            channel={ch}
            canEdit={canEdit}
            onChanged={(updated) => {
              setChannels((all) => all?.map((c) => (c.channel === updated.channel ? updated : c)) ?? null)
              announce()
            }}
            reload={load}
          />
        ) : null,
      )}
    </div>
  )
}

function EmailChannelCard({
  channel,
  canEdit,
  onChanged,
  reload,
}: {
  channel: ChannelInfo
  canEdit: boolean
  onChanged: (c: ChannelInfo) => void
  reload: () => void
}) {
  const { user } = useAuth()
  const [host, setHost] = useState(channel.config.host)
  const [port, setPort] = useState(String(channel.config.port))
  const [username, setUsername] = useState(channel.config.username)
  const [from, setFrom] = useState(channel.config.from)
  const [secret, setSecret] = useState('')
  const [clearSecret, setClearSecret] = useState(false)
  const [ownerLevel, setOwnerLevel] = useState<AlertLevel>(channel.owner_min_level)
  const [enabled, setEnabled] = useState(channel.enabled)
  const [testTo, setTestTo] = useState(user?.email ?? '')
  const [errors, setErrors] = useState<Record<string, string>>({})
  const [error, setError] = useState<string | null>(null)
  const [note, setNote] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)
  const status = channelStatus(channel)

  // Kaydedilen (server'ın normalleştirdiği) ayar gelince form onu gösterir; bildirim mesajı korunur.
  const [shown, setShown] = useState(channel.updated_at)
  if (shown !== channel.updated_at) {
    setShown(channel.updated_at)
    setHost(channel.config.host)
    setPort(String(channel.config.port))
    setUsername(channel.config.username)
    setFrom(channel.config.from)
    setSecret('')
    setClearSecret(false)
    setOwnerLevel(channel.owner_min_level)
    setEnabled(channel.enabled)
  }

  async function handleSave(e: FormEvent) {
    e.preventDefault()
    setBusy(true)
    setError(null)
    setNote(null)
    try {
      const updated = await channelsApi.update(channel.channel, {
        enabled,
        owner_min_level: ownerLevel,
        config: { host: host.trim(), port: Number(port), username: username.trim(), from: from.trim() },
        ...(clearSecret ? { secret: '' } : secret !== '' ? { secret } : {}),
      })
      setErrors({})
      setNote('Kaydedildi.')
      onChanged(updated)
    } catch (err) {
      if (err instanceof ApiError && err.fields) setErrors(err.fields)
      setError(message(err, 'kanal kaydedilemedi'))
    } finally {
      setBusy(false)
    }
  }

  async function handleTest() {
    setBusy(true)
    setError(null)
    setNote(null)
    try {
      await channelsApi.test(channel.channel, testTo)
      setNote(`Deneme e-postası ${testTo} adresine gönderildi.`)
      reload()
    } catch (err) {
      if (err instanceof ApiError && err.fields) setErrors(err.fields)
      setError(message(err, 'deneme gönderilemedi'))
    } finally {
      setBusy(false)
    }
  }

  const field = (key: string) => errors[key] && <p className="field-error flush">{errors[key]}</p>

  return (
    <form className="card form-card settings-card" onSubmit={handleSave}>
      <h2 className="card-title">
        <Mail size={16} strokeWidth={1.75} />
        E-posta (SMTP)
        <StatusBadge tone={status.tone}>{status.label}</StatusBadge>
      </h2>
      <p className="card-desc">
        Alert bildirimleri ve “Şifremi unuttum” e-postaları bu sunucu üzerinden gönderilir. Kaydettikten sonra “Deneme gönder” ile ayarı doğrulayın.
      </p>
      {channel.rule_count > 0 && !channel.enabled && (
        <div className="notice">Bu kanalı kullanan {channel.rule_count} bildirim kuralı var; kanal kapalıyken çalışmıyorlar.</div>
      )}
      {error && <div className="error-banner">{error}</div>}
      {note && <div className="notice notice-info">{note}</div>}

      <label className="check-row">
        <input type="checkbox" checked={enabled} disabled={!canEdit} onChange={(e) => setEnabled(e.target.checked)} />
        Kanal açık
      </label>
      {field('enabled')}
      <div className="form-row">
        <label htmlFor="smtp-host">SMTP sunucusu</label>
        <input id="smtp-host" value={host} disabled={!canEdit} placeholder="smtp.example.com" onChange={(e) => setHost(e.target.value)} />
        {field('config.host')}
      </div>
      <div className="form-row">
        <label htmlFor="smtp-port">Port</label>
        <input id="smtp-port" value={port} disabled={!canEdit} inputMode="numeric" onChange={(e) => setPort(e.target.value)} />
        <p className="form-hint">465 örtük TLS; diğer portlarda sunucu sunuyorsa STARTTLS kullanılır.</p>
        {field('config.port')}
      </div>
      <div className="form-row">
        <label htmlFor="smtp-from">Gönderen adres</label>
        <input id="smtp-from" value={from} disabled={!canEdit} placeholder="healthbeat@example.com" onChange={(e) => setFrom(e.target.value)} />
        {field('config.from')}
      </div>
      <div className="form-row">
        <label htmlFor="smtp-user">Kullanıcı adı</label>
        <input id="smtp-user" value={username} disabled={!canEdit} autoComplete="off" onChange={(e) => setUsername(e.target.value)} />
        <p className="form-hint">Boşsa kimlik doğrulama yapılmaz.</p>
        {field('config.username')}
      </div>
      {canEdit && (
        <div className="form-row">
          <label htmlFor="smtp-secret">Şifre</label>
          <input
            id="smtp-secret"
            type="password"
            value={secret}
            autoComplete="new-password"
            disabled={clearSecret}
            placeholder={channel.secret_set ? '•••• kayıtlı (değiştirmek için yazın)' : ''}
            onChange={(e) => setSecret(e.target.value)}
          />
          {channel.secret_set && (
            <label className="check-row">
              <input type="checkbox" checked={clearSecret} onChange={(e) => setClearSecret(e.target.checked)} />
              Kayıtlı şifreyi sil
            </label>
          )}
          {field('secret')}
        </div>
      )}
      <div className="form-row">
        <label htmlFor="smtp-owner-level">Sistem sahiplerine</label>
        <select id="smtp-owner-level" value={ownerLevel} disabled={!canEdit} onChange={(e) => setOwnerLevel(e.target.value as AlertLevel)}>
          {LEVELS.map((l) => (
            <option key={l} value={l}>
              {alertLevelLabel(l)} ve üstü
            </option>
          ))}
        </select>
        <p className="form-hint">Organizasyon ve sunucu kurallarındaki ek alıcılar kendi seviyelerini kullanır.</p>
      </div>
      {canEdit && (
        <div className="form-actions">
          <button className="btn btn-primary" type="submit" disabled={busy}>
            <Save size={15} strokeWidth={1.9} />
            Kaydet
          </button>
        </div>
      )}

      <hr className="divider" />
      <div className="form-row">
        <label htmlFor="smtp-test-to">Deneme gönder</label>
        <div className="input-with-unit">
          <input id="smtp-test-to" type="email" value={testTo} disabled={!canEdit} onChange={(e) => setTestTo(e.target.value)} />
          {canEdit && (
            <button className="btn" type="button" disabled={busy || testTo === '' || !channel.ready} onClick={handleTest}>
              <Send size={15} strokeWidth={1.9} />
              Gönder
            </button>
          )}
        </div>
        {field('to')}
        <p className="form-hint">
          Kayıtlı ayarla, kanal kapalıyken de gönderilir. Son başarılı deneme: {when(channel.verified_at)}
          {channel.rule_count > 0 && ` · ${channel.rule_count} kural bu kanalı kullanıyor`}
        </p>
      </div>
    </form>
  )
}

// ---------------------------------------------------------------- sistem sahipleri

const emptyOwner: OwnerInput = { name: '', email: '', phone: '', email_enabled: true, sms_enabled: true }

function OwnersSection({ canEdit }: { canEdit: boolean }) {
  const [owners, setOwners] = useState<NotificationOwner[] | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [editing, setEditing] = useState<{ id: string | null; input: OwnerInput } | null>(null)
  const [formError, setFormError] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)

  const load = useCallback(() => {
    ownersApi
      .list()
      .then(setOwners)
      .catch((err) => setError(message(err, 'sistem sahipleri yüklenemedi')))
  }, [])
  useEffect(load, [load])

  async function handleSave(e: FormEvent) {
    e.preventDefault()
    if (!editing) return
    setBusy(true)
    setFormError(null)
    const input: OwnerInput = {
      ...editing.input,
      email: editing.input.email?.trim() ? editing.input.email.trim() : null,
      phone: editing.input.phone?.trim() ? editing.input.phone.trim() : null,
    }
    try {
      if (editing.id) await ownersApi.update(editing.id, input)
      else await ownersApi.create(input)
      setEditing(null)
      load()
      announce()
    } catch (err) {
      setFormError(message(err, 'sistem sahibi kaydedilemedi'))
    } finally {
      setBusy(false)
    }
  }

  async function handleDelete(o: NotificationOwner) {
    if (!confirm(`${o.name} sistem sahiplerinden çıkarılsın mı? Artık alert bildirimi almaz.`)) return
    try {
      await ownersApi.remove(o.id)
      load()
      announce()
    } catch (err) {
      setError(message(err, 'sistem sahibi silinemedi'))
    }
  }

  const set = (patch: Partial<OwnerInput>) => editing && setEditing({ ...editing, input: { ...editing.input, ...patch } })

  return (
    <div className="card table-card">
      <div className="card-head">
        <div>
          <h2 className="card-title">Sistem sahipleri</h2>
          <p className="card-desc">
            Her alert bu kişilere gider (kanalın sahip seviyesinden itibaren). Panel kullanıcısı olmaları gerekmez; organizasyon ve sunucu kuralları
            bunlara ek alıcı ekler.
          </p>
        </div>
        {canEdit && (
          <button className="btn btn-primary" type="button" onClick={() => { setFormError(null); setEditing({ id: null, input: emptyOwner }) }}>
            <Plus size={15} strokeWidth={1.9} />
            Sahip ekle
          </button>
        )}
      </div>
      {error && <div className="error-banner">{error}</div>}
      <table className="stack">
        <thead>
          <tr>
            <th>Ad</th>
            <th>E-posta</th>
            <th>Telefon</th>
            {canEdit && <th className="actions" />}
          </tr>
        </thead>
        <tbody>
          {owners?.map((o) => (
            <tr key={o.id}>
              <td className="primary">{o.name}</td>
              <td data-label="E-posta">
                {o.email ?? '—'} {o.email && !o.email_enabled && <StatusBadge tone="neutral">almıyor</StatusBadge>}
              </td>
              <td data-label="Telefon">
                {o.phone ?? '—'} {o.phone && !o.sms_enabled && <StatusBadge tone="neutral">almıyor</StatusBadge>}
              </td>
              {canEdit && (
                <td className="actions">
                  <button className="btn btn-sm" type="button" onClick={() => { setFormError(null); setEditing({ id: o.id, input: { name: o.name, email: o.email ?? '', phone: o.phone ?? '', email_enabled: o.email_enabled, sms_enabled: o.sms_enabled } }) }}>
                    <Pencil size={14} strokeWidth={1.9} />
                    Düzenle
                  </button>
                  <button className="btn btn-sm btn-danger" type="button" onClick={() => handleDelete(o)}>
                    <Trash2 size={14} strokeWidth={1.9} />
                    Sil
                  </button>
                </td>
              )}
            </tr>
          ))}
          {owners?.length === 0 && (
            <tr>
              <td colSpan={canEdit ? 4 : 3} className="empty-cell">
                <EmptyState icon={Users}>Sistem sahibi yok — alert bildirimleri yalnızca kurallardaki ek alıcılara gider.</EmptyState>
              </td>
            </tr>
          )}
        </tbody>
      </table>

      <Modal open={editing !== null} title={editing?.id ? 'Sistem sahibini düzenle' : 'Sistem sahibi ekle'} onClose={() => setEditing(null)}>
        {editing && (
          <form onSubmit={handleSave}>
            {formError && <div className="error-banner">{formError}</div>}
            <div className="form-row">
              <label htmlFor="owner-name">Ad</label>
              <input id="owner-name" value={editing.input.name} maxLength={200} required onChange={(e) => set({ name: e.target.value })} />
              <p className="form-hint">Kişi ya da ortak adres (ör. “NOC masası”).</p>
            </div>
            <div className="form-row">
              <label htmlFor="owner-email">E-posta</label>
              <input id="owner-email" type="email" value={editing.input.email ?? ''} onChange={(e) => set({ email: e.target.value })} />
              <label className="check-row">
                <input type="checkbox" checked={editing.input.email_enabled} onChange={(e) => set({ email_enabled: e.target.checked })} />
                E-posta bildirimi alsın
              </label>
            </div>
            <div className="form-row">
              <label htmlFor="owner-phone">Telefon</label>
              <input id="owner-phone" value={editing.input.phone ?? ''} inputMode="tel" placeholder="+905551234567" onChange={(e) => set({ phone: e.target.value })} />
              <label className="check-row">
                <input type="checkbox" checked={editing.input.sms_enabled} onChange={(e) => set({ sms_enabled: e.target.checked })} />
                SMS bildirimi alsın (SMS kanalı eklendiğinde)
              </label>
            </div>
            <p className="form-hint" style={{ margin: '0 0 14px' }}>
              E-posta ya da telefondan en az biri gerekli.
            </p>
            <div className="form-actions">
              <button className="btn btn-primary" type="submit" disabled={busy}>
                <Save size={15} strokeWidth={1.9} />
                Kaydet
              </button>
              <button className="btn" type="button" onClick={() => setEditing(null)}>
                Vazgeç
              </button>
            </div>
          </form>
        )}
      </Modal>
    </div>
  )
}
