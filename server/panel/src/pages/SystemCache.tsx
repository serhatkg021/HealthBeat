import { useEffect, useRef, useState } from 'react'
import { Gauge, Network, RefreshCw, Shield, ShieldCheck, Timer } from 'lucide-react'
import { systemApi } from '../api/endpoints'
import type { CacheStatus, RateLimiterStatus } from '../types/api'
import { StatusBadge } from '../components/StatusBadge'
import { UsageBar } from '../components/UsageBar'
import { useNow } from '../components/useNow'
import { roleLabel } from '../labels'
import { agoText, certExpiry, certNames, expiresInText, hiddenKeys, keyKindLabel, limiterInfo, limiterRate, usedOf } from './cache'

// Cache Durumu: server sürecinin bellekte tuttuğu durum (izin önbelleği, hız sınırlayıcılar, pull zamanlayıcı, güvenilir
// proxy'ler, sunulan TLS sertifikası). Salt okunur ve anlık bir görüntüdür: "Yenile" ile yeniden okunur. Yalnızca bu
// isteği karşılayan server sürecini gösterir.
export function SystemCache() {
  const [status, setStatus] = useState<CacheStatus | null>(null)
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const now = useNow(1000)
  const latest = useRef(0)

  function load() {
    const id = ++latest.current
    setLoading(true)
    setError(null)
    systemApi
      .cache()
      .then((s) => id === latest.current && setStatus(s))
      .catch((err) => id === latest.current && setError(err instanceof Error ? err.message : 'cache durumu yüklenemedi'))
      .finally(() => id === latest.current && setLoading(false))
  }

  // eslint-disable-next-line react-hooks/exhaustive-deps
  useEffect(() => load(), [])

  return (
    <div>
      {error && <div className="error-banner">{error}</div>}
      <div className="toolbar">
        <span className="muted">
          {status ? `Anlık görüntü: ${new Date(status.generated_at).toLocaleTimeString()} (${agoText(status.generated_at, now)})` : 'Yükleniyor…'}
        </span>
        <button className="btn" type="button" disabled={loading} onClick={load}>
          <RefreshCw size={15} strokeWidth={1.9} />
          Yenile
        </button>
      </div>

      {status && (
        <>
          <PermissionCache cache={status.permissions} now={now} />
          <div className="card">
            <h2 className="card-title">
              <Gauge size={16} strokeWidth={1.75} />
              Hız sınırlayıcılar
            </h2>
            <p className="card-desc">
              Her anahtarın harcadığı hak ve kapasitesi. Hak zamanla yeniden dolar; hakkı tam olan anahtar listelenmez. Hız Ayarlar → Sistem
              Ayarları’ndan değişir.
            </p>
            <div className="limiter-grid">
              {status.rate_limiters.map((l) => (
                <Limiter key={l.id} limiter={l} now={now} />
              ))}
            </div>
          </div>
          <PullScheduler pull={status.pull_scheduler} now={now} />
          <TrustedProxies proxies={status.trusted_proxies} />
          <Certificate cert={status.tls_certificate} now={now} />
        </>
      )}
    </div>
  )
}

function PermissionCache({ cache, now }: { cache: CacheStatus['permissions']; now: number }) {
  return (
    <div className="card table-card">
      <h2 className="card-title">
        <Shield size={16} strokeWidth={1.75} />
        İzin önbelleği
      </h2>
      <p className="card-desc cache-desc">
        {cache.ttl_seconds > 0
          ? `Bir rolün izinleri ilk sorulduğunda veritabanından okunur ve ${cache.ttl_seconds} saniye bellekte tutulur.`
          : 'Önbellek kapalı: her izin denetimi veritabanına gider.'}
      </p>
      <table className="stack">
        <thead>
          <tr>
            <th>Rol</th>
            <th>İzinler</th>
            <th>Kalan ömür</th>
          </tr>
        </thead>
        <tbody>
          {cache.roles.map((r) => (
            <tr key={r.role}>
              <td className="primary nowrap">
                {roleLabel(r.role)}
                <div className="cell-sub mono">{r.role}</div>
              </td>
              <td data-label="İzinler">
                <details>
                  <summary>{r.permissions.length} izin</summary>
                  <div className="cache-keys mono">{r.permissions.join(' · ')}</div>
                </details>
              </td>
              <td className="nowrap" data-label="Kalan ömür">
                {expiresInText(r.expires_at, now)}
              </td>
            </tr>
          ))}
          {cache.roles.length === 0 && (
            <tr>
              <td colSpan={3} className="empty-cell muted">
                Önbellekte rol yok.
              </td>
            </tr>
          )}
        </tbody>
      </table>
    </div>
  )
}

function Limiter({ limiter, now }: { limiter: RateLimiterStatus; now: number }) {
  const info = limiterInfo(limiter.id)
  const hidden = hiddenKeys(limiter)
  return (
    <section className="limiter">
      <div className="limiter-head">
        <h3 className="limiter-title">{info.title}</h3>
        <StatusBadge tone={!limiter.enabled ? 'neutral' : limiter.keys > 0 ? 'warning' : 'good'}>
          {!limiter.enabled ? 'kapalı' : limiter.keys > 0 ? `${limiter.keys} anahtar` : 'boş'}
        </StatusBadge>
      </div>
      <p className="form-hint limiter-hint">
        {info.description} {limiter.enabled && <span className="nowrap">Kural: {limiterRate(limiter)}.</span>}
      </p>
      {limiter.entries.length > 0 && (
        <table className="limiter-table">
          <thead>
            <tr>
              <th>{keyKindLabel(limiter.key_kind)}</th>
              <th>Harcanan / kapasite</th>
              <th>Son istek</th>
            </tr>
          </thead>
          <tbody>
            {limiter.entries.map((e) => {
              const used = usedOf(e, limiter.burst)
              return (
                <tr key={e.key}>
                  <td className="limiter-key">
                    {e.label || e.key}
                    {e.label && <div className="cell-sub mono">{e.key}</div>}
                  </td>
                  <td>
                    <div className="limiter-used">
                      <UsageBar pct={used.pct} label={`${e.label || e.key} harcanan hak`} size="sm" levels={{ warning_level: 60, critical_level: 100 }} />
                      <span className="nowrap">{used.text}</span>
                    </div>
                  </td>
                  <td className="muted nowrap">{agoText(e.last_used_at, now)}</td>
                </tr>
              )
            })}
          </tbody>
        </table>
      )}
      {hidden > 0 && <p className="form-hint limiter-hint">ve {hidden} anahtar daha (en çok harcayanlar gösteriliyor).</p>}
    </section>
  )
}

