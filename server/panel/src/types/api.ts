// healthbeat-server/internal/model'ı yansıtır — alan adlarını/biçimlerini, kendi REST
// geleneğimizle değil, o paketle senkron tut.

import type { Permission } from '../auth/permissions'

export type Role = 'super_admin' | 'org_admin' | 'operator'

export interface User {
  id: string
  email: string
  // Görünen ad; giriş e-postayla yapılır.
  full_name?: string
  role: Role
  phone?: string
  // İleride iki faktörlü doğrulama için hesap bazlı anahtar (henüz uygulanmadı: yalnızca saklanır).
  two_factor_enabled: boolean
  two_factor_channel?: string
  created_at: string
  last_login_at?: string
  // Panel kullanılmadan önce yeni bir şifre seçmesi gereken hesaplar için ayarlanır.
  must_change_password?: boolean
  // Rolün izin anahtarları; yalnızca GET /api/v1/me döndürür (giriş yanıtında yoktur).
  permissions?: Permission[]
}

export interface Organization {
  id: string
  // Organizasyon ağacındaki üst şirket; yoksa kök.
  parent_organization_id?: string
  name: string
  // Yalnızca tam erişimli organizasyonlarda gelir.
  address?: string
  created_at: string
  // Listeyi isteyen kullanıcının erişimi: 'full' = kendi dalı, 'context' = yalnızca üst zincir
  // (ad ve konum bilgi olarak görünür; içeriği ve kardeş dalları görünmez).
  access?: OrgAccess
}

export type OrgAccess = 'full' | 'context'

// Bir organizasyonun sunucu işleri için başvurulacak kişisi (panel kullanıcısı olması gerekmez).
export interface OrganizationContact {
  id: string
  organization_id: string
  department?: string
  title?: string
  name: string
  manager_contact_id?: string
  phone?: string
  email?: string
  created_at: string
  updated_at: string
}

export interface ContactInput {
  name: string
  department?: string
  title?: string
  manager_contact_id?: string | null
  phone?: string
  email?: string
}

// Bildirim kanallarının adları. Hangilerinin tanımlı ve açık olduğunu server söyler (GET /notification-channels/options).
export type NotificationChannel = 'email' | 'sms' | 'slack' | 'discord' | 'telegram'

// Bir alert'in kime, hangi kanaldan ve en az hangi seviyeden gideceği. Kapsam bir organizasyon (altındaki dal için de
// geçerli) ya da tek bir sunucudur; alıcı bir panel kullanıcısı ya da bir iletişim kişisidir.
export interface NotificationRoute {
  id: string
  organization_id?: string
  host_id?: string
  user_id?: string
  contact_id?: string
  channel: NotificationChannel
  min_level: AlertLevel
  created_at: string
  recipient_name?: string
  recipient_target?: string
  // Kuralın kanalı kapalıysa false: kural çalışmaz.
  channel_enabled: boolean
}

// Bir kapsam için alıcı olabilecekler; kural yalnızca bunlara yazılabilir.
export interface RecipientCandidate {
  user_id?: string
  contact_id?: string
  name: string
  email?: string
  phone?: string
  source: 'super_admin' | 'org_admin' | 'operator' | 'contact'
}

export type HostMode = 'push' | 'pull'
export type HostStatus = 'online' | 'offline'

