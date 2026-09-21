// Fiziksel disklerin ve üzerindeki mount'ların birlikte gösterimi — saf mantık. Agent iki ayrı şey
// bildirir: fiziksel diskler (ad, model, ham boyut, bağlı mount adları) ve mount başına kullanım
// (toplam/boş bayt). Bu modül ikisini birleştirir: bir 2 TB diskte 1.5 TB /storage ile 500 GB /
// varsa her mount'un toplamı, kullanımı ve diskin ne kadarını kapladığı görünür.
import type { DiskUsage, PhysicalDisk } from '../types/api.ts'

export interface MountUsage {
  mount: string
  // Kullanım raporlanmadıysa (agent bu mount'u göndermiyor) usage yok.
  usage?: DiskUsage
  usedBytes?: number
  // Mount birden çok fiziksel diske düşüyor (LVM/mdraid): boyutu diske bölünemez.
  shared: boolean
}

export interface DiskGroup {
  disk: PhysicalDisk
  mounts: MountUsage[]
  // Yalnızca bu diske özgü mount'ların dosya sistemi toplamı; diskin boyutuna oranı çubukta gösterilir.
  allocatedBytes: number
  // Diskin dosya sistemlerine ayrılmayan kısmı (bölüm tablosu, swap, EFI, ayrılmamış alan, dosya sistemi
  // yükü). Boyut bilinmiyorsa ya da dosya sistemleri diski aşıyorsa 0.
  otherBytes: number
}

export interface DiskLayout {
  groups: DiskGroup[]
  // Hiçbir fiziksel diske bağlanamayan, ama kullanımı raporlanan mount'lar (ağ paylaşımı, sanal
  // dosya sistemi, keşfedilemeyen disk).
  unassigned: DiskUsage[]
}

export const usedBytesOf = (u: DiskUsage): number => Math.max(0, u.total - u.free)

export function diskLayout(physical: PhysicalDisk[] | undefined, usage: DiskUsage[] | undefined): DiskLayout {
  const disks = physical ?? []
  const byMount = new Map((usage ?? []).map((u) => [u.mount, u] as const))

  // Bir mount kaç fiziksel diskte listeleniyor?
  const diskCount = new Map<string, number>()
  for (const d of disks) for (const m of new Set(d.mounts)) diskCount.set(m, (diskCount.get(m) ?? 0) + 1)

  const groups: DiskGroup[] = disks.map((disk) => {
    const mounts: MountUsage[] = [...new Set(disk.mounts)].map((mount) => {
      const u = byMount.get(mount)
      return { mount, usage: u, usedBytes: u ? usedBytesOf(u) : undefined, shared: (diskCount.get(mount) ?? 0) > 1 }
    })
    const allocatedBytes = mounts.reduce((sum, m) => (m.usage && !m.shared ? sum + m.usage.total : sum), 0)
    const size = disk.size_bytes ?? 0
    return { disk, mounts, allocatedBytes, otherBytes: size > allocatedBytes ? size - allocatedBytes : 0 }
  })

  const attached = new Set(disks.flatMap((d) => d.mounts))
  return { groups, unassigned: (usage ?? []).filter((u) => !attached.has(u.mount)) }
}

// Bir mount'un diskin ne kadarını kapladığı (0–100); disk boyutu bilinmiyorsa null.
export function shareOfDisk(disk: PhysicalDisk, m: MountUsage): number | null {
  const size = disk.size_bytes ?? 0
  if (size <= 0 || !m.usage || m.shared) return null
  return Math.min(100, (m.usage.total / size) * 100)
}