function PullScheduler({ pull, now }: { pull: CacheStatus['pull_scheduler']; now: number }) {
  if (!pull) return null
  return (
    <div className="card table-card">
      <h2 className="card-title">
        <Timer size={16} strokeWidth={1.75} />
        Pull zamanlayıcı
        <StatusBadge tone={pull.verifies_tls ? 'good' : 'neutral'}>{pull.verifies_tls ? 'sertifika doğrulanıyor' : 'sertifika doğrulanmıyor'}</StatusBadge>
      </h2>
      <p className="card-desc cache-desc">Pull modundaki her sunucunun en son ne zaman sorgulandığı; sıradaki sorgu bu zamana ve sunucunun aralığına göre yapılır.</p>
      <table className="stack">
        <thead>
          <tr>
            <th>Sunucu</th>
            <th>Son sorgulama</th>
            <th>Durum</th>
          </tr>
        </thead>
        <tbody>
          {pull.hosts.map((h) => (
            <tr key={h.host_id}>
              <td className="primary">
                {h.title || 'silinmiş sunucu'}
                <div className="cell-sub mono">{h.host_id}</div>
              </td>
              <td className="nowrap" data-label="Son sorgulama" title={new Date(h.last_polled_at).toLocaleString()}>
                {agoText(h.last_polled_at, now)}
              </td>
              <td data-label="Durum">
                <StatusBadge tone={h.in_flight ? 'warning' : 'neutral'}>{h.in_flight ? 'sorgulanıyor' : 'bekliyor'}</StatusBadge>
              </td>
            </tr>
          ))}
          {pull.hosts.length === 0 && (
            <tr>
              <td colSpan={3} className="empty-cell muted">
                Sorgulanan pull sunucusu yok.
              </td>
            </tr>
          )}
        </tbody>
      </table>
    </div>
  )
}

function TrustedProxies({ proxies }: { proxies: CacheStatus['trusted_proxies'] }) {
  if (!proxies) return null
  const empty = proxies.prefixes.length === 0 && proxies.hosts.length === 0
  return (
    <div className="card">
      <h2 className="card-title">
        <Network size={16} strokeWidth={1.75} />
        Güvenilen proxy’ler
      </h2>
      <p className="card-desc">
        İstemci IP’si (hız sınırları, denetim kaydı) yalnızca bu adreslerden gelen X-Forwarded-For başlığından okunur (TRUSTED_PROXIES). Ad olarak
        verilenler periyodik çözülür.
      </p>
      {empty && <p className="muted">Tanımlı proxy yok: istemci IP’si her zaman bağlantının kendisinden alınır.</p>}
      <dl className="cache-facts">
        {proxies.prefixes.length > 0 && (
          <>
            <dt>Sabit adresler</dt>
            <dd className="mono">{proxies.prefixes.join(', ')}</dd>
          </>
        )}
        {proxies.hosts.map((h) => (
          <FactRow key={h.name} label={h.name}>
            <span className="mono">{h.addrs.length > 0 ? h.addrs.join(', ') : 'henüz çözülmedi'}</span>{' '}
            {h.failing && <StatusBadge tone="warning">{h.addrs.length > 0 ? 'son çözüm başarısız; bilinen adres kullanılıyor' : 'çözülemiyor'}</StatusBadge>}
          </FactRow>
        ))}
      </dl>
    </div>
  )
}

function Certificate({ cert, now }: { cert: CacheStatus['tls_certificate']; now: number }) {
  if (!cert) return null
  const expiry = certExpiry(cert, now)
  const names = certNames(cert)
  return (
    <div className="card">
      <h2 className="card-title">
        <ShieldCheck size={16} strokeWidth={1.75} />
        TLS sertifikası
        <StatusBadge tone={expiry.tone}>{expiry.text}</StatusBadge>
      </h2>
      <p className="card-desc">Server’ın agent’lara ve panele sunduğu sertifika. Dosya değişince yeniden başlatmadan yüklenir.</p>
      <dl className="cache-facts">
        <FactRow label="Kime verilmiş">
          <span className="mono">{cert.subject || '—'}</span>
        </FactRow>
        <FactRow label="Geçerli adlar">
          <span className="mono">{names.length > 0 ? names.join(', ') : '—'}</span>
        </FactRow>
        <FactRow label="Veren">{cert.self_signed ? 'kendinden imzalı' : <span className="mono">{cert.issuer}</span>}</FactRow>
        <FactRow label="Geçerlilik">
          {new Date(cert.not_before).toLocaleDateString()} – {new Date(cert.not_after).toLocaleString()}
        </FactRow>
        <FactRow label="Dosya değişti">{new Date(cert.file_modified_at).toLocaleString()}</FactRow>
      </dl>
    </div>
  )
}

function FactRow({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <>
      <dt>{label}</dt>
      <dd>{children}</dd>
    </>
  )
}
