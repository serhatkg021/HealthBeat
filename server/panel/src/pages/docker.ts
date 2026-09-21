// Docker sekmesinin özet sayıları — saf mantık.
import type { DockerContainerReport } from '../types/api.ts'

export interface DockerSummary {
  total: number
  running: number
  // Çalışmayan container'lar (durmuş, çıkmış, yeniden başlıyor...).
  notRunning: number
  // En az bir kez yeniden başlamış container sayısı.
  restarted: number
  cpuPct: number
  ramMB: number
}

// CPU/RAM toplamı yalnızca çalışan container'ları sayar: durmuş bir container kaynak kullanmaz.
export function dockerSummary(list: DockerContainerReport[]): DockerSummary {
  const running = list.filter((c) => c.status === 'running')
  return {
    total: list.length,
    running: running.length,
    notRunning: list.length - running.length,
    restarted: list.filter((c) => c.restart_count > 0).length,
    cpuPct: running.reduce((sum, c) => sum + c.cpu_pct, 0),
    ramMB: running.reduce((sum, c) => sum + c.ram_mb, 0),
  }
}

export function formatMB(mb: number): string {
  return mb >= 1024 ? `${(mb / 1024).toFixed(1).replace(/\.0$/, '')} GB` : `${Math.round(mb)} MB`
}
