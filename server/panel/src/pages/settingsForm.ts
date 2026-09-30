// Ayarlar sayfasının saf mantığı (React yok; Node'un çalıştırıcısıyla birim test edilir): alanların tanımı, API değeri
// ile formdaki gösterim arasındaki dönüşüm (süreler dakika/saat olarak gösterilir), yalnızca değişen alanlardan
// değişiklik üretme ve bildirimlerin kimseye gitmediği durumu tanıma.

import type { ChannelInfo, NotificationOwner, SettingsField, SettingsValues } from '../types/api.ts'

export interface FieldDef {
  field: SettingsField
  label: string
  hint: string
  kind: 'number' | 'text' | 'select'
  // Sayısal alanlarda gösterim biriminin API birimi karşılığı (ör. dakika = 60 saniye); yoksa 1.
  scale?: number
  unit?: string
  options?: { value: string; label: string }[]
  placeholder?: string
}

export interface SectionDef {
  id: string
  title: string
  description: string
  fields: FieldDef[]
}

export const SETTINGS_SECTIONS: SectionDef[] = [
  {
    id: 'agent',
    title: 'Agent sürüm politikası',
    description:
      'Panel agent’ları bu sürümlere göre “güncel / güncelleme var / desteklenmiyor” diye işaretler; hiçbir agent reddedilmez. Yeni bir agent yayınlandığında en güncel sürümü buradan girin.',
    fields: [
      { field: 'latest_agent_version', label: 'En güncel agent sürümü', hint: 'Boş: politika yok, her agent güncel sayılır.', kind: 'text', placeholder: '1.2.0' },
      {
        field: 'min_supported_agent_version',
        label: 'Desteklenen en düşük sürüm',
        hint: 'Bunun altındaki agent’lar “desteklenmiyor” görünür. Boş: bu durum hiç üretilmez.',
        kind: 'text',
        placeholder: '1.0.0',
      },
    ],
  },
  {
    id: 'saklama',
    title: 'Veri saklama',
    description: 'Eski kayıtlar saatte bir temizlenir. 0 = sonsuza dek sakla.',
    fields: [
      { field: 'metrics_retention_days', label: 'Metrikler', hint: 'Sunucuların ölçüm geçmişi.', kind: 'number', unit: 'gün' },
      { field: 'audit_retention_days', label: 'Denetim kaydı', hint: 'Panel ve API’de yapılan yönetim işlemleri.', kind: 'number', unit: 'gün' },
      { field: 'resolved_alert_retention_days', label: 'Çözülmüş alert’ler', hint: 'Açık ve onaylanmış alert’ler silinmez.', kind: 'number', unit: 'gün' },
    ],
  },
  {
    id: 'oturum',
    title: 'Oturum ve hız sınırları',
    description: 'Oturum süresindeki değişiklik yeni girişlere uygulanır; açık oturumlar kendi süreleriyle devam eder.',
    fields: [
      { field: 'access_token_ttl_seconds', label: 'Erişim süresi', hint: 'Panel bu süre dolunca oturumu sessizce yeniler (1 dk – 24 saat).', kind: 'number', scale: 60, unit: 'dakika' },
      { field: 'refresh_token_ttl_seconds', label: 'Oturum süresi', hint: 'Bu kadar süre işlem yapılmazsa yeniden giriş gerekir (1 saat – 90 gün).', kind: 'number', scale: 3600, unit: 'saat' },
      {
        field: 'rate_limit_auth_failures_per_minute',
        label: 'Başarısız giriş sınırı',
        hint: 'IP başına dakikada; şifre sıfırlama sınırları da buna bağlıdır. 0 = kapalı.',
        kind: 'number',
        unit: '/ dakika',
      },
      { field: 'rate_limit_ingest_per_minute', label: 'Rapor sınırı', hint: 'Push agent başına dakikada kabul edilen rapor. 0 = kapalı.', kind: 'number', unit: '/ dakika' },
    ],
  },
  {
    id: 'panel',
    title: 'Panel adresi',
    description: 'Kullanıcıların tarayıcıda gördüğü adres. Alert e-postalarındaki bağlantının ve “Şifremi unuttum” bağlantısının kökü; boşsa e-posta ile şifre sıfırlama kapalıdır.',
    fields: [{ field: 'panel_base_url', label: 'Adres', hint: 'Ör. https://panel.example.com (yol olmadan).', kind: 'text', placeholder: 'https://panel.example.com' }],
  },
  {
    id: 'log',
    title: 'Loglama',
    description: 'Server logunun ayrıntısı ve log dosyalarının saklanması. Değişiklik hemen uygulanır.',
    fields: [
      {
        field: 'log_level',
        label: 'Log seviyesi',
        hint: 'Sorun ayıklarken geçici olarak “debug” açıp sonra geri alın.',
        kind: 'select',
        options: [
          { value: 'debug', label: 'debug' },
          { value: 'info', label: 'info' },
          { value: 'warn', label: 'warn' },
          { value: 'error', label: 'error' },
        ],
      },
      {
        field: 'log_error_body_bytes',
        label: 'Hata isteklerinde gövde',
        hint: '4xx/5xx isteklerde loga yazılan gövdenin azami boyutu; şifre ve token alanları her zaman maskelenir. 0 = yazılmaz.',
        kind: 'number',
        unit: 'bayt',
      },
      { field: 'log_file_max_age_days', label: 'Log dosyası geçmişi', hint: 'Bugün dahil kaç günün tutulacağı.', kind: 'number', unit: 'gün' },
      { field: 'log_file_max_total_mb', label: 'Log dosyaları toplamı', hint: 'Aşılırsa en eski günler silinir.', kind: 'number', unit: 'MB' },
    ],
  },
]

