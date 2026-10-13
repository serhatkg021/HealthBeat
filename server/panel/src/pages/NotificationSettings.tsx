import { useCallback, useEffect, useState, type ComponentProps, type FormEvent, type ReactNode } from 'react'
import { Braces, Mail, Pencil, Plus, Save, Send, Smartphone, Trash2, Users } from 'lucide-react'
import { RowMenu } from '../components/RowMenu'
import { ApiError } from '../api/client'
import { channelsApi, systemContactsApi } from '../api/endpoints'
import { useAuth } from '../auth/AuthContext'
import { SlackLogo } from '../components/BrandIcons'
import { EmptyState } from '../components/EmptyState'
import { FieldLabel } from '../components/FieldLabel'
import { InfoTip } from '../components/InfoTip'
import { Modal } from '../components/Modal'
import { StatusBadge } from '../components/StatusBadge'
import type { ChannelInfo, ContactInput, SystemContact } from '../types/api'
import { SETTINGS_CHANGED, channelStatus } from './settingsForm'
import { formatDateTime } from './datetime'


const message = (err: unknown, fallback: string) => (err instanceof Error ? err.message : fallback)
const when = (iso: string | null | undefined) => formatDateTime(iso)
const announce = () => window.dispatchEvent(new Event(SETTINGS_CHANGED))

// Bildirim sayfasının Kanallar sekmesi ve Kişiler sekmesindeki sistem kişileri. Okumak settings.view, değiştirmek
// settings.manage ister (varsayılan olarak yalnızca süper admin).

// ---------------------------------------------------------------- bildirim kanalları

// Kanallar 2 sütunlu kart ızgarasıdır; her kart aynı iskelettedir (ChannelCard): simge, ad ve açıklama, durum; iki satırlık
// özet; altta işlemler. Gerçek kanallar (şimdilik e-posta) ayarını bir pencerede düzenler; henüz gelmemiş kanallar
// "Yakında" kartıdır (soluk, örnek satırla, işlemsiz).
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

  const email = channels?.find((c) => c.provider === 'smtp')
  return (
    <div>
      {error && <div className="error-banner">{error}</div>}
      <div className="channel-grid">
        {email && (
          <EmailChannelCard
            channel={email}
            canEdit={canEdit}
            onChanged={(updated) => {
              setChannels((all) => all?.map((c) => (c.channel === updated.channel ? updated : c)) ?? null)
              announce()
            }}
            reload={load}
          />
        )}
        <ChannelCard
          soon
          icon={<SlackLogo />}
          brand
          title="Slack"
          description="Kanal webhook’u ile alert iletisi; Mattermost ve Rocket.Chat de aynı biçimle çalışır."
          lines={['Örnek: #ops · #geliştirici', 'Bir kanala birden çok hedef eklenebilecek']}
        />
        <ChannelCard
          soon
          icon={<Smartphone size={19} strokeWidth={1.75} />}
          title="SMS"
          description="Telefonu olan kişilere kısa ileti."
          lines={['Bir SMS sağlayıcısı seçilince açılır', 'Örnek: Netgsm · gönderen HEALTHBEAT']}
        />
        <ChannelCard
          soon
          icon={<Braces size={19} strokeWidth={1.75} />}
          title="Özel webhook"
          description="Kendi sisteminize (ticket, n8n…) JSON gövdeli POST isteği."
          lines={['HMAC ile imzalı (X-HealthBeat-Signature)', 'Örnek: https://hooks.example.com/…']}
        />
      </div>
    </div>
  )
}

// ChannelCard, bir kanal kartının iskeletidir. soon: henüz gelmemiş kanal ("Yakında" rozeti, soluk, işlemsiz; içerik
// yalnızca anlatım ve örnektir, gerçek veri değildir). brand: simge hizmetin kendi renkli logosudur (nötr zeminde).
function ChannelCard({
  icon,
  brand,
  title,
  info,
  description,
  status,
  soon,
  lines,
  children,
  actions,
}: {
  icon: ReactNode
  brand?: boolean
  title: string
  info?: ReactNode
  description: string
  status?: { label: string; tone: ComponentProps<typeof StatusBadge>['tone'] }
  soon?: boolean
  lines: ReactNode[]
  children?: ReactNode
  actions?: ReactNode
}) {
  return (
    <section className={`card channel-card${soon ? ' channel-card-soon' : ''}`} aria-label={soon ? `${title} (yakında)` : title}>
      <div className="channel-card-head">
        <span className={`channel-icon${brand ? ' channel-icon-brand' : ''}`} aria-hidden="true">
          {icon}
        </span>
        <div className="channel-card-heading">
          <h2 className="channel-card-title">
            {title}
            {info}
          </h2>
          <p className="channel-card-desc">{description}</p>
        </div>
        {soon ? <span className="coming-soon-badge">Yakında</span> : status && <StatusBadge tone={status.tone}>{status.label}</StatusBadge>}
      </div>
      <div className="channel-card-body">
        {children}
        {lines.map((line, i) => (
          <p key={i} className={i === 0 ? 'channel-line' : 'channel-line channel-line-meta'}>
            {line}
          </p>
        ))}
      </div>
      <div className="channel-card-foot">{soon ? <span className="muted">Henüz kullanılamıyor</span> : actions}</div>
    </section>
  )
}

