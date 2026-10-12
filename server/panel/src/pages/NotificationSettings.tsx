import { useCallback, useEffect, useState, type FormEvent } from 'react'
import { Mail, Pencil, Plus, Save, Send, Trash2, Users } from 'lucide-react'
import { ApiError } from '../api/client'
import { channelsApi, systemContactsApi } from '../api/endpoints'
import { useAuth } from '../auth/AuthContext'
import { EmptyState } from '../components/EmptyState'
import { FieldLabel } from '../components/FieldLabel'
import { InfoTip } from '../components/InfoTip'
import { Modal } from '../components/Modal'
import { StatusBadge } from '../components/StatusBadge'
import type { ChannelInfo, ContactInput, SystemContact } from '../types/api'
import { SETTINGS_CHANGED, channelStatus } from './settingsForm'


const message = (err: unknown, fallback: string) => (err instanceof Error ? err.message : fallback)
const when = (iso: string | null | undefined) => (iso ? new Date(iso).toLocaleString() : '—')
const announce = () => window.dispatchEvent(new Event(SETTINGS_CHANGED))

// Bildirim sayfasının Kanallar sekmesi ve Kişiler sekmesindeki sistem kişileri. Okumak settings.view, değiştirmek
// settings.manage ister (varsayılan olarak yalnızca süper admin).

// ---------------------------------------------------------------- bildirim kanalları

export function ChannelsSection({ canEdit }: { canEdit: boolean }) {
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
        <InfoTip label="E-posta (SMTP)">
          Alert bildirimleri ve “Şifremi unuttum” e-postaları bu sunucu üzerinden gönderilir. Kimin hangi seviyeden alacağı bildirim
          kurallarındadır. Kaydettikten sonra “Deneme gönder” ile ayarı doğrulayın.
        </InfoTip>
        <StatusBadge tone={status.tone}>{status.label}</StatusBadge>
      </h2>
      {channel.secret_unreadable && (
        <div className="notice">
          Kayıtlı şifre çözülemiyor: server’ın şifreleme anahtarı (SECRETS_ENCRYPTION_KEY) şifre kaydedildikten sonra değişmiş. Şifre yeniden
          girilene kadar e-posta gönderilemez; bildirimler kuyrukta yeniden denenir. Şifreyi girip kaydedin (SMTP sunucunuz şifre istemiyorsa
          “Kayıtlı şifreyi sil”i işaretleyin).
        </div>
      )}
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
        <FieldLabel htmlFor="smtp-port" label="Port" hint="465 örtük TLS; diğer portlarda sunucu sunuyorsa STARTTLS kullanılır." />
        <input id="smtp-port" value={port} disabled={!canEdit} inputMode="numeric" onChange={(e) => setPort(e.target.value)} />
        {field('config.port')}
      </div>
      <div className="form-row">
        <label htmlFor="smtp-from">Gönderen adres</label>
        <input id="smtp-from" value={from} disabled={!canEdit} placeholder="healthbeat@example.com" onChange={(e) => setFrom(e.target.value)} />
        {field('config.from')}
      </div>
      <div className="form-row">
        <FieldLabel htmlFor="smtp-user" label="Kullanıcı adı" hint="Boşsa kimlik doğrulama yapılmaz." />
        <input id="smtp-user" value={username} disabled={!canEdit} autoComplete="off" onChange={(e) => setUsername(e.target.value)} />
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
            placeholder={channel.secret_set ? '•••• kayıtlı (değiştirmek için yazın)' : channel.secret_unreadable ? 'çözülemiyor — yeniden girin' : ''}
            onChange={(e) => setSecret(e.target.value)}
          />
          {(channel.secret_set || channel.secret_unreadable) && (
            <label className="check-row">
              <input type="checkbox" checked={clearSecret} onChange={(e) => setClearSecret(e.target.checked)} />
              Kayıtlı şifreyi sil
            </label>
          )}
          {field('secret')}
        </div>
      )}
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

// ---------------------------------------------------------------- sistem kişileri

const emptyContact: ContactInput = { name: '', email: '', phone: '' }

