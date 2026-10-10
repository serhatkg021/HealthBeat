// Servisler sekmesindeki Docker bölümünün özet sayıları — saf mantık.
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

export type Tone = 'good' | 'warning' | 'critical' | 'neutral'

// Healthcheck sonucu (protokol 4); healthcheck tanımlı değilse ya da bilinmiyorsa null.
export function healthBadge(c: Pick<DockerContainerReport, 'health' | 'health_failing_streak'>): { label: string; tone: Tone } | null {
  switch (c.health) {
    case 'healthy':
      return { label: 'sağlıklı', tone: 'good' }
    case 'unhealthy': {
      const streak = c.health_failing_streak ? ` · ${c.health_failing_streak} kontrol` : ''
      return { label: `sağlıksız${streak}`, tone: 'critical' }
    }
    case 'starting':
      return { label: 'başlıyor', tone: 'warning' }
  }
  return null
}

// Duran bir container'ın neden durduğu: son çıkış kodu ve bellek yetmediği için öldürüldüyse OOM.
export function exitNote(c: Pick<DockerContainerReport, 'status' | 'exit_code' | 'oom_killed'>): string | null {
  if (c.status === 'running') return null
  const parts: string[] = []
  if (c.exit_code !== undefined) parts.push(`çıkış kodu ${c.exit_code}`)
  if (c.oom_killed) parts.push('bellek yetmediği için öldürüldü (OOM)')
  return parts.length > 0 ? parts.join(' · ') : null
}