export interface Host {
  id: string
  organization_id: string
  // Panelde görünen ad (organizasyon içinde benzersiz); makinenin kendi bildirdiği hostname host_info.hostname'dedir.
  title: string
  ip: string
  mode: HostMode
  interval_seconds: number
  status: HostStatus
  last_seen?: string
  pull_port?: number
  pull_endpoint?: string
  // true = agent'ın raporladığı her mount disk alert'i üretebilir; false = yalnızca custom_alert_mounts
  // ([] = hiçbiri).
  all_mounts_alert: boolean
  custom_alert_mounts: string[]
  // Agent'ın son raporunda bildirdiği donanım toplamları; eski agent sürümlerinde yoktur.
  cpu_cores?: number
  ram_total_mb?: number
  // Agent'ın son raporundaki fiziksel diskler ve üzerlerindeki mount'lar; eski agent'larda ya da
  // diskler keşfedilemediğinde yoktur.
  physical_disks?: PhysicalDisk[]
  // Agent'ın son isteğinde başlıktan bildirdiği sürüm. agent_protocol 1 ve agent_version yok =
  // sürüm bildirmeyen eski agent; ikisi de yoksa henüz rapor gelmedi.
  agent_version?: string
  agent_protocol?: number
  // Agent'ın son raporunda gönderdiği ama bu server sürümünün tanımadığı alan adları: agent
  // server'dan yeni, server güncellenmeli. Yoksa/boşsa bilinmeyen alan yok.
  unsupported_fields?: string[]
  // Makine envanteri ve anlık durumu (protokol 3); eski agent'larda yoktur.
  host_info?: HostInfo
  // Protokol 4'ün geçmişi tutulmayan anlık durumları (sıcaklık, RAID, kapasite, süreçler …). Yalnızca tek sunucu
  // yanıtında (GET /hosts/:id) gelir; eski agent'ta yoktur.
  system_state?: SystemState
  created_at: string
  updated_at: string
  // Aynı makine kimliğini bildiren diğer sunucular (çift kayıt uyarısı); yalnızca tek sunucu yanıtında,
  // yalnızca çağıranın görebildikleri. Klon sanal makineler aynı kimliği taşıyabilir: yalnızca uyarıdır.
  same_machine_as?: { id: string; title: string; organization_id: string }[]
  // Yalnızca oluşturma/rotate-credentials'tan hemen sonra bir kez bulunur.
  api_token?: string
  pull_secret?: string
}

// Bir mount birden çok diske düşebilir (LVM/mdraid); hiçbir diske düşmeyenler (NFS, tmpfs) listelenmez.
export interface PhysicalDisk {
  name: string
  model?: string
  // Aygıtın ham boyutu (bayt), üzerindeki dosya sisteminin değil.
  size_bytes?: number
  kind?: string // nvme | ssd | hdd
  mounts: string[]
}

export interface DiskUsage {
  mount: string
  used_pct: number
  total: number
  free: number
  // Inode doluluğu (protokol 3); yoksa bilinmiyor (eski agent ya da inode bildirmeyen dosya sistemi).
  inodes_used_pct?: number
}

// Agent'ın bildirdiği makine envanteri ve anlık durumu (protokol 3). Yalnızca bilgi içindir; her alan
// isteğe bağlıdır ("bilinmiyor" = alan yok). reboot_required/time_synced/failed_units için undefined =
// bilinmiyor, false/0 = biliniyor ve hayır/sıfır.
export interface HostInfo {
  hostname?: string
  os?: { id?: string; name?: string; pretty_name?: string; version_id?: string; id_like?: string }
  kernel?: { release?: string; arch?: string }
  cpu_model?: string
  virtualization?: { kind?: 'physical' | 'vm' | 'container' | 'unknown'; vendor?: string }
  machine?: { vendor?: string; model?: string }
  machine_id_hash?: string
  timezone?: string
  init?: string
  security_module?: string
  boot_time?: string
  uptime_seconds?: number
  load_avg?: number[]
  swap?: { total_mb: number; used_mb: number }
  addresses?: { interface?: string; address: string }[]
  reboot_required?: boolean
  time_synced?: boolean
  failed_units?: number
  docker_version?: string
}

export interface DockerContainerReport {
  name: string
  image: string
  status: string
  cpu_pct: number
  ram_mb: number
  restart_count: number
  uptime_seconds: number
  // Protokol 4: healthcheck sonucu (healthcheck yoksa yok), üst üste başarısız kontrol sayısı, son çıkış kodu ve bellek
  // yetmediği için öldürülüp öldürülmediği. undefined = bilinmiyor.
  health?: 'healthy' | 'unhealthy' | 'starting'
  health_failing_streak?: number
  exit_code?: number
  oom_killed?: boolean
}

