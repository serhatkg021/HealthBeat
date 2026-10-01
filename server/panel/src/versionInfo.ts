// Sürümün alt çubukta gösterimi — saf mantık. Tek bir sürüm yazılır ("HealthBeat Server 2.0.0"). Panel ve server TEK sürüm numarasıyla, birlikte
// yayınlanır (`server/vX.Y.Z`, bkz. docs/DISTRIBUTION.md). İkisi farklıysa genellikle yalnızca biri yeniden
// kurulmuştur (ör. server imajı güncellenmiş, panel eski kalmış); bu durum sessizce geçmesin diye uyarılır.

export interface VersionInfo {
  panel: string
  server: string
  // İki sürüm de biliniyor ve birbirinden farklıysa true. Biri bilinmiyorsa (server erişilemedi, geliştirme derlemesi)
  // uyarı verilmez: "farklı" diye bir şey söylenemez.
  mismatch: boolean
  // Alt çubukta yazan tek sürüm satırı: server'ın sürümü; server'a ulaşılamadıysa panelin kendi sürümü.
  label: string
  // Fareyle üzerine gelince görünen açıklama; sürümler farklıysa uyarı şeridinde de gösterilir.
  title: string
}

const UNKNOWN = '—'

export function versionInfo(panel: string | undefined, server: string | undefined): VersionInfo {
  const p = panel?.trim() || UNKNOWN
  const s = server?.trim() || UNKNOWN
  const known = (v: string) => v !== UNKNOWN && v !== 'dev'
  const mismatch = known(p) && known(s) && p !== s
  const title = mismatch
    ? `Panel ${p}, server ${s}: ikisi aynı sürümle kurulmalı. Muhtemelen yalnızca biri yeniden kurulmuş; sürüm etiketli imajlarla (HB_VERSION) ikisini birlikte güncelleyin.`
    : `Panel ${p} · Server ${s}`
  const label = `HealthBeat Server ${s !== UNKNOWN ? s : p}`
  return { panel: p, server: s, mismatch, label, title }
}
