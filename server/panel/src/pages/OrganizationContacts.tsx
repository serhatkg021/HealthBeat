import { useCallback, useEffect, useState, type FormEvent } from 'react'
import { Pencil, Plus, Trash2, UserRound, X } from 'lucide-react'
import { contactsApi } from '../api/endpoints'
import type { OrganizationContact } from '../types/api'
import { EmptyState } from '../components/EmptyState'
import { draftFromContact, emptyContactDraft, managerChoices, managerName, toContactInput, validateContact, type ContactDraft } from './contacts'

// Organizasyonun iletişim kişileri: sunucu işleri için başvurulacak kişiler (panel kullanıcısı olmaları gerekmez).
// Bildirim kurallarında alıcı olarak seçilebilirler.
export function OrganizationContacts({ organizationId, canEdit }: { organizationId: string; canEdit: boolean }) {
  const [contacts, setContacts] = useState<OrganizationContact[] | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [editing, setEditing] = useState<{ id?: string; draft: ContactDraft } | null>(null)
  const [problems, setProblems] = useState<string[]>([])
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
    setError(null)
    try {
      const input = toContactInput(editing.draft)
      if (editing.id) await contactsApi.update(editing.id, input)
      else await contactsApi.create(organizationId, input)
      setEditing(null)
      load()
    } catch (err) {
      setError(err instanceof Error ? err.message : 'kaydedilemedi')
    } finally {
      setBusy(false)
    }
  }

  async function handleDelete(c: OrganizationContact) {
    if (!confirm(`${c.name} silinsin mi? Bildirim kurallarındaki kaydı da silinir.`)) return
    try {
      await contactsApi.remove(c.id)
      load()
    } catch (err) {
      setError(err instanceof Error ? err.message : 'silinemedi')
    }
  }

  const list = contacts ?? []
  const set = (patch: Partial<ContactDraft>) => setEditing((cur) => (cur ? { ...cur, draft: { ...cur.draft, ...patch } } : cur))

  return (
    <div>
      {error && <div className="error-banner">{error}</div>}
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
              <tr key={c.id}>
                <td className="primary">{c.name}</td>
                <td className="muted" data-label="Departman / Unvan">{[c.department, c.title].filter(Boolean).join(' · ') || '—'}</td>
                <td className="muted" data-label="Yöneticisi">{managerName(c, list) || '—'}</td>
                <td className="muted mono" data-label="Telefon">{c.phone ?? '—'}</td>
                <td className="muted" data-label="E-posta">{c.email ?? '—'}</td>
                {canEdit && (
                  <td className="actions">
                    <button className="btn btn-sm" onClick={() => { setProblems([]); setEditing({ id: c.id, draft: draftFromContact(c) }) }}>
                      <Pencil size={14} strokeWidth={1.9} />
                      Düzenle
                    </button>
                    <button className="btn btn-sm btn-danger" onClick={() => handleDelete(c)}>
                      <Trash2 size={14} strokeWidth={1.9} />
                      Sil
                    </button>
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

      {canEdit && !editing && (
        <button className="btn btn-primary" type="button" onClick={() => { setProblems([]); setEditing({ draft: emptyContactDraft() }) }}>
          <Plus size={15} strokeWidth={1.9} />
          Kişi ekle
        </button>
      )}

      {canEdit && editing && (
        <form className="card form-card" onSubmit={handleSave}>
          <h2 className="card-title">
            <UserRound size={16} strokeWidth={1.75} />
            {editing.id ? 'Kişiyi düzenle' : 'Yeni iletişim kişisi'}
          </h2>
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
          <div className="grid-2">
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
          <div className="grid-2">
            <div className="form-row">
              <label htmlFor="ct-phone">Telefon</label>
              <input id="ct-phone" value={editing.draft.phone} onChange={(e) => set({ phone: e.target.value })} inputMode="tel" />
            </div>
            <div className="form-row">
              <label htmlFor="ct-email">E-posta</label>
              <input id="ct-email" type="email" value={editing.draft.email} onChange={(e) => set({ email: e.target.value })} />
            </div>
          </div>
          <p className="form-hint">Telefon ya da e-postadan en az biri zorunlu. E-postası olan kişilere bildirim kuralıyla alert e-postası gönderilebilir.</p>
          <div style={{ display: 'flex', gap: 8 }}>
            <button className="btn btn-primary" type="submit" disabled={busy}>
              Kaydet
            </button>
            <button className="btn" type="button" onClick={() => setEditing(null)}>
              <X size={15} strokeWidth={1.9} />
              Vazgeç
            </button>
          </div>
        </form>
      )}
    </div>
  )
}