export interface MetricPoint {
  timestamp: string
  cpu_usage_pct: number
  ram_usage_pct: number
  disk: DiskUsage[]
  // Protokol 4 zaman serisi; eski agent'ın satırlarında yoktur. Değerler ham gelir, yuvarlama gösterimde (units.ts).
  system?: SystemSample
  disk_io?: DiskIOSample[]
  net_io?: NetIOSample[]
}

// ---------------------------------------------------------------- Protokol 4: sistem sağlığı ve performans
// healthbeat-server/internal/model/ingest_v4.go'yu yansıtır. Her alan isteğe bağlıdır: yoksa "bilinmiyor".

// Bir metrik satırının makine geneli örneği.
export interface SystemSample {
  cpu_detail?: { iowait_pct?: number; steal_pct?: number; procs_blocked?: number }
  memory_detail?: { available_mb?: number; cached_mb?: number; swap_in_per_s?: number; swap_out_per_s?: number }
  pressure?: Pressure
  tcp?: { retrans_pct?: number; established?: number; time_wait?: number }
}

// PSI: süreçlerin kaynak beklerken geçirdiği zamanın yüzdesi (10 ve 60 sn ortalaması). CPU'da full gelmez.
export interface Pressure {
  cpu?: PressureStall
  memory?: PressureStall
  io?: PressureStall
}

export interface PressureStall {
  some10: number
  some60: number
  full10?: number
  full60?: number
}

// Bir fiziksel diskin G/Ç'si: hızlar bayt/sn, await_ms bir işlemin ortalama süresi.
export interface DiskIOSample {
  name: string
  read_iops: number
  write_iops: number
  read_bps: number
  write_bps: number
  util_pct: number
  await_ms: number
  queue_depth: number
}

// Bir ağ arayüzünün trafiği: hızlar bit/sn; hata ve düşen paketler aralıktaki farktır.
export interface NetIOSample {
  interface: string
  rx_bps: number
  tx_bps: number
  rx_errors: number
  tx_errors: number
  rx_drops: number
  tx_drops: number
}

// Agent'ın son raporundaki anlık durumlar (host_status.system_state).
export interface SystemState {
  // Açılıştan beri bellek yetmediği için öldürülen süreç sayısı ve sayacın en son arttığının görüldüğü an.
  oom_kills?: number
  oom_last_increase_at?: string
  temperatures?: Temperature[]
  raid?: RAIDArray[]
  capacity?: Capacity
  processes?: Processes
  updates?: PendingUpdates
  time_sync?: TimeSync
}

// max/crit donanımın bildirdiği sınırlardır.
export interface Temperature {
  sensor: string
  kind?: 'cpu' | 'disk' | 'other'
  celsius: number
  max?: number
  crit?: number
}

export interface RAIDArray {
  name: string
  level?: string
  state: string // clean | degraded | recovering | resyncing | failed …
  devices: number
  active: number
  sync_pct?: number
}

export interface Capacity {
  file_handles?: number
  file_handles_max?: number
  conntrack?: number
  conntrack_max?: number
  // Süreç + iş parçacığı sayısı: pid_max sınırı bunlara uygulanır.
  tasks?: number
  pid_max?: number
}

// Aynı adlı süreçlerin toplamı; CPU yüzdesi makinenin toplam kapasitesine göredir.
export interface ProcessGroup {
  name: string
  count: number
  cpu_pct: number
  rss_mb: number
}

export interface Processes {
  total: number
  zombie: number
  top_cpu?: ProcessGroup[]
  top_ram?: ProcessGroup[]
}

export interface PendingUpdates {
  pending: number
  security: number
  // Paket listelerinin en son güncellendiği an.
  lists_updated_at?: string
}

export interface TimeSync {
  enabled?: boolean
  synchronized?: boolean
  daemon?: string // timesyncd | chrony | ntpd | none
  local_rtc?: boolean
  server?: string
  server_address?: string
  configured_servers?: string[]
  stratum?: number
  leap?: string // normal | insert | delete | alarm
  offset_ms?: number
  delay_ms?: number
  jitter_ms?: number
  root_distance_ms?: number
  poll_s?: number
  last_sync?: string
  // Saat sunucusunun son yanıtı geçersiz sayıldı.
  ignored?: boolean
  sources?: TimeSource[]
}

