import type { ReactNode } from 'react'
import { Activity, MonitorCog, Network, ShieldCheck } from 'lucide-react'
import type { Host, HostThresholdsResponse } from '../types/api'
import { EmptyState } from '../components/EmptyState'
import { StatusBadge } from '../components/StatusBadge'
import { diskKindLabel, formatBytes, formatCores } from './hardwareTotals'
import { HostSystemState } from './HostSystemState'
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

// Sunucu sayfasının "Envanter" sekmesi: sunucunun ne olduğu (donanım, işletim sistemi, ağ, çalışma durumu). Yalnızca
// bilgi içindir; IP uyuşmazlığı alert üretmez, ilgili satırda uyarı rozeti olarak gösterilir. Altında agent'ın son
// raporundaki anlık durumlar (protokol 4: sıcaklık, süreçler, güncellemeler, kapasite, saat senkronu, RAID) durur.
// Disklerin doluluğu ve zamana bağlı her şey "Performans", Docker ve servisler "Servisler" sekmesindedir.
export function HostInventory({ host, thresholds }: { host: Host; thresholds: HostThresholdsResponse | null }) {
  const h = host.host_info
  const disks = host.physical_disks ?? []
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
            <Row label="Fiziksel diskler">
              {disks.length > 0 ? (
                <ul className="info-addresses">
                  {disks.map((d) => (
                    <li key={d.name}>
                      <span className="mono">{d.name}</span>
                      <span className="muted"> · {[d.size_bytes ? formatBytes(d.size_bytes) : null, d.kind ? diskKindLabel(d.kind) : null, d.model].filter(Boolean).join(' · ')}</span>
                    </li>
                  ))}
                </ul>
              ) : (
                '—'
              )}
            </Row>
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
                  : 'Bu agent sistem bilgisini (işletim sistemi, kernel, IP adresleri, uptime vb.) göndermiyor; agent güncellenince görünür (protokol 3).'}
              </EmptyState>
            </div>
          )}
        </div>
      </div>

      <HostSystemState host={host} thresholds={thresholds} />
    </div>
  )
}