// Sistem kişileri: organizasyonsuz kişiler ya da ortak adresler (ör. "NOC masası"). Panel hesabı gerekmez; bütün
// sunucuların bildirimlerini sistem kurallarıyla alırlar. Silinen kişinin kuralları da silinir.
export function SystemContactsSection({ canEdit }: { canEdit: boolean }) {
  const [contacts, setContacts] = useState<SystemContact[] | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [editing, setEditing] = useState<{ id: string | null; input: ContactInput } | null>(null)
  const [deleting, setDeleting] = useState<SystemContact | null>(null)
  const [formError, setFormError] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)

  const load = useCallback(() => {
    systemContactsApi
      .list()
      .then(setContacts)
      .catch((err) => setError(message(err, 'sistem kişileri yüklenemedi')))
  }, [])
  useEffect(load, [load])

  async function handleSave(e: FormEvent) {
    e.preventDefault()
    if (!editing) return
    setBusy(true)
    setFormError(null)
    const input: ContactInput = { name: editing.input.name.trim(), email: editing.input.email?.trim() ?? '', phone: editing.input.phone?.trim() ?? '' }
    try {
      if (editing.id) await systemContactsApi.update(editing.id, input)
      else await systemContactsApi.create(input)
      setEditing(null)
      load()
      announce()
    } catch (err) {
      setFormError(message(err, 'sistem kişisi kaydedilemedi'))
    } finally {
      setBusy(false)
    }
  }

  async function handleDelete() {
    if (!deleting) return
    setBusy(true)
    try {
      await systemContactsApi.remove(deleting.id)
      setDeleting(null)
      load()
      announce()
    } catch (err) {
      setError(message(err, 'sistem kişisi silinemedi'))
      setDeleting(null)
    } finally {
      setBusy(false)
    }
  }

  const set = (patch: Partial<ContactInput>) => editing && setEditing({ ...editing, input: { ...editing.input, ...patch } })

  return (
    <div className="card table-card">
      <div className="card-head">
        <div>
          <h2 className="card-title">
            Sistem kişileri
            <InfoTip label="Sistem kişileri">
              Bir organizasyona bağlı olmayan kişiler ya da ortak adresler (ör. “NOC masası”); panel hesabı gerekmez. Bütün sunucuların
              alert’lerini almaları için Bildirim kuralları → Sistem’de kural yazılır.
            </InfoTip>
          </h2>
        </div>
        {canEdit && (
          <button className="btn btn-primary" type="button" onClick={() => { setFormError(null); setEditing({ id: null, input: emptyContact }) }}>
            <Plus size={15} strokeWidth={1.9} />
            Kişi ekle
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
            <th>Kurallar</th>
            {canEdit && <th className="actions" />}
          </tr>
        </thead>
        <tbody>
          {contacts?.map((c) => (
            <tr key={c.id}>
              <td className="primary">{c.name}</td>
              <td data-label="E-posta">{c.email ?? <span className="muted">—</span>}</td>
              <td data-label="Telefon" className="tnum">
                {c.phone ?? <span className="muted">—</span>}
              </td>
              <td data-label="Kurallar">
                {c.rule_count > 0 ? c.rule_count : <StatusBadge tone="neutral">kural yok</StatusBadge>}
              </td>
              {canEdit && (
                <td className="actions">
                  <button className="btn btn-sm" type="button" onClick={() => { setFormError(null); setEditing({ id: c.id, input: { name: c.name, email: c.email ?? '', phone: c.phone ?? '' } }) }}>
                    <Pencil size={14} strokeWidth={1.9} />
                    Düzenle
                  </button>
                  <button className="btn btn-sm btn-danger" type="button" onClick={() => setDeleting(c)}>
                    <Trash2 size={14} strokeWidth={1.9} />
                    Sil
                  </button>
                </td>
              )}
            </tr>
          ))}
          {contacts?.length === 0 && (
            <tr>
              <td colSpan={canEdit ? 5 : 4} className="empty-cell">
                <EmptyState icon={Users}>Sistem kişisi yok.</EmptyState>
              </td>
            </tr>
          )}
        </tbody>
      </table>

      <Modal open={editing !== null} title={editing?.id ? 'Sistem kişisini düzenle' : 'Sistem kişisi ekle'} onClose={() => setEditing(null)}>
        {editing && (
          <form onSubmit={handleSave}>
            {formError && <div className="error-banner">{formError}</div>}
            <div className="form-row">
              <FieldLabel htmlFor="system-contact-name" label="Ad" hint="Kişi ya da ortak adres (ör. “NOC masası”)." />
              <input id="system-contact-name" value={editing.input.name} maxLength={200} required onChange={(e) => set({ name: e.target.value })} />
            </div>
            <div className="form-row">
              <label htmlFor="system-contact-email">E-posta</label>
              <input id="system-contact-email" type="email" value={editing.input.email ?? ''} onChange={(e) => set({ email: e.target.value })} />
            </div>
            <div className="form-row">
              <label htmlFor="system-contact-phone">Telefon</label>
              <input id="system-contact-phone" value={editing.input.phone ?? ''} inputMode="tel" placeholder="+905551234567" onChange={(e) => set({ phone: e.target.value })} />
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

      <Modal open={deleting !== null} size="sm" title="Sistem kişisini sil" onClose={() => setDeleting(null)}>
        {deleting && (
          <div>
            <p style={{ marginTop: 0 }}>
              <strong>{deleting.name}</strong> silinsin mi?
              {deleting.rule_count > 0
                ? ` Alıcı olduğu ${deleting.rule_count} bildirim kuralı da silinir; artık alert bildirimi almaz.`
                : ' Hiçbir bildirim kuralında alıcı değil.'}
            </p>
            <div className="form-actions">
              <button className="btn btn-danger" type="button" disabled={busy} onClick={handleDelete}>
                <Trash2 size={15} strokeWidth={1.9} />
                Sil
              </button>
              <button className="btn" type="button" onClick={() => setDeleting(null)}>
                Vazgeç
              </button>
            </div>
          </div>
        )}
      </Modal>
    </div>
  )
}
