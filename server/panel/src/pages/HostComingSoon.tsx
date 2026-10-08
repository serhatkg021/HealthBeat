import { Gauge, PackageOpen, Thermometer } from 'lucide-react'
import { ComingSoon } from '../components/ComingSoon'
import { StatusBadge } from '../components/StatusBadge'
import { SAMPLE_PROCESSES, SAMPLE_TEMPERATURES, SAMPLE_UPDATES } from './comingSoonSamples'

// Sunucu sayfasındaki "Yakında" kartları. Hepsi ÖRNEK veri çizer (comingSoonSamples.ts); gerçek özellik gelince ilgili
// kart buradan kaldırılıp gerçek bileşen konur.

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