// toDisplay, bir ayarın API değerini formdaki metnine çevirir (süreler seçilen birime).
export function toDisplay(def: FieldDef, value: string | number): string {
  if (def.kind !== 'number') return String(value)
  const n = Number(value) / (def.scale ?? 1)
  return Number.isInteger(n) ? String(n) : String(Math.round(n * 100) / 100)
}

// fromDisplay, formdaki metni API değerine çevirir; geçersizse hata metni döner.
export function fromDisplay(def: FieldDef, text: string): { value: string | number } | { error: string } {
  if (def.kind !== 'number') return { value: text.trim() }
  const t = text.trim().replace(',', '.')
  if (t === '' || !/^\d+(\.\d+)?$/.test(t)) return { error: 'sayı olmalı' }
  const v = Math.round(Number(t) * (def.scale ?? 1))
  return { value: v }
}

// buildPatch, bir bölümün formundan yalnızca değişen alanları çıkarır. Geçersiz metinler errors'ta döner (alan →
// sorun); o zaman patch gönderilmemelidir.
export function buildPatch(
  section: SectionDef,
  current: SettingsValues,
  draft: Partial<Record<SettingsField, string>>,
): { patch: Partial<SettingsValues>; errors: Partial<Record<SettingsField, string>> } {
  const patch: Record<string, string | number> = {}
  const errors: Partial<Record<SettingsField, string>> = {}
  for (const def of section.fields) {
    const text = draft[def.field]
    if (text === undefined) continue
    const parsed = fromDisplay(def, text)
    if ('error' in parsed) {
      errors[def.field] = parsed.error
      continue
    }
    if (parsed.value !== current[def.field]) patch[def.field] = parsed.value
  }
  return { patch: patch as Partial<SettingsValues>, errors }
}

// fieldMessage, server'ın bir alan için verdiği hatayı alanın gösterim birimine çevirir: süreler formda dakika/saat
// olduğundan "60 ile 86400 arasında olmalı" (saniye) "1 ile 1440 dakika arasında olmalı" olur.
export function fieldMessage(def: FieldDef | undefined, message: string): string {
  const scale = def?.scale ?? 1
  if (!def || scale === 1) return message
  const round = (n: number) => String(Math.round((n / scale) * 100) / 100)
  const range = message.match(/^(\d+) ile (\d+) arasında olmalı$/)
  if (range) return `${round(Number(range[1]))} ile ${round(Number(range[2]))} ${def.unit} arasında olmalı`
  const min = message.match(/^en az (\d+) olmalı$/)
  if (min) return `en az ${round(Number(min[1]))} ${def.unit} olmalı`
  return message
}

// defaultText, bir alanın varsayılanını okunur biçimde verir ("varsayılan: 15 dakika", "varsayılan: boş").
export function defaultText(def: FieldDef, value: string | number): string {
  const shown = toDisplay(def, value)
  if (shown === '') return 'varsayılan: boş'
  return `varsayılan: ${shown}${def.unit ? ` ${def.unit}` : ''}`
}

// channelStatus, bir kanalın durum rozetidir.
export function channelStatus(ch: Pick<ChannelInfo, 'enabled' | 'ready'>): { label: string; tone: 'good' | 'warning' | 'neutral' } {
  if (!ch.ready) return { label: 'Ayar gerekli', tone: 'warning' }
  if (!ch.enabled) return { label: 'Kapalı', tone: 'neutral' }
  return { label: 'Açık', tone: 'good' }
}

// notificationGap, alert bildirimlerinin sistem sahiplerine hiç gitmediği durumu anlatır (uyarı bandı); sorun yoksa null.
// Şimdilik tek kanal e-postadır: kanal kapalıysa ya da e-posta alan bir sahip yoksa kimse bilgilendirilmez.
export function notificationGap(channels: Pick<ChannelInfo, 'channel' | 'enabled'>[], owners: Pick<NotificationOwner, 'email' | 'email_enabled'>[]): string | null {
  const email = channels.find((c) => c.channel === 'email')
  if (!email?.enabled) return 'E-posta kanalı kapalı: alert bildirimleri kimseye gitmiyor.'
  if (!owners.some((o) => o.email && o.email_enabled)) return 'E-posta alan bir sistem sahibi yok: alert bildirimleri kimseye gitmiyor.'
  return null
}

// SETTINGS_CHANGED, Ayarlar sayfasında kanal ya da sahip değiştiğinde yayılan olaydır; uyarı bandı kendini yeniler.
export const SETTINGS_CHANGED = 'healthbeat:settings-changed'