// EmailChannelCard, e-posta kanalının kartıdır: sunucu ve gönderen, son başarılı deneme ve kanalı kullanan kural sayısı.
// Ayar "Düzenle" penceresinde değişir, deneme gönderimi kendi penceresindedir.
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
  const [editing, setEditing] = useState(false)
  const [testing, setTesting] = useState(false)
  const [note, setNote] = useState<string | null>(null)
  const { host, port, from } = channel.config
  const rules = channel.rule_count > 0 ? `${channel.rule_count} kural kullanıyor` : 'kullanan kural yok'

  return (
    <ChannelCard
      icon={<Mail size={19} strokeWidth={1.75} />}
      title="E-posta (SMTP)"
      info={
        <InfoTip label="E-posta (SMTP)">
          Alert bildirimleri ve “Şifremi unuttum” e-postaları bu sunucu üzerinden gönderilir. Kimin hangi seviyeden alacağı bildirim
          kurallarındadır. Ayarı kaydettikten sonra “Deneme gönder” ile doğrulayın.
        </InfoTip>
      }
      description="Alert bildirimleri ve hesap e-postaları."
      status={channelStatus(channel)}
      lines={[
        <>
          <span className="mono">{host ? `${host}:${port}` : 'SMTP sunucusu girilmedi'}</span>
          {from && <> · {from}</>}
        </>,
        `${channel.verified_at ? `Son deneme başarılı · ${when(channel.verified_at)}` : 'Henüz deneme gönderilmedi'} · ${rules}`,
      ]}
      actions={
        canEdit && (
          <>
            <button
              className="btn btn-sm"
              type="button"
              disabled={!channel.ready}
              title={channel.ready ? undefined : 'Önce sunucu ve gönderen adresi girilmeli'}
              onClick={() => { setNote(null); setTesting(true) }}
            >
              <Send size={14} strokeWidth={1.9} />
              Deneme gönder
            </button>
            <button className="btn btn-sm btn-primary" type="button" onClick={() => { setNote(null); setEditing(true) }}>
              <Pencil size={14} strokeWidth={1.9} />
              Düzenle
            </button>
          </>
        )
      }
    >
      {channel.secret_unreadable && (
        <div className="notice">Kayıtlı şifre çözülemiyor (şifreleme anahtarı değişmiş); yeniden girilene kadar e-posta gönderilemez.</div>
      )}
      {channel.rule_count > 0 && !channel.enabled && (
        <div className="notice">Bu kanalı kullanan {channel.rule_count} kural var; kanal kapalıyken çalışmıyorlar.</div>
      )}
      {note && <div className="notice notice-info">{note}</div>}

      <Modal open={editing} title="E-posta (SMTP) ayarı" onClose={() => setEditing(false)}>
        {editing && (
          <EmailChannelForm
            channel={channel}
            onCancel={() => setEditing(false)}
            onSaved={(updated) => {
              onChanged(updated)
              setEditing(false)
              setNote('Kaydedildi. Ayarı “Deneme gönder” ile doğrulayın.')
            }}
          />
        )}
      </Modal>
      <Modal open={testing} size="sm" title="Deneme e-postası gönder" onClose={() => setTesting(false)}>
        {testing && (
          <EmailTestForm
            channel={channel}
            onCancel={() => setTesting(false)}
            onSent={(to) => {
              setTesting(false)
              setNote(`Deneme e-postası ${to} adresine gönderildi.`)
              reload()
            }}
          />
        )}
      </Modal>
    </ChannelCard>
  )
}

