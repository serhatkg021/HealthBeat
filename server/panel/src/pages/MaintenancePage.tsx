import { CalendarClock, CalendarPlus } from 'lucide-react'
import { ComingSoon } from '../components/ComingSoon'
import { PageHeader } from '../components/PageHeader'
import { StatusBadge } from '../components/StatusBadge'
import { SAMPLE_MAINTENANCE } from './comingSoonSamples'

// Bakım pencereleri: planlı bakım sırasında bildirimleri susturmak. Özellik henüz yok; sayfa nasıl görüneceğini örnek
// veriyle "Yakında" olarak gösterir. Özellik gelince bu sayfa gerçek liste ve formla değiştirilir.
export function MaintenancePage() {
  return (
    <div>
      <PageHeader title="Bakım pencereleri" />
      <ComingSoon
        title="Planlı pencereler"
        icon={CalendarClock}
        description="Sunucu ya da organizasyon için tek seferlik veya tekrarlı pencere. Bu sürede alert’ler kaydedilir ama bildirim gönderilmez; sunucu sayfasında “bakımda” şeridi görünür."
      >
        <table>
          <thead>
            <tr>
              <th>Açıklama</th>
              <th>Kapsam</th>
              <th>Zaman</th>
              <th>Bu sürede</th>
              <th>Durum</th>
            </tr>
          </thead>
          <tbody>
            {SAMPLE_MAINTENANCE.map((m) => (
              <tr key={m.title}>
                <td>{m.title}</td>
                <td>{m.scope}</td>
                <td className="tnum">{m.when}</td>
                <td className="muted">{m.mode}</td>
                <td>
                  <StatusBadge tone={m.status.startsWith('sürüyor') ? 'warning' : m.status === 'tamamlandı' ? 'good' : 'neutral'}>{m.status}</StatusBadge>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </ComingSoon>
      <div style={{ marginTop: 16 }}>
        <ComingSoon
          title="Yeni bakım penceresi"
          icon={CalendarPlus}
          description="Açıklama, kapsam (sunucular ya da organizasyon), başlangıç–bitiş, tekrar (bir kez / her gün / her hafta) ve bu sürede ne olacağı (alert’leri kaydet ama bildirme / hiç alert açma; bitince açık kalanları bildir)."
        />
      </div>
    </div>
  )
}
