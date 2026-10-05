import { useMemo } from 'react'
import { buildTree } from '../pages/orgTree'
import type { Organization, OverviewHost } from '../types/api'
import { SearchSelect } from './SearchSelect'
import type { SelectOption } from './searchSelect'

// Kapsam seçicileri (Alert kuralları, Bildirim): organizasyon ağacı ve sunucu listesi, yazarak aranabilen seçim kutularıyla.
// Veriyi useScopeData yükler.

export function OrgPicker({
  idPrefix,
  orgs,
  value,
  onChange,
}: {
  idPrefix: string
  orgs: Organization[]
  value?: string
  onChange: (id: string) => void
}) {
  // Ağaç sırasında, girintili; üst şirket zinciri de aranır ("üretim" yazınca altındaki DC'ler de çıkar).
  const options = useMemo<SelectOption[]>(
    () => buildTree(orgs).map((r) => ({ value: r.org.id, label: r.org.name, depth: r.depth, keywords: r.path.join(' ') })),
    [orgs],
  )
  return (
    <span className="row">
      <span className="muted">Organizasyon</span>
      <SearchSelect id={`${idPrefix}-org`} label="Organizasyon" options={options} value={value} onChange={onChange} placeholder="Organizasyon ara ya da seç…" />
    </span>
  )
}

// Sunucu seçici: 150+ sunucuda kaydırmak yerine adı ya da IP'si yazılarak bulunur.
export function HostPicker({
  idPrefix,
  hosts,
  orgNames,
  value,
  onChange,
}: {
  idPrefix: string
  hosts: OverviewHost[]
  orgNames: Map<string, string>
  value?: string
  onChange: (id: string) => void
}) {
  const options = useMemo<SelectOption[]>(
    () => hosts.map((h) => ({ value: h.id, label: h.title, hint: orgNames.get(h.organization_id), keywords: h.ip })),
    [hosts, orgNames],
  )
  return (
    <span className="row">
      <span className="muted">Sunucu</span>
      <SearchSelect id={`${idPrefix}-host`} label="Sunucu" options={options} value={value} onChange={onChange} placeholder="Sunucu adı ya da IP ara…" />
    </span>
  )
}
