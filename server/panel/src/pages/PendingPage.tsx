import { Construction } from 'lucide-react'
import { EmptyState } from '../components/EmptyState'
import { PageHeader } from '../components/PageHeader'

// Yeni düzende yeri açılmış ama içeriği henüz taşınmamış sayfa. Yeniden düzenleme sürerken geçicidir; içerik
// taşındıkça her biri gerçek sayfasıyla değiştirilir.
export function PendingPage({ title, subtitle, items }: { title: string; subtitle: string; items: string[] }) {
  return (
    <div>
      <PageHeader title={title} subtitle={subtitle} />
      <div className="card">
        <EmptyState icon={Construction}>
          <strong>Bu sayfanın içeriği yeni düzende buraya taşınacak.</strong>
          <ul style={{ margin: '10px 0 0', paddingLeft: 18, textAlign: 'left' }}>
            {items.map((i) => (
              <li key={i}>{i}</li>
            ))}
          </ul>
        </EmptyState>
      </div>
    </div>
  )
}
