import { Gauge, HardDriveDownload, ListChecks, Network, PackageOpen, Thermometer } from 'lucide-react'
import { ComingSoon } from '../components/ComingSoon'
import { StatusBadge } from '../components/StatusBadge'
import { SAMPLE_DISK_IO, SAMPLE_NETWORK, SAMPLE_PROCESSES, SAMPLE_SERVICES, SAMPLE_TEMPERATURES, SAMPLE_UPDATES, type SampleService } from './comingSoonSamples'

// Sunucu sayfasındaki "Yakında" kartları. Hepsi ÖRNEK veri çizer (comingSoonSamples.ts); gerçek özellik gelince ilgili
// kart buradan kaldırılıp gerçek bileşen konur.

const SERVICE_STATE: Record<SampleService['state'], { label: string; tone: 'good' | 'warning' | 'critical' | 'neutral' }> = {
  running: { label: 'çalışıyor', tone: 'good' },
  failed: { label: 'çöktü', tone: 'critical' },
  activating: { label: 'başlatılıyor', tone: 'warning' },
  inactive: { label: 'durmuş', tone: 'neutral' },
}

export function ServicesPreview() {
  return (
    <ComingSoon
      title="Sistem servisleri"
      icon={ListChecks}
      description="systemd servislerinin anlık durumu, ne zamandan beri o durumda oldukları ve yeniden başlatma sayıları. Alert için izlenecek servisler Alert kuralları’ndan seçilir."
    >
      <table>
        <thead>
          <tr>
            <th>Servis</th>
            <th>Durum</th>
            <th>Ne zamandan beri</th>
            <th>Yeniden başlatma</th>
            <th>Alert</th>
          </tr>
        </thead>
        <tbody>
          {SAMPLE_SERVICES.map((s) => (
            <tr key={s.name}>
              <td className="mono">{s.name}</td>
              <td>
                <StatusBadge tone={SERVICE_STATE[s.state].tone}>{SERVICE_STATE[s.state].label}</StatusBadge>
              </td>
              <td>{s.since}</td>
              <td>{s.restarts}</td>
              <td className="muted">{s.watched ? 'izleniyor' : '—'}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </ComingSoon>
  )
}

const points = (values: number[]) => values.map((v, i) => `${(i * 100) / (values.length - 1)},${100 - v}`).join(' ')

function Spark({ label, values, color }: { label: string; values: number[]; color: string }) {
  return (
    <div className="coming-soon-spark">
      <span className="muted">{label}</span>
      <svg viewBox="0 0 100 100" preserveAspectRatio="none" aria-hidden="true">
        <polyline points={points(values)} fill="none" stroke={color} strokeWidth="2" vectorEffect="non-scaling-stroke" />
      </svg>
    </div>
  )
}

export function DiskIoPreview() {
  const io = SAMPLE_DISK_IO
  return (
    <ComingSoon
      title="Disk G/Ç"
      icon={HardDriveDownload}
      description="Disk başına gecikme, okuma/yazma hızı ve IOPS; üç grafik aynı zaman eksenini paylaşır. Doluluk normalken diskin boğulduğu durumları gösterir."
    >
      <div className="muted" style={{ fontSize: 13, marginBottom: 8 }}>
        <span className="mono">{io.disk}</span> · gecikme {io.now.latency} · okuma {io.now.read} · yazma {io.now.write} · IOPS {io.now.iops}
      </div>
      <Spark label="Gecikme (ms)" values={io.latency} color="#2563eb" />
      <Spark label="Hız (MB/sn)" values={io.throughput} color="#eb6834" />
      <Spark label="IOPS" values={io.iops} color="#2a78d6" />
    </ComingSoon>
  )
}

export function NetworkPreview() {
  return (
    <ComingSoon title="Ağ" icon={Network} description="Arayüz başına gelen/giden trafik, hata ve düşen paket sayıları.">
      <dl className="info-list single">
        {SAMPLE_NETWORK.map((n) => (
          <div className="info-row" key={n.iface}>
            <dt className="mono">{n.iface}</dt>
            <dd>
              ↓ {n.rx} · ↑ {n.tx} <span className="muted">· hata/düşen {n.errors}</span>
            </dd>
          </div>
        ))}
      </dl>
    </ComingSoon>
  )
}

export function TemperaturePreview() {
  return (
    <ComingSoon title="Sıcaklık" icon={Thermometer} description="CPU ve disk sıcaklıkları (yalnızca fiziksel makinelerde).">
      <dl className="info-list single">
        {SAMPLE_TEMPERATURES.map((t) => (
          <div className="info-row" key={t.sensor}>
            <dt>{t.sensor}</dt>
            <dd>
              {t.celsius} °C <span className="muted">/ eşik {t.limit} °C</span>
            </dd>
          </div>
        ))}
      </dl>
    </ComingSoon>
  )
}

export function ProcessesPreview() {
  return (
    <ComingSoon title="En çok kaynak kullananlar" icon={Gauge} description="CPU ve RAM’i en çok kullanan süreçler; yalnızca süreç adı gönderilir, komut satırı gönderilmez.">
      <table>
        <thead>
          <tr>
            <th>Süreç</th>
            <th>CPU</th>
            <th>RAM</th>
            <th>Adet</th>
          </tr>
        </thead>
        <tbody>
          {SAMPLE_PROCESSES.map((p) => (
            <tr key={p.name}>
              <td className="mono">{p.name}</td>
              <td className="tnum">{p.cpu}</td>
              <td className="tnum">{p.ram}</td>
              <td className="tnum">{p.count}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </ComingSoon>
  )
}

export function UpdatesPreview() {
  const u = SAMPLE_UPDATES
  return (
    <ComingSoon title="Bekleyen güncellemeler" icon={PackageOpen} description="Kurulmayı bekleyen paket ve güvenlik güncellemeleri.">
      <dl className="info-list single">
        <div className="info-row">
          <dt>Bekleyen paket</dt>
          <dd>{u.total}</dd>
        </div>
        <div className="info-row">
          <dt>Güvenlik güncellemesi</dt>
          <dd>
            <StatusBadge tone="critical">{u.security}</StatusBadge>
          </dd>
        </div>
        <div className="info-row">
          <dt>Son kontrol</dt>
          <dd>{u.checked}</dd>
        </div>
      </dl>
    </ComingSoon>
  )
}
