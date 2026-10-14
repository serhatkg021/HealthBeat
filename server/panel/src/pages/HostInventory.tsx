import type { ReactNode } from 'react'
import { Link } from 'react-router-dom'
import { Clock, MonitorCog, Network, Wrench, type LucideIcon } from 'lucide-react'
import type { Host, HostThresholdsResponse } from '../types/api'
import { EmptyState } from '../components/EmptyState'
import { StatusBadge } from '../components/StatusBadge'
import { useNow } from '../components/useNow'
import { alertRulesPath } from '../navigation'
import { agoText } from './cache'
import { diskKindLabel, formatBytes, formatCores } from './hardwareTotals'
import {
  SECURITY_MODULE_LABEL,
  formatUptime,
  healthEmptyText,
  ipMismatch,
  kernelLabel,
  osLabel,
  shortMachineId,
  supportsInventory,
  virtualizationLabel,
} from './inventory'
import { RuleList } from './PerfPanels'
import { isRuleItem, topicInfo, topicItems, type TopicId } from './ruleTopics'
import { daemonLabel, reachText, timeSourceState, timeSyncIssues } from './systemState'
import { formatCount, formatMs, formatOffsetMs } from './units'
import { useHostRules } from './useHostRules'
import { formatDateTime } from './datetime'
import { dnsOverTLSLabel, dnssecLabel, domainItems, resolverLabel, serverGroups } from './dns'
import { MonoValue } from '../components/MonoValue'

function Row({ label, children }: { label: string; children: ReactNode }) {
  return (
    <div className="info-row">
      <dt>{label}</dt>
      <dd>{children}</dd>
    </div>
  )
}

// Envanter kartı: başlık, satırlar, (varsa) ara başlıklı ek bölüm ve kartın dibine yaslanan alert kuralları listesi. Aynı
// satırdaki iki kart aynı yüksekliktedir; listeler bu yüzden aynı hizada başlar.
function Card({
  title,
  icon: Icon,
  children,
  sub,
  footer,
}: {
  title: string
  icon: LucideIcon
  children: ReactNode
  sub?: { title: string; rows: ReactNode }
  footer?: ReactNode
}) {
  return (
    <section className="card inventory-card">
      <h2 className="card-title">
        <Icon size={16} strokeWidth={1.75} />
        {title}
      </h2>
      <dl className="info-list single">{children}</dl>
      {sub && (
        <>
          <h3 className="info-subhead">{sub.title}</h3>
          <dl className="info-list single">{sub.rows}</dl>
        </>
      )}
      {footer && <div className="inventory-card-footer">{footer}</div>}
    </section>
  )
}

const when = (at: string, now: number) => <span title={formatDateTime(at)}>{agoText(at, now)}</span>

