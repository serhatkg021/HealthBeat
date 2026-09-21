import type { ReactNode } from 'react'

interface Props {
  tone: 'good' | 'warning' | 'critical' | 'neutral'
  children: ReactNode
  // Fareyle üzerine gelince görünen açıklama.
  title?: string
}

// Durum rengi her zaman bir etiketle gelir (asla tek başına renk değil) — bkz. dataviz
// skill'inin durum paleti kuralı.
export function StatusBadge({ tone, children, title }: Props) {
  return (
    <span className={`badge badge-${tone}`} title={title}>
      {children}
    </span>
  )
}
