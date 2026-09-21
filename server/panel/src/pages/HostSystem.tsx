import type { ReactNode } from 'react'
import { Activity, HardDrive, MonitorCog, Network, ShieldCheck } from 'lucide-react'
import type { Host, HostThresholdsResponse } from '../types/api'
import { DiskGroupCard } from '../components/DiskGroupCard'
import { EmptyState } from '../components/EmptyState'
import { MountMeter } from '../components/MountMeter'
import { StatusBadge } from '../components/StatusBadge'
import { supportsHardwareSummary } from './agentStatus'
import { diskLayout } from './diskLayout'
import { formatBytes, formatCores } from './hardwareTotals'
import {
  SECURITY_MODULE_LABEL,
  formatUptime,
  ipMismatch,
  kernelLabel,
  loadText,
  osLabel,
  shortMachineId,
  supportsInventory,
  swapText,
  virtualizationLabel,
} from './inventory'
import { mountLevels } from './usage'
import type { DiskUsage } from '../types/api'

function Row({ label, children, warning }: { label: string; children: ReactNode; warning?: string }) {
  return (
    <div className="info-row">
      <dt>{label}</dt>
      <dd>
        {children}
        {warning && (
          <div className="info-warning">
            <StatusBadge tone="warning">{warning}</StatusBadge>
          </div>
        )}
      </dd>
    </div>
  )
}

function Group({ title, icon: Icon, children }: { title: string; icon: typeof Network; children: ReactNode }) {
  return (
    <div className="card">
      <h2 className="card-title">
        <Icon size={16} strokeWidth={1.75} />
        {title}
      </h2>
      <dl className="info-list single">{children}</dl>
    </div>
  )
}

const yesNo = (v: boolean | undefined, yes: string, no: string): ReactNode => (v === undefined ? '—' : v ? yes : no)

