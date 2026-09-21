// Organizasyon ağacı için saf mantık (React yok; Node'un çalıştırıcısıyla birim test edilir).

import type { Organization } from '../types/api.ts'

export interface TreeRow {
  org: Organization
  depth: number
  // Kökten kendisine üst şirket adları ("Holding › Acme"); kökte boş.
  path: string[]
}

// Ağacı gösterim sırasına dizer: kökler ada göre, her birinin altında çocukları (yine ada göre). Üst şirketi listede
// olmayan bir organizasyon (ör. yönetici üst zinciri göremiyorsa) kök sayılır.
export function buildTree(orgs: Organization[]): TreeRow[] {
  const byId = new Map(orgs.map((o) => [o.id, o]))
  const children = new Map<string, Organization[]>()
  const roots: Organization[] = []
  for (const o of orgs) {
    const parent = o.parent_organization_id
    if (parent && byId.has(parent)) children.set(parent, [...(children.get(parent) ?? []), o])
    else roots.push(o)
  }
  const byName = (a: Organization, b: Organization) => a.name.localeCompare(b.name)
  const out: TreeRow[] = []
  const seen = new Set<string>()
  const walk = (o: Organization, depth: number, path: string[]) => {
    if (seen.has(o.id)) return // döngüye karşı koruma (server zaten engeller)
    seen.add(o.id)
    out.push({ org: o, depth, path })
    for (const c of (children.get(o.id) ?? []).sort(byName)) walk(c, depth + 1, [...path, o.name])
  }
  for (const r of roots.sort(byName)) walk(r, 0, [])
  return out
}

// `id`'nin ve altındaki tüm organizasyonların kimlikleri: bir organizasyon bunların altına taşınamaz (döngü).
export function selfAndDescendants(id: string, orgs: Organization[]): Set<string> {
  const out = new Set<string>([id])
  let grew = true
  while (grew) {
    grew = false
    for (const o of orgs) {
      if (o.parent_organization_id && out.has(o.parent_organization_id) && !out.has(o.id)) {
        out.add(o.id)
        grew = true
      }
    }
  }
  return out
}

// Üst şirket olarak seçilebilecekler: tam erişimli olanlar (bağlam organizasyonları yalnızca bilgidir) ve, düzenlenen
// organizasyon varsa, onun kendisi ile altındakiler hariç.
export function parentChoices(orgs: Organization[], editingId?: string): Organization[] {
  const banned = editingId ? selfAndDescendants(editingId, orgs) : new Set<string>()
  return orgs.filter((o) => o.access !== 'context' && !banned.has(o.id))
}

// Organizasyon kimliğinden üst şirket kimliğine eşleme (bkz. thresholds.ts orgChain).
export function parentMap(orgs: Organization[]): Map<string, string | undefined> {
  return new Map(orgs.map((o) => [o.id, o.parent_organization_id]))
}
