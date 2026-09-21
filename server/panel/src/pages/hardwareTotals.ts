// Agent'ın bildirdiği donanım toplamlarını (cpu_cores, ram_total_mb) yüzdenin yanında
// gösterilecek metne çevirir. Toplam bilinmiyorsa (eski agent sürümü) null döner ve panel
// yalnızca yüzdeyi gösterir.

export function formatCores(cores?: number | null): string | null {
  return cores && cores > 0 ? `${cores} çekirdek` : null
}

// Örn. "9.6 / 16 GB": kullanılan (yüzdeden hesaplanır) / toplam, ikisi de toplamın birimiyle.
export function formatRamUsage(usagePct: number, totalMB?: number | null): string | null {
  if (!totalMB || totalMB <= 0) return null
  const usedMB = (totalMB * usagePct) / 100
  if (totalMB < 1024) return `${Math.round(usedMB)} / ${Math.round(totalMB)} MB`
  const fmt = (mb: number) => {
    const gb = mb / 1024
    return gb >= 10 ? String(Math.round(gb)) : gb.toFixed(1).replace(/\.0$/, '')
  }
  return `${fmt(usedMB)} / ${fmt(totalMB)} GB`
}

// Yüzde ile isteğe bağlı ek bilgiyi "%42.5 · 8 çekirdek" biçiminde birleştirir.
export function joinParts(main: string, extra: string | null): string {
  return extra ? `${main} · ${extra}` : main
}

// 1024 tabanlı, RAM biçimiyle ve lsblk ile tutarlı: 500107862016 -> "465.8 GB".
export function formatBytes(bytes?: number | null): string {
  if (!bytes || bytes <= 0) return '—'
  const units = ['B', 'KB', 'MB', 'GB', 'TB', 'PB']
  let v = bytes
  let i = 0
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024
    i++
  }
  return `${v >= 1000 || i === 0 ? Math.round(v) : v.toFixed(1).replace(/\.0$/, '')} ${units[i]}`
}

export function diskKindLabel(kind?: string | null): string {
  switch (kind) {
    case 'nvme':
      return 'NVMe SSD'
    case 'ssd':
      return 'SSD'
    case 'hdd':
      return 'HDD'
    default:
      return '—'
  }
}
