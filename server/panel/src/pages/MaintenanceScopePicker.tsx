import { useMemo, useState } from 'react'
import { ChevronDown, ChevronRight } from 'lucide-react'
import { SearchInput } from '../components/SearchInput'
import type { Organization, OverviewHost } from '../types/api'
import { buildTree } from './orgTree'

// Bakım penceresinin kapsamı: organizasyon ağacı; her organizasyonda "organizasyonun tamamı" (yalnızca doğrudan bağlı
// sunucuları kapsar, alt organizasyonlar kendi satırlarında ayrıca seçilir) ve tek tek sunucular. Organizasyon
// seçiliyken sunucuları zaten kapsamdadır (işaretli ve kapalı görünür). Seçimi olan organizasyonlar açık başlar; arama
// bütün eşleşenleri açar.
export function MaintenanceScopePicker({
  orgs,
  hosts,
  hostIds,
  orgIds,
  onChange,
}: {
  orgs: Organization[]
  hosts: OverviewHost[]
  hostIds: string[]
  orgIds: string[]
  onChange: (hostIds: string[], orgIds: string[]) => void
}) {
  const [query, setQuery] = useState('')
  const [expanded, setExpanded] = useState<Set<string>>(() => {
    const open = new Set(orgIds)
    for (const h of hosts) if (hostIds.includes(h.id)) open.add(h.organization_id)
    return open
  })
  const rows = useMemo(() => buildTree(orgs), [orgs])
  const hostsByOrg = useMemo(() => {
    const m = new Map<string, OverviewHost[]>()
    for (const h of hosts) m.set(h.organization_id, [...(m.get(h.organization_id) ?? []), h])
    return m
  }, [hosts])

  const q = query.trim().toLocaleLowerCase('tr')
  const hostMatches = (h: OverviewHost) => !q || h.title.toLocaleLowerCase('tr').includes(q) || h.ip.includes(q)
  const selectedHosts = new Set(hostIds)
  const selectedOrgs = new Set(orgIds)

  const toggleOrg = (id: string, on: boolean) => {
    // Organizasyonun tamamı seçilince o organizasyonun tek tek seçilmiş sunucuları gereksizleşir.
    const own = new Set((hostsByOrg.get(id) ?? []).map((h) => h.id))
    onChange(
      on ? hostIds.filter((h) => !own.has(h)) : hostIds,
      on ? [...orgIds, id] : orgIds.filter((o) => o !== id),
    )
  }
  const toggleHost = (id: string, on: boolean) => onChange(on ? [...hostIds, id] : hostIds.filter((h) => h !== id), orgIds)

  return (
    <div className="scope-picker">
      <SearchInput value={query} onChange={setQuery} placeholder="Organizasyon, sunucu adı ya da IP ara…" debounceMs={150} />
      <div className="scope-tree">
        {rows.map(({ org, depth }) => {
          const own = hostsByOrg.get(org.id) ?? []
          const visibleHosts = own.filter(hostMatches)
          const orgMatches = !q || org.name.toLocaleLowerCase('tr').includes(q)
          if (q && !orgMatches && visibleHosts.length === 0) return null
          const whole = selectedOrgs.has(org.id)
          const picked = own.filter((h) => selectedHosts.has(h.id)).length
          const open = Boolean(q) || expanded.has(org.id)
          return (
            <div key={org.id} className="scope-org" style={{ paddingLeft: depth * 20 }}>
              <div className="scope-org-row">
                <button
                  type="button"
                  className="icon-btn scope-toggle"
                  aria-label={open ? `${org.name}: sunucuları gizle` : `${org.name}: sunucuları göster`}
                  aria-expanded={open}
                  disabled={own.length === 0}
                  onClick={() =>
                    setExpanded((prev) => {
                      const next = new Set(prev)
                      if (next.has(org.id)) next.delete(org.id)
                      else next.add(org.id)
                      return next
                    })
                  }
                >
                  {open ? <ChevronDown size={15} /> : <ChevronRight size={15} />}
                </button>
                <label className="check-row">
                  <input type="checkbox" checked={whole} onChange={(e) => toggleOrg(org.id, e.target.checked)} />
                  <strong>{org.name}</strong>
                  <span className="muted">
                    {whole ? 'organizasyonun tamamı' : picked > 0 ? `${picked}/${own.length} sunucu` : `${own.length} sunucu`}
                  </span>
                </label>
              </div>
              {open &&
                visibleHosts.map((h) => (
                  <label key={h.id} className="check-row scope-host">
                    <input
                      type="checkbox"
                      checked={whole || selectedHosts.has(h.id)}
                      disabled={whole}
                      onChange={(e) => toggleHost(h.id, e.target.checked)}
                    />
                    {h.title} <span className="muted mono">{h.ip}</span>
                  </label>
                ))}
            </div>
          )
        })}
        {rows.length === 0 && <p className="muted">Seçilebilecek organizasyon yok.</p>}
      </div>
      <p className="muted scope-summary">
        {orgIds.length} organizasyon, {hostIds.length} sunucu seçili
      </p>
    </div>
  )
}
