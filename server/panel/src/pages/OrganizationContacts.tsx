import { useCallback, useEffect, useState, type FormEvent } from 'react'
import { Pencil, Plus, Save, Trash2, UserRound } from 'lucide-react'
import { Modal } from '../components/Modal'
import { RowMenu } from '../components/RowMenu'
import { contactsApi } from '../api/endpoints'
import type { OrganizationContact } from '../types/api'
import { EmptyState } from '../components/EmptyState'
import { draftFromContact, emptyContactDraft, managerChoices, managerName, toContactInput, validateContact, type ContactDraft } from './contacts'
import { MonoValue } from '../components/MonoValue'

// Organizasyonun iletişim kişileri: sunucu işleri için başvurulacak kişiler (panel kullanıcısı olmaları gerekmez).
// Bildirim kurallarında alıcı olarak seçilebilirler.
export function OrganizationContacts({ organizationId, canEdit }: { organizationId: string; canEdit: boolean }) {
  const [contacts, setContacts] = useState<OrganizationContact[] | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [editing, setEditing] = useState<{ id?: string; draft: ContactDraft } | null>(null)
  const [problems, setProblems] = useState<string[]>([])
  const [formError, setFormError] = useState<string | null>(null)
  const [deleting, setDeleting] = useState<OrganizationContact | null>(null)
  const [busy, setBusy] = useState(false)

  const load = useCallback(() => {
    contactsApi
      .list(organizationId)
      .then(setContacts)
      .catch((err) => setError(err instanceof Error ? err.message : 'iletişim kişileri yüklenemedi'))
  }, [organizationId])

  useEffect(load, [load])

  async function handleSave(e: FormEvent) {
    e.preventDefault()
    if (!editing) return
    const found = validateContact(editing.draft)
    setProblems(found)
    if (found.length > 0) return
    setBusy(true)
    setFormError(null)
    try {
      const input = toContactInput(editing.draft)
      if (editing.id) await contactsApi.update(editing.id, input)
      else await contactsApi.create(organizationId, input)
      setEditing(null)
      load()
    } catch (err) {
      setFormError(err instanceof Error ? err.message : 'kaydedilemedi')
    } finally {
      setBusy(false)
    }
  }

  async function handleDelete() {
    if (!deleting) return
    setBusy(true)
    try {
      await contactsApi.remove(deleting.id)
      setDeleting(null)
      load()
    } catch (err) {
      setError(err instanceof Error ? err.message : 'silinemedi')
      setDeleting(null)
    } finally {
      setBusy(false)
    }
  }

  function startEdit(id: string | undefined, draft: ContactDraft) {
    setProblems([])
    setFormError(null)
    setEditing({ id, draft })
  }

  const list = contacts ?? []
  const set = (patch: Partial<ContactDraft>) => setEditing((cur) => (cur ? { ...cur, draft: { ...cur.draft, ...patch } } : cur))

  return (
    <div>
      {error && <div className="error-banner">{error}</div>}
      <div className="toolbar">
        <span className="muted toolbar-note">Sunucu işleri için başvurulacak kişiler; bildirim kurallarında alıcı seçilebilirler.</span>
        {canEdit && (
          <button className="btn btn-primary" type="button" onClick={() => startEdit(undefined, emptyContactDraft())}>
            <Plus size={15} strokeWidth={1.9} />
            Kişi ekle
          </button>
        )}
      </div>
      <div className="card table-card">
        <table className="stack">
          <thead>
            <tr>
              <th>Ad</th>
              <th>Departman / Unvan</th>
              <th>Yöneticisi</th>
              <th>Telefon</th>
              <th>E-posta</th>
              {canEdit && <th className="actions" />}
            </tr>
          </thead>
          <tbody>
            {list.map((c) => (
              <tr key={c.id} className={canEdit ? 'menu-row' : undefined}>
                <td className="primary">{c.name}</td>
                <td className="muted" data-label="Departman / Unvan">{[c.department, c.title].filter(Boolean).join(' · ') || '—'}</td>
                <td className="muted" data-label="Yöneticisi">{managerName(c, list) || '—'}</td>
                <td className="muted" data-label="Telefon"><MonoValue value={c.phone} /></td>
                <td className="muted" data-label="E-posta">{c.email ?? '—'}</td>
                {canEdit && (
                  <td className="actions">
                    <RowMenu
                      label={c.name}
                      items={[
                        { label: 'Düzenle', icon: Pencil, onSelect: () => startEdit(c.id, draftFromContact(c)) },
                        { label: 'Sil', icon: Trash2, danger: true, separated: true, onSelect: () => setDeleting(c) },
                      ]}
                    />
                  </td>
                )}
              </tr>
            ))}
            {contacts !== null && list.length === 0 && (
              <tr>
                <td colSpan={canEdit ? 6 : 5} className="empty-cell">
                  <EmptyState icon={UserRound}>Bu organizasyonda henüz iletişim kişisi yok.</EmptyState>
                </td>
              </tr>
            )}
          </tbody>
        </table>
      </div>

      <Modal open={editing !== null} title={editing?.id ? 'Kişiyi düzenle' : 'İletişim kişisi ekle'} onClose={() => setEditing(null)}>
        {editing && (
          <form onSubmit={handleSave}>
            {formError && <div className="error-banner">{formError}</div>}
            {problems.length > 0 && (
              <div className="error-banner">
                {problems.map((p) => (
                  <div key={p}>{p}</div>
                ))}
              </div>
            )}
            <div className="form-row">
              <label htmlFor="ct-name">Ad</label>
              <input id="ct-name" value={editing.draft.name} onChange={(e) => set({ name: e.target.value })} autoFocus />
            </div>
            <div className="form-grid-2">
              <div className="form-row">
                <label htmlFor="ct-dept">Departman</label>
                <input id="ct-dept" value={editing.draft.department} onChange={(e) => set({ department: e.target.value })} />
              </div>
              <div className="form-row">
                <label htmlFor="ct-title">Unvan</label>
                <input id="ct-title" value={editing.draft.title} onChange={(e) => set({ title: e.target.value })} />
              </div>
            </div>
            <div className="form-row">
              <label htmlFor="ct-manager">Yöneticisi</label>
              <select id="ct-manager" value={editing.draft.managerId} onChange={(e) => set({ managerId: e.target.value })}>
                <option value="">Yok</option>
                {managerChoices(list, editing.id).map((c) => (
                  <option key={c.id} value={c.id}>
                    {c.name}
                  </option>
                ))}
              </select>
            </div>
            <div className="form-grid-2">
              <div className="form-row">
                <label htmlFor="ct-phone">Telefon</label>
                <input id="ct-phone" value={editing.draft.phone} onChange={(e) => set({ phone: e.target.value })} inputMode="tel" placeholder="+905551234567" />
              </div>
              <div className="form-row">
                <label htmlFor="ct-email">E-posta</label>
                <input id="ct-email" type="email" value={editing.draft.email} onChange={(e) => set({ email: e.target.value })} />
              </div>
            </div>
            <p className="form-hint" style={{ margin: '0 0 14px' }}>
              Telefon ya da e-postadan en az biri zorunlu. E-postası olan kişilere bildirim kuralıyla alert e-postası gönderilebilir.
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

      <Modal open={deleting !== null} size="sm" title="İletişim kişisini sil" onClose={() => setDeleting(null)}>
        {deleting && (
          <div>
            <p style={{ marginTop: 0 }}>
              <strong>{deleting.name}</strong> silinsin mi? Alıcı olduğu bildirim kuralları da silinir.
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
