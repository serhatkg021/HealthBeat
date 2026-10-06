// Alert kuralları sayfasının saf mantığı: adresteki kapsamın (`?kapsam=&id=`) çözülmesi. React içermez; Node'un
// çalıştırıcısıyla birim test edilir.
import type { RuleScope } from '../navigation.ts'

// Bilinmeyen kapsam sistem varsayılanına düşer; organizasyonları göremeyen (operatör) organizasyon kapsamına giremez.
export function resolveScope(kapsam: string | null, id: string | null, canSeeOrganizations: boolean): RuleScope {
  const target = id?.trim() || undefined
  if (kapsam === 'org' && canSeeOrganizations) return { kind: 'org', id: target }
  if (kapsam === 'sunucu') return { kind: 'sunucu', id: target }
  return { kind: 'sistem' }
}