// EmailChannelForm, e-posta kanalının ayar formudur (pencerede): açık/kapalı, SMTP sunucusu, port, gönderen, kullanıcı
// adı ve şifre. Server'ın doğrulama hataları alanın altında gösterilir.
function EmailChannelForm({ channel, onSaved, onCancel }: { channel: ChannelInfo; onSaved: (c: ChannelInfo) => void; onCancel: () => void }) {
  const [host, setHost] = useState(channel.config.host)
  const [port, setPort] = useState(String(channel.config.port))
  const [username, setUsername] = useState(channel.config.username)
  const [from, setFrom] = useState(channel.config.from)
  const [secret, setSecret] = useState('')
  const [clearSecret, setClearSecret] = useState(false)
  const [enabled, setEnabled] = useState(channel.enabled)
  const [errors, setErrors] = useState<Record<string, string>>({})
  const [error, setError] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)

  async function handleSave(e: FormEvent) {
    e.preventDefault()
    setBusy(true)
    setError(null)
    try {
      const updated = await channelsApi.update(channel.channel, {
        enabled,
        config: { host: host.trim(), port: Number(port), username: username.trim(), from: from.trim() },
        ...(clearSecret ? { secret: '' } : secret !== '' ? { secret } : {}),
      })
      onSaved(updated)
    } catch (err) {
      if (err instanceof ApiError && err.fields) setErrors(err.fields)
      setError(message(err, 'kanal kaydedilemedi'))
    } finally {
      setBusy(false)
    }
  }

  const field = (key: string) => errors[key] && <p className="field-error flush">{errors[key]}</p>

  return (
    <form onSubmit={handleSave}>
      {channel.secret_unreadable && (
        <div className="notice">
          Kayıtlı şifre çözülemiyor: server’ın şifreleme anahtarı (SECRETS_ENCRYPTION_KEY) şifre kaydedildikten sonra değişmiş. Şifre yeniden
          girilene kadar e-posta gönderilemez; bildirimler kuyrukta yeniden denenir. Şifreyi girip kaydedin (SMTP sunucunuz şifre istemiyorsa
          “Kayıtlı şifreyi sil”i işaretleyin).
        </div>
      )}
      {error && <div className="error-banner">{error}</div>}
      <label className="check-row">
        <input type="checkbox" checked={enabled} onChange={(e) => setEnabled(e.target.checked)} />
        Kanal açık
      </label>
      {field('enabled')}
      <div className="form-row">
        <label htmlFor="smtp-host">SMTP sunucusu</label>
        <input id="smtp-host" value={host} placeholder="smtp.example.com" onChange={(e) => setHost(e.target.value)} />
        {field('config.host')}
      </div>
      <div className="form-row">
        <FieldLabel htmlFor="smtp-port" label="Port" hint="465 örtük TLS; diğer portlarda sunucu sunuyorsa STARTTLS kullanılır." />
        <input id="smtp-port" value={port} inputMode="numeric" onChange={(e) => setPort(e.target.value)} />
        {field('config.port')}
      </div>
      <div className="form-row">
        <label htmlFor="smtp-from">Gönderen adres</label>
        <input id="smtp-from" value={from} placeholder="healthbeat@example.com" onChange={(e) => setFrom(e.target.value)} />
        {field('config.from')}
      </div>
      <div className="form-row">
        <FieldLabel htmlFor="smtp-user" label="Kullanıcı adı" hint="Boşsa kimlik doğrulama yapılmaz." />
        <input id="smtp-user" value={username} autoComplete="off" onChange={(e) => setUsername(e.target.value)} />
        {field('config.username')}
      </div>
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
      <div className="form-actions">
        <button className="btn btn-primary" type="submit" disabled={busy}>
          <Save size={15} strokeWidth={1.9} />
          Kaydet
        </button>
        <button className="btn" type="button" onClick={onCancel}>
          Vazgeç
        </button>
      </div>
    </form>
  )
}

// EmailTestForm, kayıtlı ayarla bir deneme e-postası gönderir (kanal kapalıyken de); varsayılan alıcı oturumdaki kullanıcı.
function EmailTestForm({ channel, onSent, onCancel }: { channel: ChannelInfo; onSent: (to: string) => void; onCancel: () => void }) {
  const { user } = useAuth()
  const [to, setTo] = useState(user?.email ?? '')
  const [error, setError] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)

  async function handleSend(e: FormEvent) {
    e.preventDefault()
    setBusy(true)
    setError(null)
    try {
      await channelsApi.test(channel.channel, to.trim())
      onSent(to.trim())
    } catch (err) {
      setError(err instanceof ApiError && err.fields?.to ? err.fields.to : message(err, 'deneme gönderilemedi'))
    } finally {
      setBusy(false)
    }
  }

  return (
    <form onSubmit={handleSend}>
      {error && <div className="error-banner">{error}</div>}
      <div className="form-row">
        <label htmlFor="smtp-test-to">Alıcı</label>
        <input id="smtp-test-to" type="email" value={to} required onChange={(e) => setTo(e.target.value)} />
        <p className="form-hint">Kayıtlı ayarla, kanal kapalıyken de gönderilir.</p>
      </div>
      <div className="form-actions">
        <button className="btn btn-primary" type="submit" disabled={busy || to.trim() === ''}>
          <Send size={15} strokeWidth={1.9} />
          Gönder
        </button>
        <button className="btn" type="button" onClick={onCancel}>
          Vazgeç
        </button>
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
            <tr key={c.id} className={canEdit ? 'menu-row' : undefined}>
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
                  <RowMenu
                    label={c.name}
                    items={[
                      { label: 'Düzenle', icon: Pencil, onSelect: () => { setFormError(null); setEditing({ id: c.id, input: { name: c.name, email: c.email ?? '', phone: c.phone ?? '' } }) } },
                      { label: 'Sil', icon: Trash2, danger: true, separated: true, onSelect: () => setDeleting(c) },
                    ]}
                  />
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
