// Bildirim sayfasının saf mantığı: kurallar sekmesinin kapsamı ve tüm organizasyonların iletişim kişileri tablosunun
// süzülmesi. React içermez; Node'un çalıştırıcısıyla birim test edilir.
import type { OrganizationContact } from '../types/api.ts'

export interface RouteScopeChoice {
  kind: 'org' | 'sunucu'
  id?: string
}

// Bildirim kurallarının kapsamı organizasyon ya da sunucudur (sistem genelinde kural yok: sistem sahipleri her zaman
// alır). Organizasyonları göremeyen (operatör) yalnızca sunucu kapsamını açabilir; varsayılan, görebiliyorsa organizasyon.
export function resolveRouteScope(kapsam: string | null, id: string | null, canSeeOrganizations: boolean): RouteScopeChoice {
  const target = id?.trim() || undefined
  if (kapsam === 'sunucu' || !canSeeOrganizations) return { kind: 'sunucu', id: kapsam === 'sunucu' ? target : undefined }
  return { kind: 'org', id: kapsam === 'org' ? target : undefined }
}

export interface ContactRow extends OrganizationContact {
  organizationName: string
}

const norm = (s: string) => s.toLocaleLowerCase('tr')

// Metnin her kelimesi kişinin adında, organizasyonunda, e-postasında, telefonunda, departmanında ya da unvanında geçmeli.
// Sıra: organizasyon, sonra ad.
export function filterContacts(rows: ContactRow[], query: string): ContactRow[] {
  const words = norm(query).split(/\s+/).filter(Boolean)
  const sorted = [...rows].sort((a, b) => a.organizationName.localeCompare(b.organizationName, 'tr') || a.name.localeCompare(b.name, 'tr'))
  if (words.length === 0) return sorted
  return sorted.filter((r) => {
    const hay = norm([r.name, r.organizationName, r.email, r.phone, r.department, r.title].filter(Boolean).join(' '))
    return words.every((w) => hay.includes(w))
  })
}
