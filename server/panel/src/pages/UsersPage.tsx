import { useEffect, useState, type FormEvent } from 'react'
import { organizationsApi, usersApi } from '../api/endpoints'
import { usePagedQuery } from '../api/usePagedQuery'
import type { Organization, Role, User } from '../types/api'
import { TabPanel, Tabs, type TabItem } from '../components/Tabs'
import { useTab } from '../components/useTab'
import { PageHeader } from '../components/PageHeader'
import { EmptyState } from '../components/EmptyState'
import { SearchInput } from '../components/SearchInput'
import { Pagination } from '../components/Pagination'
import { Building2, Pencil, Server, Trash2, UserPlus, Users, UserCog } from 'lucide-react'
import { HostAssignment } from './HostAssignment'
import { buildTree } from './orgTree'

const PAGE_SIZE = 20

const ROLE_LABELS: Record<Role, string> = {
  super_admin: 'Süper Admin',
  org_admin: 'Organizasyon Admin',
  operator: 'Operatör',
}

export function UsersPage() {
  const {
    items: users,
    total,
    page,
    setPage,
    pageSize,
    setPageSize,
    q,
    setSearch,
    error,
    setError,
    reload,
  } = usePagedQuery((p) => usersApi.list({ q: p.q, limit: p.limit, offset: p.offset }), PAGE_SIZE)
  const [orgs, setOrgs] = useState<Organization[]>([])

  const [email, setEmail] = useState('')
  const [password, setPassword] = useState('')
  const [role, setRole] = useState<Role>('operator')
  const [fullName, setFullName] = useState('')
  const [phone, setPhone] = useState('')
  const [creating, setCreating] = useState(false)
  // Profil düzenleme (ad ve telefon); e-posta giriş adıdır ve buradan değişmez.
  const [editingUser, setEditingUser] = useState<User | null>(null)
  const [profileName, setProfileName] = useState('')
  const [profilePhone, setProfilePhone] = useState('')

  const [assigningUserId, setAssigningUserId] = useState<string | null>(null)
  const [selectedOrgIds, setSelectedOrgIds] = useState<string[]>([])
  const [assigningHostsTo, setAssigningHostsTo] = useState<User | null>(null)

  const assigning = assigningUserId !== null || assigningHostsTo !== null
  const assigningUser = assigningHostsTo ?? users.find((u) => u.id === assigningUserId) ?? null
  const [tab, setTab] = useTab(['liste', 'yeni', ...(assigning ? ['atama'] : []), ...(editingUser ? ['profil'] : [])], 'liste')
  const tabItems: TabItem[] = [
    { id: 'liste', label: 'Kullanıcılar', badge: total, icon: Users },
    { id: 'yeni', label: 'Yeni kullanıcı', icon: UserPlus },
    ...(assigning ? [{ id: 'atama', label: `Atama — ${assigningUser?.email ?? ''}`, icon: UserCog }] : []),
    ...(editingUser ? [{ id: 'profil', label: `Profil — ${editingUser.email}`, icon: Pencil }] : []),
  ]

  function openProfile(u: User) {
    setEditingUser(u)
    setProfileName(u.full_name ?? '')
    setProfilePhone(u.phone ?? '')
    setTab('profil')
  }

  async function saveProfile(e: FormEvent) {
    e.preventDefault()
    if (!editingUser) return
    setError(null)
    try {
      await usersApi.update(editingUser.id, { full_name: profileName.trim(), phone: profilePhone.trim() })
      setEditingUser(null)
      reload()
      setTab('liste')
    } catch (err) {
      setError(err instanceof Error ? err.message : 'profil güncellenemedi')
    }
  }

  function closeAssignment() {
    setAssigningUserId(null)
    setAssigningHostsTo(null)
    setTab('liste')
  }

  useEffect(() => {
    organizationsApi.list().then(setOrgs).catch(() => undefined)
  }, [])

  async function handleCreate(e: FormEvent) {
    e.preventDefault()
    setCreating(true)
    setError(null)
    try {
      await usersApi.create({
        email,
        password,
        role,
        ...(fullName.trim() ? { full_name: fullName.trim() } : {}),
        ...(phone.trim() ? { phone: phone.trim() } : {}),
      })
      setEmail('')
      setPassword('')
      setRole('operator')
      setFullName('')
      setPhone('')
      reload()
      setTab('liste')
    } catch (err) {
      setError(err instanceof Error ? err.message : 'kullanıcı oluşturulamadı')
    } finally {
      setCreating(false)
    }
  }

  async function handleDelete(id: string) {
    if (!confirm('Bu kullanıcıyı silmek istediğinize emin misiniz?')) return
    try {
      await usersApi.remove(id)
      reload()
    } catch (err) {
      setError(err instanceof Error ? err.message : 'kullanıcı silinemedi')
    }
  }

  async function openAssign(userId: string) {
    setAssigningHostsTo(null) // aynı anda tek bir atama paneli
    setAssigningUserId(userId)
    setTab('atama')
    try {
      const assigned = await usersApi.organizations(userId)
      setSelectedOrgIds(assigned.map((o) => o.id))
    } catch {
      setSelectedOrgIds([])
    }
  }

  async function saveAssign() {
    if (!assigningUserId) return
    try {
      await usersApi.setOrganizations(assigningUserId, selectedOrgIds)
      closeAssignment()
    } catch (err) {
      setError(err instanceof Error ? err.message : 'atama güncellenemedi')
    }
  }

  return (
    <div>
      <PageHeader title="Kullanıcılar" subtitle="Panele giriş yapabilen hesaplar ve erişim kapsamları" />
      {error && <div className="error-banner">{error}</div>}

      <Tabs items={tabItems} active={tab} onChange={setTab} label="Kullanıcı bölümleri" />

      <TabPanel id="liste" active={tab}>
        <div className="toolbar">
          <SearchInput value={q} onChange={setSearch} placeholder="E-posta ara…" />
        </div>
        <div className="card table-card">
          <Pagination page={page} pageSize={pageSize} total={total} onPageChange={setPage} onPageSizeChange={setPageSize} />
          <table className="stack">
            <thead>
              <tr>
                <th>Kullanıcı</th>
                <th>Telefon</th>
                <th>Rol</th>
                <th>Son giriş</th>
                <th className="actions" />
              </tr>
            </thead>
            <tbody>
              {users.map((u) => (
                <tr key={u.id}>
                  <td className="primary">
                    {u.full_name ?? u.email}
                    {u.full_name && <div className="muted">{u.email}</div>}
                  </td>
                  <td className="muted mono" data-label="Telefon">{u.phone ?? '—'}</td>
                  <td className="muted" data-label="Rol">{ROLE_LABELS[u.role]}</td>
                  <td className="muted" data-label="Son giriş">{u.last_login_at ? new Date(u.last_login_at).toLocaleString() : '—'}</td>
                  <td className="actions">
                    <button className="btn btn-sm" onClick={() => openProfile(u)}>
                      <Pencil size={14} strokeWidth={1.9} />
                      Profil
                    </button>
                    {u.role === 'org_admin' && (
                      <button className="btn btn-sm" onClick={() => openAssign(u.id)}>
                        <Building2 size={14} strokeWidth={1.9} />
                        Organizasyon ata
                      </button>
                    )}
                    {u.role === 'operator' && (
                      <button
                        className="btn btn-sm"
                        onClick={() => {
                          setAssigningUserId(null)
                          setAssigningHostsTo(u)
                          setTab('atama')
                        }}
                      >
                        <Server size={14} strokeWidth={1.9} />
                        Sunucu ata
                      </button>
                    )}
                    <button className="btn btn-sm btn-danger" onClick={() => handleDelete(u.id)}>
                      <Trash2 size={14} strokeWidth={1.9} />
                      Sil
                    </button>
                  </td>
                </tr>
              ))}
              {users.length === 0 && (
                <tr>
                  <td colSpan={5} className="empty-cell">
                    <EmptyState icon={Users}>Kullanıcı bulunamadı.</EmptyState>
                  </td>
                </tr>
              )}
            </tbody>
          </table>
        </div>
      </TabPanel>

      <TabPanel id="yeni" active={tab}>
        <form className="card form-card" onSubmit={handleCreate}>
          <div className="form-row">
            <label htmlFor="new-email">E-posta</label>
            <input id="new-email" type="email" value={email} onChange={(e) => setEmail(e.target.value)} required />
          </div>
          <div className="form-row">
            <label htmlFor="new-name">Ad soyad</label>
            <input id="new-name" value={fullName} onChange={(e) => setFullName(e.target.value)} maxLength={200} />
            <p className="form-hint">İsteğe bağlı; panelde ve bildirim kurallarında görünen ad. Giriş e-postayla yapılır.</p>
          </div>
          <div className="form-row">
            <label htmlFor="new-phone">Telefon</label>
            <input id="new-phone" value={phone} onChange={(e) => setPhone(e.target.value)} inputMode="tel" maxLength={32} />
            <p className="form-hint">İsteğe bağlı; ileride SMS bildirimi için.</p>
          </div>
          <div className="form-row">
            <label htmlFor="new-password">Şifre</label>
            <input
              id="new-password"
              type="password"
              minLength={12}
              value={password}
              onChange={(e) => setPassword(e.target.value)}
              required
            />
          </div>
          <div className="form-row">
            <label htmlFor="new-role">Rol</label>
            <select id="new-role" value={role} onChange={(e) => setRole(e.target.value as Role)}>
              {(Object.entries(ROLE_LABELS) as [Role, string][]).map(([value, label]) => (
                <option key={value} value={value}>
                  {label}
                </option>
              ))}
            </select>
          </div>
          <p className="form-hint" style={{ margin: '0 0 14px' }}>
            Buraya yazdığınız şifre geçicidir: kullanıcı ilk girişte kendi şifresini belirlemek zorundadır.
          </p>
          <button className="btn btn-primary" type="submit" disabled={creating}>
            <UserPlus size={15} strokeWidth={1.9} />
            Kullanıcı oluştur
          </button>
        </form>
      </TabPanel>

      {editingUser && (
        <TabPanel id="profil" active={tab}>
          <form className="card form-card" onSubmit={saveProfile}>
            <h2 className="card-title">
              <Pencil size={16} strokeWidth={1.75} />
              Profil
            </h2>
            <div className="form-row">
              <label htmlFor="pf-name">Ad soyad</label>
              <input id="pf-name" value={profileName} onChange={(e) => setProfileName(e.target.value)} maxLength={200} />
            </div>
            <div className="form-row">
              <label htmlFor="pf-phone">Telefon</label>
              <input id="pf-phone" value={profilePhone} onChange={(e) => setProfilePhone(e.target.value)} inputMode="tel" maxLength={32} />
            </div>
            <div style={{ display: 'flex', gap: 8 }}>
              <button className="btn btn-primary" type="submit">
                Kaydet
              </button>
              <button className="btn" type="button" onClick={() => { setEditingUser(null); setTab('liste') }}>
                Vazgeç
              </button>
            </div>
          </form>
        </TabPanel>
      )}

      {assigning && (
        <TabPanel id="atama" active={tab}>
        {assigningHostsTo && (
          <HostAssignment key={assigningHostsTo.id} user={assigningHostsTo} orgs={orgs} onClose={closeAssignment} />
        )}

        {assigningUserId && (
          <div className="card form-card">
            <h2 className="card-title">
              <Building2 size={16} strokeWidth={1.75} />
              Organizasyon ataması
            </h2>
            <p className="card-desc">
              Bir organizasyona atanan yönetici o organizasyonu ve altındaki tüm dalı yönetir; üst şirketini yalnızca adıyla (bilgi olarak)
              görür, kardeş dalları göremez.
            </p>
            {buildTree(orgs).map(({ org: o, depth }) => (
              <label key={o.id} className="check-row" style={{ paddingLeft: depth * 20 }}>
                <input
                  type="checkbox"
                  checked={selectedOrgIds.includes(o.id)}
                  onChange={(e) =>
                    setSelectedOrgIds((prev) => (e.target.checked ? [...prev, o.id] : prev.filter((id) => id !== o.id)))
                  }
                />
                {o.name}
              </label>
            ))}
            <div style={{ display: 'flex', gap: 8, marginTop: 12 }}>
              <button className="btn btn-primary" onClick={saveAssign}>
                Kaydet
              </button>
              <button className="btn" onClick={closeAssignment}>
                Vazgeç
              </button>
            </div>
          </div>
        )}
        </TabPanel>
      )}
    </div>
  )
}
