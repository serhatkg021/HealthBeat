import type { ReactNode } from 'react'
import type { LucideIcon } from 'lucide-react'

export function EmptyState({ icon: Icon, children }: { icon: LucideIcon; children: ReactNode }) {
  return (
    <div className="empty-state">
      <Icon size={28} strokeWidth={1.5} />
      <div>{children}</div>
    </div>
  )
}
