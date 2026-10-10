import type { DockerContainerReport, Host, HostThresholdsResponse } from '../types/api'
import { useTab } from '../components/useTab'
import { HostDocker } from './HostDocker'
import { SystemServices } from './SystemServices'

const KINDS = ['docker', 'sistem'] as const

// Sunucu sayfasının "Servisler" sekmesi: sunucuda çalışan her şey — Docker container'ları ve systemd servisleri (protokol
// 4). Seçili bölüm adreste (`?tur=`) tutulur.
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
          Sistem servisleri
        </button>
      </div>

      {kind === 'docker' ? <HostDocker host={host} containers={containers} thresholds={thresholds} /> : <SystemServices key={host.id} host={host} />}
    </div>
  )
}