// Sunucu sayfasının "Sistem" sekmesi: sunucunun ne olduğu (donanım, işletim sistemi, ağ, çalışma
// durumu) ve fiziksel diskleri. Yalnızca bilgi içindir; IP uyuşmazlığı alert üretmez, ilgili
// satırda uyarı rozeti olarak gösterilir. Docker bilgileri "Docker" sekmesindedir.
export function HostSystem({
  host,
  disk,
  thresholds,
}: {
  host: Host
  // Son raporlanan mount kullanımı (Genel sekmesiyle aynı veri).
  disk: DiskUsage[]
  thresholds: HostThresholdsResponse | null
}) {
  const h = host.host_info
  const layout = diskLayout(host.physical_disks, disk)
  const cores = formatCores(host.cpu_cores)
  const ram = host.ram_total_mb ? formatBytes(host.ram_total_mb * 1024 * 1024) : null

  return (
    <div>
      <div className="grid-2">
        <div className="stack-col">
          <Group title="Makine" icon={MonitorCog}>
            {h && <Row label="İşletim sistemi">{osLabel(h)}</Row>}
            {h && (
              <Row label="Kernel · mimari">
                <span className="mono">{kernelLabel(h)}</span>
              </Row>
            )}
            {h && <Row label="Donanım">{virtualizationLabel(h)}</Row>}
            {h && <Row label="CPU modeli">{h.cpu_model || '—'}</Row>}
            <Row label="CPU çekirdeği">{cores ?? '—'}</Row>
            <Row label="Toplam RAM">{ram ?? '—'}</Row>
            {h && <Row label="Swap">{swapText(h)}</Row>}
            {h && (
              <Row label="Makine kimliği (özet)">
                <span className="mono" title="/etc/machine-id'nin uygulamaya özgü özeti; aynı makinenin iki kez eklenmesini yakalamak içindir">
                  {shortMachineId(h)}
                </span>
              </Row>
            )}
          </Group>

          {h && (
            <Group title="Zaman ve güvenlik" icon={ShieldCheck}>
              <Row label="Saat dilimi">{h.timezone || '—'}</Row>
              <Row label="Saat senkronu">{yesNo(h.time_synced, 'Senkron', 'Senkron değil')}</Row>
              <Row label="Güvenlik modülü">{h.security_module ? (SECURITY_MODULE_LABEL[h.security_module] ?? h.security_module) : '—'}</Row>
            </Group>
          )}
        </div>

        <div className="stack-col">
          {h ? (
            <>
              <Group title="Ağ ve kimlik" icon={Network}>
                <Row label="Makinenin hostname'i">
                  <span className="mono">{h.hostname || '—'}</span>
                </Row>
                <Row label="IP adresleri" warning={ipMismatch(host.ip, h)}>
                  {h.addresses && h.addresses.length > 0 ? (
                    <ul className="info-addresses">
                      {h.addresses.map((a) => (
                        <li key={`${a.interface ?? ''}${a.address}`} className="mono">
                          {a.interface ? <span className="muted">{a.interface} </span> : null}
                          {a.address}
                        </li>
                      ))}
                    </ul>
                  ) : (
                    '—'
                  )}
                </Row>
              </Group>

              <Group title="Çalışma durumu" icon={Activity}>
                <Row label="Çalışma süresi">
                  {formatUptime(h.uptime_seconds)}
                  {h.boot_time && <span className="muted"> · açılış {new Date(h.boot_time).toLocaleString()}</span>}
                </Row>
                <Row label="Yük ortalaması">{loadText(h)}</Row>
                <Row label="Init sistemi">{h.init || '—'}</Row>
                <Row label="Başarısız servis">
                  {h.failed_units === undefined ? '—' : h.failed_units > 0 ? <StatusBadge tone="warning">{h.failed_units} servis</StatusBadge> : '0'}
                </Row>
                <Row label="Yeniden başlatma">
                  {h.reboot_required === undefined ? '—' : h.reboot_required ? <StatusBadge tone="warning">gerekiyor</StatusBadge> : 'gerekmiyor'}
                </Row>
              </Group>
            </>
          ) : (
            <div className="card">
              <EmptyState icon={MonitorCog}>
                {supportsInventory(host)
                  ? 'Sistem bilgisi henüz alınmadı.'
                  : 'Bu agent sistem bilgisini (işletim sistemi, kernel, IP adresleri, uptime vb.) göndermiyor; agent 1.3.0 ya da üstü (protokol 3) gerekir.'}
              </EmptyState>
            </div>
          )}
        </div>
      </div>

      <h2 className="section-title">
        <HardDrive size={16} strokeWidth={1.75} />
        Fiziksel diskler
      </h2>
      {layout.groups.length === 0 ? (
        <div className="card">
          <EmptyState icon={HardDrive}>
            {!supportsHardwareSummary(host)
              ? 'Bu agent donanım özetini göndermiyor; fiziksel diskler, çekirdek sayısı ve toplam RAM için agent güncellenmeli.'
              : 'Fiziksel disk bilgisi yok (diskler keşfedilemedi).'}
          </EmptyState>
        </div>
      ) : (
        <div className="grid-2 disk-groups">
          {layout.groups.map((g) => (
            <DiskGroupCard key={g.disk.name} group={g} thresholds={thresholds} />
          ))}
        </div>
      )}

      {layout.groups.length > 0 && layout.unassigned.length > 0 && (
        <div className="card">
          <h2 className="card-title">Diğer bağlama noktaları</h2>
          <p className="card-desc">Fiziksel bir diske bağlanamayan dosya sistemleri (ağ paylaşımı, sanal dosya sistemi vb.).</p>
          <div className="mount-grid">
            {layout.unassigned.map((u) => (
              <MountMeter key={u.mount} mount={u.mount} usage={u} levels={mountLevels(thresholds?.thresholds, thresholds?.mount_thresholds, u.mount)} />
            ))}
          </div>
        </div>
      )}
    </div>
  )
}