export interface TimeSource {
  name: string
  state: string // selected | candidate | falseticker | unreachable | unusable
  // Son 8 denemenin bit maskesi (255 = hepsi başarılı).
  reach?: number
  offset_ms?: number
}

// Bir sunucunun saklanan systemd servisi (GET /hosts/:id/services). watched = izlenen servisler arasında.
export interface HostService {
  name: string
  description?: string
  active: string // active | inactive | failed | activating | deactivating …
  sub?: string
  // Bu duruma geçtiği an.
  since?: string
  restarts?: number
  enabled?: string
  // Satırın içeriğinin en son değiştiği an.
  updated_at: string
  watched: boolean
}

// watched seçimin tamamıdır: şu an raporlanmayan izlenen servisler services'te yoktur.
export interface HostServices {
  services: HostService[]
  watched: string[]
}

// Eşik taşıyan metrikler (server'ın model.ThresholdMetricTypes'ı). İlk dördü anlık değerlendirilir; protokol 4
// türlerine süre koşulu verilebilir ve sunucu kapsamında konu (disk, sensör, servis) başına eşik tanımlanabilir.
export type MetricType = 'cpu' | 'ram' | 'disk' | 'docker_restart' | 'disk_latency' | 'temperature' | 'service_restart' | 'time_offset'

// Konu bazlı eşik verilebilen protokol 4 türleri.
export type SubjectMetricType = 'disk_latency' | 'temperature' | 'service_restart'

export interface ThresholdConfig {
  id: string
  // Yoksa genel varsayılan; sunucuya özel eşikler /hosts/:id/thresholds'tan yönetilir.
  organization_id?: string
  metric_type: MetricType
  warning_level: number
  critical_level: number
  // Eşik kesintisiz bu kadar saniye aşılınca alert açılır; yoksa hemen (yalnızca protokol 4 türlerinde).
  duration_seconds?: number
  created_at: string
  updated_at: string
}

export interface ThresholdLevels {
  warning_level: number
  critical_level: number
  duration_seconds?: number
}

// Bir sunucu için bir metriğin eşik durumu: varsayılan olarak ne aldığı ve kendi özel değerleri
// (ayarlıysa özel kazanır). Metrik için hiçbir şey tanımlı değilse default null'dır; yani
// alert üretmez.
export interface HostThresholdView {
  metric_type: MetricType
  default: ThresholdLevels | null
  custom: ThresholdLevels | null
}

// Bir mount'un kendi disk eşiği; kendi eşiği olmayan mount'lar sunucunun disk eşiğini izler.
export interface MountThreshold {
  mount: string
  custom: ThresholdLevels
}

export interface HostThresholdsResponse {
  thresholds: HostThresholdView[]
  mount_thresholds: MountThreshold[]
  container_thresholds: ContainerThreshold[]
  // Protokol 4 türlerinde kendi eşiği olan konular (disk, sensör, servis); diğerleri sunucu genelindekini izler.
  subject_thresholds: SubjectThreshold[]
}

export interface SubjectThreshold {
  metric_type: SubjectMetricType
  subject: string
  custom: ThresholdLevels
}

// tür -> konu -> null (sunucunun eşiğini izle) ya da kendi seviyeleri; dışarıda bırakılana dokunulmaz.
export type SubjectThresholdOverrides = Partial<Record<SubjectMetricType, SubjectOverrides>>

// subject (mount yolu / container adı) -> null (sunucunun eşiğini izle) ya da kendi çifti;
// dışarıda bırakılan bir subject'e dokunulmaz.
export type SubjectOverrides = Record<string, ThresholdLevels | null>
export type MountOverrides = SubjectOverrides
export type ContainerOverrides = SubjectOverrides

// Bir container'ın kendi docker_restart eşiği.
export interface ContainerThreshold {
  container: string
  custom: ThresholdLevels
}

// null = varsayılanı kullan; bir çift = özel değerler; dışarıda bırakılan bir metriğe dokunulmaz.
export type ThresholdOverrides = Partial<Record<MetricType, ThresholdLevels | null>>

