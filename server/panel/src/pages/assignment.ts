// Bir operatöre host atamak için saf seçim mantığı. React ve import'lardan arındırılmıştır;
// böylece Node'un yerleşik çalıştırıcısıyla birim test edilebilir.
//
// Ekran, tüm atamayı değiştiren PUT /users/:id/hosts ile seçimin TAMAMINI gönderir. Bundan
// iki kural çıkar:
//  - atanmış ama ekranda görünmeyen kimlikler (ör. organizasyonlarının listesi
//    yüklenemedi) bir düzenlemede korunmalı; bu yüzden değiştirmeler yalnızca
//    kendilerine verilen kimliklere dokunur;
//  - kaydetmek yalnızca her grup yüklendikten sonra güvenlidir.

export interface GroupHost {
  id: string
}

export interface Group {
  hosts: GroupHost[]
}

export type GroupState = 'none' | 'some' | 'all'

export function groupState(group: Group, selected: ReadonlySet<string>): GroupState {
  if (group.hosts.length === 0) return 'none'
  let hits = 0
  for (const c of group.hosts) if (selected.has(c.id)) hits++
  if (hits === 0) return 'none'
  return hits === group.hosts.length ? 'all' : 'some'
}

export function toggleHost(selected: ReadonlySet<string>, id: string, on: boolean): Set<string> {
  const next = new Set(selected)
  if (on) next.add(id)
  else next.delete(id)
  return next
}

// "Bu organizasyonun her sunucusunu seç" (ya da temizle) — diğer gruplardaki kimliklere ve
// ekranda gösterilmeyen kimliklere dokunmaz.
export function toggleGroup(selected: ReadonlySet<string>, group: Group, on: boolean): Set<string> {
  const next = new Set(selected)
  for (const c of group.hosts) {
    if (on) next.add(c.id)
    else next.delete(c.id)
  }
  return next
}

export function selectedCount(group: Group, selected: ReadonlySet<string>): number {
  return group.hosts.filter((c) => selected.has(c.id)).length
}

// Kararlı sıra; böylece aynı seçimin tekrar kaydedilmesi aynı gövdeleri gönderir.
export function serializeSelection(selected: ReadonlySet<string>): string[] {
  return [...selected].sort()
}

export function sameSelection(a: ReadonlySet<string>, b: ReadonlySet<string>): boolean {
  if (a.size !== b.size) return false
  for (const id of a) if (!b.has(id)) return false
  return true
}