// Sunucu sayfasının "Envanter" sekmesi: makinenin ne olduğu ve bakımı, 2×2 kart. Yan yana kartlar içerik miktarı yakın
// olacak şekilde eşleşir (Makine ve işletim sistemi ↔ Ağ ve DNS, Saat ↔ Bakım). DNS yalnızca bilgidir. Kaynak kullanımı (sıcaklık, süreçler, RAID,
// bellek yetmezliği, sistem sınırları, yük) Performans'tadır; Docker ve servisler Servisler sekmesindedir. IP uyuşmazlığı
// alert üretmez, satırda uyarı rozetidir.
export function HostInventory({ host, thresholds }: { host: Host; thresholds: HostThresholdsResponse | null }) {
  const now = useNow(60_000)
  const rules = useHostRules(host, thresholds)
  const h = host.host_info
  const state = host.system_state
  const disks = host.physical_disks ?? []
  const cores = formatCores(host.cpu_cores)
  const ram = host.ram_total_mb ? formatBytes(host.ram_total_mb * 1024 * 1024) : null
  const swap = h?.swap

  const ruleFooter = (topic: TopicId) => {
    const lines = rules.linesFor(topicItems(topicInfo(topic), 'sunucu').filter(isRuleItem))
    return lines.length > 0 ? <RuleList lines={lines} to={alertRulesPath({ kind: 'sunucu', id: host.id }, topic)} canEdit={rules.canEdit} embedded /> : undefined
  }

  const t = state?.time_sync
  const issues = t ? timeSyncIssues(t, now) : []
  const u = state?.updates

  // Sistem bilgisi yoksa (eski agent ya da henüz rapor yok) "—" dolu kartlar yerine tek açıklama; agent'ın donanım
  // özetinden bilinenler (çekirdek, bellek, disk sayısı) varsa altında.
  if (!h) {
    const known = [cores, ram ? `${ram} bellek` : null, disks.length > 0 ? `${disks.length} fiziksel disk` : null].filter(Boolean).join(' · ')
    return (
      <div className="card">
        <EmptyState icon={MonitorCog}>
          <div className="empty-state-title">{supportsInventory(host) ? 'Sistem bilgisi henüz alınmadı' : 'Bu agent sürümü sistem bilgisi göndermiyor'}</div>
          <div className="empty-state-hint">
            {supportsInventory(host)
              ? 'Agent ilk raporunu gönderince işletim sistemi, ağ, saat ve bakım bilgileri burada görünür.'
              : 'İşletim sistemi, kernel, IP adresleri, saat senkronu ve bekleyen güncellemeler agent güncellenince görünür (protokol 3).'}
          </div>
          {known && <div className="empty-state-hint">Bilinen: {known}</div>}
        </EmptyState>
      </div>
    )
  }

  return (
    <div className="stack-col">
      <div className="inventory-grid">
        <Card title="Makine ve işletim sistemi" icon={MonitorCog}>
          <Row label="Donanım">{h ? virtualizationLabel(h) : '—'}</Row>
          <Row label="CPU">{[h?.cpu_model, cores].filter(Boolean).join(' · ') || '—'}</Row>
          <Row label="Bellek">
            {ram ?? '—'}
            {swap && <span className="muted"> · swap {swap.total_mb > 0 ? formatBytes(swap.total_mb * 1024 * 1024) : 'yok'}</span>}
          </Row>
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
          <Row label="Makine kimliği (özet)">
            {h?.machine_id_hash ? (
              <span className="mono" title="/etc/machine-id'nin uygulamaya özgü özeti; aynı makinenin iki kez eklenmesini yakalamak içindir">
                {shortMachineId(h)}
              </span>
            ) : (
              '—'
            )}
          </Row>
          <Row label="İşletim sistemi">{h ? osLabel(h) : '—'}</Row>
          <Row label="Kernel · mimari">
            <MonoValue value={h ? kernelLabel(h) : null} />
          </Row>
          <Row label="Init · güvenlik modülü">
            {h?.init || '—'} · {h?.security_module ? (SECURITY_MODULE_LABEL[h.security_module] ?? h.security_module) : '—'}
          </Row>
        </Card>

        <Card title="Ağ" icon={Network} sub={{ title: 'DNS', rows: <DNSRows host={host} /> }}>
          <Row label="Hostname">
            <MonoValue value={h?.hostname} />
          </Row>
          <Row label="IP adresleri">
            {h?.addresses && h.addresses.length > 0 ? (
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
            {ipMismatch(host.ip, h) && (
              <div className="info-warning">
                <StatusBadge tone="warning">{ipMismatch(host.ip, h)}</StatusBadge>
              </div>
            )}
          </Row>
        </Card>

        <Card title="Saat" icon={Clock} footer={ruleFooter('saat')}>
          <Row label="Saat dilimi">{h?.timezone || '—'}</Row>
          <Row label="Senkron">
            {(t?.synchronized ?? h?.time_synced) === undefined ? (
              '—'
            ) : (t?.synchronized ?? h?.time_synced) ? (
              <StatusBadge tone="good">senkron</StatusBadge>
            ) : (
              <StatusBadge tone="critical">senkron değil</StatusBadge>
            )}
            {t?.daemon && <span className="muted"> · {daemonLabel(t.daemon)}</span>}
          </Row>
          <Row label="Saat sunucusu">
            {t?.server || t?.server_address ? (
              <span className="mono">
                {t.server}
                {t.server_address && t.server_address !== t.server && <span className="muted"> ({t.server_address})</span>}
              </span>
            ) : (
              '—'
            )}
          </Row>
          <Row label="Fark">
            {t ? (
              <>
                {formatOffsetMs(t.offset_ms)}
                <span className="muted">
                  {' '}
                  · gecikme {formatMs(t.delay_ms)} · titreşim {formatMs(t.jitter_ms)} · stratum {t.stratum ?? '—'}
                </span>
              </>
            ) : (
              '—'
            )}
          </Row>
          <Row label="Son senkron">{t?.last_sync ? when(t.last_sync, now) : '—'}</Row>
          {issues.length > 0 && (
            <Row label="Sorunlar">
              <ul className="info-addresses">
                {issues.map((i) => (
                  <li key={i}>
                    <StatusBadge tone="warning">{i}</StatusBadge>
                  </li>
                ))}
              </ul>
            </Row>
          )}
          {t?.sources && t.sources.length > 0 && (
            <Row label="Saat kaynakları">
              {/* Kaynak tablosu varsayılanda kapalı: kartı (ve yanındaki Bakım'ı) uzatmasın. */}
              <details className="inventory-details">
                <summary>{t.sources.length} kaynak</summary>
                <table className="stack compact-table">
                  <thead>
                    <tr>
                      <th>Kaynak</th>
                      <th>Durum</th>
                      <th>Ulaşılabilirlik</th>
                      <th>Fark</th>
                    </tr>
                  </thead>
                  <tbody>
                    {t.sources.map((src) => {
                      const st = timeSourceState(src.state)
                      return (
                        <tr key={src.name}>
                          <td className="primary mono">{src.name}</td>
                          <td data-label="Durum">
                            <StatusBadge tone={st.tone}>{st.label}</StatusBadge>
                          </td>
                          <td className="tnum" data-label="Ulaşılabilirlik" title="son 8 denemenin başarılı olanları">
                            {reachText(src.reach)}
                          </td>
                          <td className="tnum" data-label="Fark">
                            {formatOffsetMs(src.offset_ms)}
                          </td>
                        </tr>
                      )
                    })}
                  </tbody>
                </table>
              </details>
            </Row>
          )}
        </Card>

        <Card title="Bakım" icon={Wrench} footer={ruleFooter('bakim')}>
          <Row label="Bekleyen paket">{u ? formatCount(u.pending) : '—'}</Row>
          <Row label="Güvenlik güncellemesi">{u ? u.security > 0 ? <StatusBadge tone="critical">{formatCount(u.security)} paket</StatusBadge> : '0' : '—'}</Row>
          <Row label="Paket listesi">{u?.lists_updated_at ? <>{when(u.lists_updated_at, now)} güncellendi</> : '—'}</Row>
          <Row label="Yeniden başlatma">
            {h?.reboot_required === undefined ? '—' : h.reboot_required ? <StatusBadge tone="warning">gerekiyor</StatusBadge> : 'gerekmiyor'}
          </Row>
          <Row label="Çalışma süresi">
            {formatUptime(h?.uptime_seconds)}
            {h?.boot_time && <span className="muted"> · açılış {formatDateTime(h.boot_time)}</span>}
          </Row>
          <Row label="Başarısız servis">
            {h?.failed_units === undefined ? '—' : h.failed_units > 0 ? <StatusBadge tone="warning">{h.failed_units} servis</StatusBadge> : '0'}
            <Link className="inventory-link" to={`/hosts/${host.id}?sekme=servisler&tur=sistem`}>
              Servisler →
            </Link>
          </Row>
        </Card>
      </div>
    </div>
  )
}

// Ağ kartının DNS bölümü. Eski agent DNS bilgisini göndermez (protokol 4 öncesi) ya da henüz bildirmemiştir.
function DNSRows({ host }: { host: Host }) {
  const d = host.system_state?.dns
  if (!d) {
    return (
      <Row label="Durum">
        <span className="muted">{healthEmptyText(host, 'DNS bilgisi')}</span>
      </Row>
    )
  }
  const groups = serverGroups(d)
  const domains = domainItems(d)
  const resolved = d.resolver === 'resolved'
  return (
    <>
      <Row label="Çözümleyici">{resolverLabel(d)}</Row>
      {resolved && (
        <Row label="Kullanılan sunucu">
          {d.current_server ? (
            <span className="mono">
              {d.current_server}
              {d.current_interface && <span className="muted"> ({d.current_interface})</span>}
            </span>
          ) : (
            '—'
          )}
        </Row>
      )}
      <Row label="Sunucular">
        {groups.length > 0 ? (
          <ul className="info-addresses">
            {groups.map((g) => (
              <li key={g.iface ?? ''} className="mono">
                <span className="muted">{g.iface ?? 'genel'} </span>
                {g.addresses.join(', ')}
              </li>
            ))}
          </ul>
        ) : (
          <span className="muted">DNS sunucusu tanımlı değil</span>
        )}
      </Row>
      {resolved && (
        <Row label="Yedek sunucular">
          {d.fallback_servers && d.fallback_servers.length > 0 ? <span className="mono">{d.fallback_servers.join(', ')}</span> : '—'}
        </Row>
      )}
      <Row label="Alan adları">
        {domains.length > 0 ? (
          <ul className="info-addresses">
            {domains.map((x) => (
              <li key={`${x.name}|${x.routingOnly}`}>
                <span className="mono">{x.name}</span>
                {x.routingOnly && <span className="muted"> (yalnızca yönlendirme)</span>}
              </li>
            ))}
          </ul>
        ) : (
          '—'
        )}
      </Row>
      {resolved ? (
        <Row label="DNSSEC · DoT">
          {dnssecLabel(d.dnssec)} · {dnsOverTLSLabel(d.dns_over_tls)}
        </Row>
      ) : (
        <Row label="Seçenekler">{d.options && d.options.length > 0 ? <span className="mono">{d.options.join(' ')}</span> : '—'}</Row>
      )}
    </>
  )
}