export type AlertLevel = 'info' | 'warning' | 'critical'
export type AlertStatus = 'open' | 'acknowledged' | 'resolved'
// Sayısal alert'ler eşik türünün adını taşır (service_restart ve time_offset hariç: onların alert'i service_restart_loop
// ve time_sync/offset'tir); diğerleri olay ya da durum alert'idir.
export type AlertType =
  | Exclude<MetricType, 'service_restart' | 'time_offset'>
  | 'host_offline'
  | 'disk_missing'
  | 'service_failed'
  | 'service_restart_loop'
  | 'container_unhealthy'
  | 'container_oom'
  | 'oom_kill'
  | 'fs_readonly'
  | 'raid_degraded'
  | 'time_sync'
  | 'reboot_required'
  | 'security_updates'

// time_sync alert'inin konuları: sorun türü.
export type TimeSyncSubject = 'unsynced' | 'source' | 'offset'

// Durum kuralları: eşiği olmayan alert'lerin seviyesi ve süresi (server'ın model.StatusRules'u). raid_degraded ve
// raid_rebuilding ikisi de raid_degraded alert'idir; time_unsynced ve time_source time_sync alert'idir.
export type StatusRule =
  | 'service_failed'
  | 'container_unhealthy'
  | 'container_oom'
  | 'oom_kill'
  | 'fs_readonly'
  | 'raid_degraded'
  | 'raid_rebuilding'
  | 'time_unsynced'
  | 'time_source'
  | 'reboot_required'
  | 'security_updates'

// off, üst kapsamda açık bir kuralı bu kapsamda kapatır.
export type StatusRuleLevel = 'off' | AlertLevel

export interface StatusRuleSetting {
  level: StatusRuleLevel
  duration_seconds?: number
}

// Saklanan bir kural satırı (GET/PUT /status-rules); organization_id yoksa geneldir.
export interface StatusRuleConfig extends StatusRuleSetting {
  organization_id?: string
  rule: StatusRule
  updated_at: string
}

// Bir sunucu için bir kural (GET/PUT /hosts/:id/status-rules): default üst kapsamlardan geleni (null = kapalı), custom
// sunucunun kendi ayarıdır. takes_duration, kurala süre verilip verilemeyeceğidir.
export interface HostStatusRuleView {
  rule: StatusRule
  takes_duration: boolean
  default: StatusRuleSetting | null
  custom: StatusRuleSetting | null
}

// kural -> ayar ya da null (bu kapsamdaki ayarı kaldır, üst kapsamı izle); dışarıda bırakılana dokunulmaz.
export type StatusRuleChanges = Partial<Record<StatusRule, StatusRuleSetting | null>>

export interface Alert {
  id: string
  host_id: string
  alert_type: AlertType
  // Alert'in sunucu içinde neyle ilgili olduğu — mount yolu ya da container adı.
  subject?: string
  level: AlertLevel
  status: AlertStatus
  // Tetiklendiği andaki ölçüm ve o seviyeyi tetikleyen eşik (olay alert'lerinde yok).
  value?: number
  threshold?: number
  created_at: string
  acknowledged_at?: string
  acknowledged_by?: string
  resolved_at?: string
  // Yalnızca alert listelerinde: bildirimlerin toplu durumu (en az biri gitmediyse failed, bekleyen varsa pending, hepsi
  // gittiyse sent). Yoksa alert'in bildirimi olmamıştır (ör. alıcı yoktu).
  notification_status?: NotificationStatus
}

export type NotificationStatus = 'sent' | 'pending' | 'failed'
export type AlertEvent = 'opened' | 'level_changed' | 'resolved'

// Bir alert bildiriminin teslim kaydı (GET /alerts/:id/notifications). Alıcılar ve son hata yalnızca notification.view
// izniyle gelir; yoksa yalnızca recipient_count vardır.
export interface AlertNotification {
  id: string
  event: AlertEvent
  level: AlertLevel
  channel: string
  status: NotificationStatus
  attempts: number
  recipient_count: number
  recipients?: string[]
  last_error?: string
  subject: string
  body: string
  created_at: string
  sent_at?: string
  failed_at?: string
  next_attempt_at?: string
}

