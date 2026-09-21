// healthbeat-server/internal/model'ı yansıtır — alan adlarını/biçimlerini, kendi REST
// geleneğimizle değil, o paketle senkron tut.

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

// Bildirim kanalları: veritabanı hepsini tanır, API yalnızca uygulananı (şimdilik e-posta) kabul eder.
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
}

export interface MetricPoint {
  timestamp: string
  cpu_usage_pct: number
  ram_usage_pct: number
  disk: DiskUsage[]
}

export type MetricType = 'cpu' | 'ram' | 'disk' | 'docker_restart'

export interface ThresholdConfig {
  id: string
  // Yoksa genel varsayılan; sunucuya özel eşikler /hosts/:id/thresholds'tan yönetilir.
  organization_id?: string
  metric_type: MetricType
  warning_level: number
  critical_level: number
  created_at: string
  updated_at: string
}

export interface ThresholdLevels {
  warning_level: number
  critical_level: number
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
}

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
export type AlertType = MetricType | 'host_offline' | 'disk_missing'

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
}

export interface DiskAlertSettings {
  all_mounts_alert: boolean
  custom_alert_mounts: string[]
  reported: DiskUsage[]
}
