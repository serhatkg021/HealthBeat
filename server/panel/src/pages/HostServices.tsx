import type { DockerContainerReport, Host, HostThresholdsResponse } from '../types/api'
import { ComingSoonNote } from '../components/ComingSoon'
import { useTab } from '../components/useTab'
import { HostDocker } from './HostDocker'
import { ServicesPreview } from './HostComingSoon'

const KINDS = ['docker', 'sistem'] as const

// Sunucu sayfasının "Servisler" sekmesi: sunucuda çalışan her şey. Docker container'ları bugün gerçek veriyle; systemd
// servisleri henüz gelmediği için örnek veriyle "Yakında" olarak gösterilir. Seçili bölüm adreste (`?tur=`) tutulur.
export function HostServices({
  host,
  containers,
  thresholds,
}: {
  host: Host
  containers: DockerContainerReport[]
  thresholds: HostThresholdsResponse | null
}) {
  const [kind, setKind] = useTab(KINDS, 'docker', 'tur')
  return (
    <div>
      <div className="segmented" role="group" aria-label="Servis türü" style={{ marginBottom: 14 }}>
        <button type="button" aria-pressed={kind === 'docker'} onClick={() => setKind('docker')}>
          Docker · {containers.length}
        </button>
        <button type="button" aria-pressed={kind === 'sistem'} onClick={() => setKind('sistem')}>
          Sistem servisleri · yakında
        </button>
      </div>

      {kind === 'docker' ? (
        <>
          <ComingSoonNote>Container sağlık durumu (healthcheck) ve yeniden başlatma döngüsü uyarısı.</ComingSoonNote>
          <HostDocker host={host} containers={containers} thresholds={thresholds} />
        </>
      ) : (
        <ServicesPreview />
      )}
    </div>
  )
}