export interface AuditLogEntry {
  id: string
  user_id?: string
  actor_email: string
  action: string
  target_type: string
  target_id?: string
  details?: Record<string, unknown>
  created_at: string
}

export interface AuditLogPage {
  items: AuditLogEntry[]
  next_cursor: string | null
}

// Özet ekranındaki süzgeçlerin çalıştığı veri (GET /dashboard/overview). Host'lar bilerek dardır.
export interface OverviewHost {
  id: string
  organization_id: string
  title: string
  ip: string
  mode: HostMode
  status: HostStatus
  last_seen?: string
  agent_version?: string
  agent_protocol?: number
}

// GET /api/v1/meta: server sürümü ve agent sürüm politikası (boş = tanımsız).
export interface Meta {
  server_version: string
  protocol: number
  latest_agent_version: string
  min_agent_version: string
}

export interface DashboardOverview {
  // Operatör için boştur (organization.view yetkisi yok).
  organizations: { id: string; name: string }[]
  hosts: OverviewHost[]
  // Yalnızca açık alert'ler.
  alerts: Alert[]
}

export interface DashboardSummary {
  total_hosts: number
  online_hosts: number
  offline_hosts: number
  open_alerts: number
  open_critical_alerts: number
  open_warning_alerts: number
  open_info_alerts: number
}

export interface DiskAlertSettings {
  all_mounts_alert: boolean
  custom_alert_mounts: string[]
  reported: DiskUsage[]
}

// Çalışma zamanı ayarları (GET /api/v1/settings). Süreler saniyedir; sürümlerde "" tanımsızdır.
export interface SettingsValues {
  latest_agent_version: string
  min_supported_agent_version: string
  metrics_retention_days: number
  audit_retention_days: number
  resolved_alert_retention_days: number
  access_token_ttl_seconds: number
  refresh_token_ttl_seconds: number
  rate_limit_auth_failures_per_minute: number
  rate_limit_ingest_per_minute: number
  panel_base_url: string
  log_level: string
  log_error_body_bytes: number
  log_file_max_age_days: number
  log_file_max_total_mb: number
}

export type SettingsField = keyof SettingsValues

export interface UserRef {
  id: string
  name: string
}

export interface SettingsResponse {
  values: SettingsValues
  defaults: SettingsValues
  changed: SettingsField[]
  updated_at: string
  updated_by: UserRef | null
}

// E-posta kanalının (smtp) ayarı; şifre ayrıca gönderilir ve asla geri okunmaz.
export interface SMTPConfig {
  host: string
  port: number
  username: string
  from: string
}

// Sistem düzeyindeki bir bildirim kanalı (GET /api/v1/notification-channels).
export interface ChannelInfo {
  channel: NotificationChannel
  provider: string
  enabled: boolean
  config: SMTPConfig
  secret_set: boolean
  // Kayıtlı şifre server'ın anahtarıyla çözülemiyor (SECRETS_ENCRYPTION_KEY değişmiş): yeniden girilene kadar kanal gönderemez.
  secret_unreadable: boolean
  owner_min_level: AlertLevel
  verified_at: string | null
  updated_at: string
  updated_by: UserRef | null
  ready: boolean
  implemented: boolean
  personal: boolean
  rule_count: number
}

// Kural ekranının kanal bilgisi (GET /api/v1/notification-channels/options; ayrıntısız).
export interface ChannelOption {
  channel: NotificationChannel
  enabled: boolean
  ready: boolean
  personal: boolean
  implemented: boolean
}

// Her alert'in bildirimini alan bir sistem sahibi (panel kullanıcısı olması gerekmez).
export interface NotificationOwner {
  id: string
  name: string
  email: string | null
  phone: string | null
  email_enabled: boolean
  sms_enabled: boolean
  created_at: string
  updated_at: string
}

// ---------------------------------------------------------------- Sistem Araçları

// Bildirim kuyruğundaki bir satırın durumu: pending henüz başarısız denemesi olmayan, retrying yeniden denenecek olandır.
export type QueueStatus = 'pending' | 'retrying' | 'sent' | 'failed'
export type QueueKind = 'alert' | 'password_reset' | 'password_changed'

export interface QueueCounts {
  pending: number
  retrying: number
  sent: number
  failed: number
}

// Kuyruğun özeti ve veritabanı bağlantı havuzu (GET /api/v1/system/queue).
export interface QueueSummary extends QueueCounts {
  by_kind: (QueueCounts & { kind: QueueKind })[]
  // Bitmemiş en eski satırın oluşturulma zamanı; bitmemiş satır yoksa null.
  oldest_active_at: string | null
  max_attempts: number
  retain_finished_days: number
  db_pool: { acquired: number; idle: number; total: number; max: number }
}

// Kuyruktaki bir satır (GET /api/v1/system/queue/items). İleti gövdesi hiç gelmez.
export interface QueueItem {
  id: string
  kind: QueueKind
  channel: string
  recipients: string[]
  subject: string
  status: QueueStatus
  attempts: number
  last_error: string
  alert_id: string | null
  alert_event: AlertEvent | null
  alert_level: AlertLevel | null
  created_at: string
  next_attempt_at: string | null
  expires_at: string | null
  sent_at: string | null
  failed_at: string | null
}

export interface QueueItemPage {
  items: QueueItem[]
  next_cursor: string | null
}

// Hız sınırlayıcının bir anahtarı: remaining, anahtarın şu an kullanabileceği hak sayısıdır (0 ile burst arası).
export interface LimiterEntry {
  key: string
  // Anahtarın okunur adı (host anahtarında sunucu başlığı); yoksa boş.
  label: string
  remaining: number
  last_used_at: string
}

export type LimiterKeyKind = 'ip' | 'email' | 'host'

export interface RateLimiterStatus {
  id: string
  key_kind: LimiterKeyKind
  per_minute: number
  burst: number
  enabled: boolean
  // Hakkı eksik olan anahtar sayısı; entries bunların en çok 200'ünü (en az hakkı kalan önce) taşır.
  keys: number
  entries: LimiterEntry[]
}

// Server sürecinin bellekte tuttuğu durum (GET /api/v1/system/cache). Bağlı olmayan kaynak null gelir.
export interface CacheStatus {
  generated_at: string
  permissions: {
    ttl_seconds: number
    roles: { role: string; permissions: string[]; expires_at: string }[]
  }
  rate_limiters: RateLimiterStatus[]
  pull_scheduler: {
    verifies_tls: boolean
    hosts: { host_id: string; title: string; last_polled_at: string; in_flight: boolean }[]
  } | null
  trusted_proxies: {
    prefixes: string[]
    hosts: { name: string; addrs: string[]; failing: boolean }[]
  } | null
  tls_certificate: {
    subject: string
    issuer: string
    dns_names: string[]
    ips: string[]
    self_signed: boolean
    not_before: string
    not_after: string
    file_modified_at: string
  } | null
}

// Bir günün log dosyaları: parts dosya sayısı, bytes diskteki boyut, compressed hepsinin gzip'li olduğu.
export interface LogDay {
  day: string
  parts: number
  bytes: number
  compressed: boolean
}

// Log dosyası olan günler (en yeni önce) ve saklama sınırları (GET /api/v1/system/logs). enabled false ise server
// dosyaya loglamıyor.
export interface LogFiles {
  enabled: boolean
  days: LogDay[]
  total_bytes: number
  max_total_bytes: number
  max_age_days: number
}

// Ayrıştırılmış bir log satırı. Ayrıştırılamayan satırda time null, level boş ve message satırın ham hâlidir.
export interface LogEntry {
  // Satırın o gün içindeki sıra numarası.
  line: number
  time: string | null
  level: string
  message: string
  attrs: { key: string; value: string }[]
  truncated?: boolean
}

// Bir günün satırlarının bir sayfası, en yeni önce (GET /api/v1/system/logs/entries). next_before 0 ise daha eski satır yok.
export interface LogPage {
  entries: LogEntry[]
  next_before: number
  scanned: number
}
