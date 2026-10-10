import { apiDownload, apiRequest, apiRequestPage } from './client'
import type {
  Alert,
  ChannelInfo,
  ChannelOption,
  NotificationOwner,
  SettingsField,
  SettingsResponse,
  SettingsValues,
  SMTPConfig,
  AlertNotification,
  AuditLogPage,
  CacheStatus,
  LogFiles,
  LogPage,
  QueueItemPage,
  QueueSummary,
  AlertStatus,
  Host,
  HostMode,
  HostServices,
  HostStatusRuleView,
  HostThresholdsResponse,
  StatusRuleChanges,
  StatusRuleConfig,
  SubjectThresholdOverrides,
  ContainerOverrides,
  DashboardOverview,
  DashboardSummary,
  DiskAlertSettings,
  DockerContainerReport,
  MetricPoint,
  MaintenanceInput,
  MaintenanceList,
  MaintenanceOccurrence,
  MaintenanceWindow,
  Meta,
  MetricType,
  MountOverrides,
  NotificationChannel,
  NotificationRoute,
  Organization,
  OrganizationContact,
  ContactInput,
  RecipientCandidate,
  AlertLevel,
  Role,
  ThresholdOverrides,
  ThresholdConfig,
  User,
} from '../types/api'

export interface LoginResponse {
  access_token: string
  access_token_expires_at: string
  refresh_token: string
  refresh_token_expires_at: string
  user: User
}

export const authApi = {
  // Kullanıcıyı yeni token'larla yeniden oturum açtırır (server daha eski her oturumu sonlandırır).
  changePassword: (currentPassword: string, newPassword: string) =>
    apiRequest<LoginResponse>('/api/v1/me/password', {
      method: 'POST',
      body: { current_password: currentPassword, new_password: newPassword },
    }),
  login: (email: string, password: string) =>
    apiRequest<LoginResponse>('/api/v1/auth/login', { method: 'POST', body: { email, password } }),
  // Şifre sıfırlama (kimlik doğrulamasız, e-posta ile): bkz. docs/DEPLOYMENT.md.
  options: () => apiRequest<{ password_reset_enabled: boolean }>('/api/v1/auth/options'),
  // Her zaman başarılı döner (hesabın var olup olmadığı sızdırılmaz); yalnızca biçim hatası 400, aşırı istek 429.
  forgotPassword: (email: string) => apiRequest<void>('/api/v1/auth/forgot-password', { method: 'POST', body: { email } }),
  resetPassword: (token: string, newPassword: string) =>
    apiRequest<void>('/api/v1/auth/reset-password', { method: 'POST', body: { token, new_password: newPassword } }),
}

export const dashboardApi = {
  summary: () => apiRequest<DashboardSummary>('/api/v1/dashboard/summary'),
  overview: () => apiRequest<DashboardOverview>('/api/v1/dashboard/overview'),
}

const maintenancePath = '/api/v1/maintenance-windows'

export const maintenanceApi = {
  list: () => apiRequest<MaintenanceList>(maintenancePath),
  get: (id: string) => apiRequest<MaintenanceWindow>(`${maintenancePath}/${id}`),
  preview: (input: MaintenanceInput) =>
    apiRequest<{ timezone: string; upcoming: MaintenanceOccurrence[] }>(`${maintenancePath}/preview`, { method: 'POST', body: input }),
  create: (input: MaintenanceInput) => apiRequest<MaintenanceWindow>(maintenancePath, { method: 'POST', body: input }),
  update: (id: string, input: MaintenanceInput) => apiRequest<MaintenanceWindow>(`${maintenancePath}/${id}`, { method: 'PUT', body: input }),
  remove: (id: string) => apiRequest<void>(`${maintenancePath}/${id}`, { method: 'DELETE' }),
  end: (id: string) => apiRequest<MaintenanceWindow>(`${maintenancePath}/${id}/end`, { method: 'POST' }),
  endOccurrence: (id: string) => apiRequest<MaintenanceWindow>(`${maintenancePath}/${id}/end-occurrence`, { method: 'POST' }),
  skipNext: (id: string) => apiRequest<MaintenanceWindow>(`${maintenancePath}/${id}/skip-next`, { method: 'POST' }),
}

export const metaApi = {
  get: () => apiRequest<Meta>('/api/v1/meta'),
}

export const meApi = {
  get: () => apiRequest<User>('/api/v1/me'),
  update: (input: { full_name?: string; phone?: string }) => apiRequest<User>('/api/v1/me', { method: 'PATCH', body: input }),
}

export const organizationsApi = {
  list: () => apiRequest<Organization[]>('/api/v1/organizations'),
  get: (id: string) => apiRequest<Organization>(`/api/v1/organizations/${id}`),
  create: (input: { name: string; parent_organization_id?: string; address?: string }) =>
    apiRequest<Organization>('/api/v1/organizations', { method: 'POST', body: input }),
  // parent_organization_id: verilmezse değişmez, null = kök yap, bir kimlik = o organizasyonun altına taşı (yalnızca
  // super_admin). address: boş metin adresi temizler.
  update: (id: string, patch: { name?: string; address?: string; parent_organization_id?: string | null }) =>
    apiRequest<Organization>(`/api/v1/organizations/${id}`, { method: 'PUT', body: patch }),
  remove: (id: string) => apiRequest<void>(`/api/v1/organizations/${id}`, { method: 'DELETE' }),
}

export const contactsApi = {
  list: (organizationId: string) => apiRequest<OrganizationContact[]>(`/api/v1/organizations/${organizationId}/contacts`),
  create: (organizationId: string, input: ContactInput) =>
    apiRequest<OrganizationContact>(`/api/v1/organizations/${organizationId}/contacts`, { method: 'POST', body: input }),
  update: (id: string, input: ContactInput) => apiRequest<OrganizationContact>(`/api/v1/contacts/${id}`, { method: 'PUT', body: input }),
  remove: (id: string) => apiRequest<void>(`/api/v1/contacts/${id}`, { method: 'DELETE' }),
}

// Bildirim kuralları: kapsam bir organizasyon ya da bir sunucudur.
export type RouteScope = { organizationId: string } | { hostId: string }

export interface CreateRouteInput {
  user_id?: string
  contact_id?: string
  channel: NotificationChannel
  min_level: AlertLevel
}

export const notificationsApi = {
  list: (scope: RouteScope) =>
    apiRequest<NotificationRoute[]>(
      'organizationId' in scope ? `/api/v1/organizations/${scope.organizationId}/notification-routes` : `/api/v1/hosts/${scope.hostId}/notification-routes`,
    ),
  candidates: (scope: RouteScope) =>
    apiRequest<RecipientCandidate[]>(
      'organizationId' in scope
        ? `/api/v1/organizations/${scope.organizationId}/notification-recipients`
        : `/api/v1/hosts/${scope.hostId}/notification-recipients`,
    ),
  create: (scope: RouteScope, input: CreateRouteInput) =>
    apiRequest<NotificationRoute>('/api/v1/notification-routes', {
      method: 'POST',
      body: { ...('organizationId' in scope ? { organization_id: scope.organizationId } : { host_id: scope.hostId }), ...input },
    }),
  update: (id: string, patch: { channel?: NotificationChannel; min_level?: AlertLevel }) =>
    apiRequest<NotificationRoute>(`/api/v1/notification-routes/${id}`, { method: 'PUT', body: patch }),
  remove: (id: string) => apiRequest<void>(`/api/v1/notification-routes/${id}`, { method: 'DELETE' }),
}

export interface CreateHostInput {
  organization_id: string
  // Panelde görünen ad (organizasyon içinde benzersiz).
  title: string
  ip: string
  mode: HostMode
  interval_seconds: number
  pull_port?: number
  pull_endpoint?: string
  // Verilmezse (ya da true) raporlanan her mount için alert; false ise yalnızca custom_alert_mounts.
  all_mounts_alert?: boolean
  custom_alert_mounts?: string[]
  // Metrik başına özel eşikler; null ya da verilmemiş = varsayılan. Sunucuyla birlikte kaydedilir.
  thresholds?: ThresholdOverrides
  // Mount başına disk eşikleri ({"/storage": {warning_level, critical_level}}).
  mount_thresholds?: MountOverrides
  // Container başına docker_restart eşikleri ({"web": {warning_level, critical_level}}).
  container_thresholds?: ContainerOverrides
}

// Sunucu eşiklerinin isteğe bağlı konu bazlı kısımları; verilmeyen kısma dokunulmaz.
export interface HostSubjectOverrides {
  mounts?: MountOverrides
  containers?: ContainerOverrides
  // Protokol 4 türlerinde disk, sensör ya da servis başına ({"disk_latency": {"sda": {...}}}).
  subjects?: SubjectThresholdOverrides
}

export const hostsApi = {
  thresholds: (id: string) => apiRequest<HostThresholdsResponse>(`/api/v1/hosts/${id}/thresholds`),
  setThresholds: (id: string, thresholds: ThresholdOverrides, parts: HostSubjectOverrides = {}) =>
    apiRequest<HostThresholdsResponse>(`/api/v1/hosts/${id}/thresholds`, {
      method: 'PUT',
      body: {
        thresholds,
        ...(parts.mounts ? { mount_thresholds: parts.mounts } : {}),
        ...(parts.containers ? { container_thresholds: parts.containers } : {}),
        ...(parts.subjects ? { subject_thresholds: parts.subjects } : {}),
      },
    }),
  // Durum kuralları (servis çalışmıyor, RAID bozuk …): her kural için üst kapsamlardan gelen ve sunucunun kendi ayarı.
  statusRules: (id: string) => apiRequest<HostStatusRuleView[]>(`/api/v1/hosts/${id}/status-rules`),
  setStatusRules: (id: string, rules: StatusRuleChanges) =>
    apiRequest<HostStatusRuleView[]>(`/api/v1/hosts/${id}/status-rules`, { method: 'PUT', body: { rules } }),
  // systemd servisleri (protokol 4; eski agent'ta liste boş) ve izlenen servis seçimi.
  services: (id: string) => apiRequest<HostServices>(`/api/v1/hosts/${id}/services`),
  // Seçimin tamamını değiştirir; şu an raporlanmayan bir servis de seçilebilir.
  setWatchedServices: (id: string, services: string[]) =>
    apiRequest<HostServices>(`/api/v1/hosts/${id}/watched-services`, { method: 'PUT', body: { services } }),
  diskAlerts: (id: string) => apiRequest<DiskAlertSettings>(`/api/v1/hosts/${id}/disk-alerts`),
  setDiskAlerts: (id: string, selection: { all_mounts_alert: boolean; custom_alert_mounts: string[] }) =>
    apiRequest<DiskAlertSettings>(`/api/v1/hosts/${id}/disk-alerts`, { method: 'PUT', body: selection }),
  // Bir organizasyonun TÜM host'ları (sayfalanmamış) — çok seçimli atama listeleri gibi,
  // gerçekten hepsine ihtiyaç duyan yerler için (bkz. HostAssignment.tsx).
  listByOrganization: (organizationId: string) =>
    apiRequest<Host[]>(`/api/v1/organizations/${organizationId}/hosts`),
  // Aynı uç nokta, sayfalanmış/aranabilir hali — OrganizationHostsPage'in tablosu için.
  searchByOrganization: (organizationId: string, params: { q: string; limit: number; offset: number }) =>
    apiRequestPage<Host>(`/api/v1/organizations/${organizationId}/hosts`, {
      query: { q: params.q || undefined, limit: String(params.limit), offset: String(params.offset) },
    }),
  get: (id: string) => apiRequest<Host>(`/api/v1/hosts/${id}`),
  create: (input: CreateHostInput) => apiRequest<Host>('/api/v1/hosts', { method: 'POST', body: input }),
  update: (id: string, patch: Partial<Pick<Host, 'title' | 'ip' | 'interval_seconds'>>) =>
    apiRequest<Host>(`/api/v1/hosts/${id}`, { method: 'PUT', body: patch }),
  remove: (id: string) => apiRequest<void>(`/api/v1/hosts/${id}`, { method: 'DELETE' }),
  rotateCredentials: (id: string) => apiRequest<Host>(`/api/v1/hosts/${id}/rotate-credentials`, { method: 'POST' }),
  // Geçmiş grafiği ("Detay"): aralıktaki ham satırların tamamı (ortalanmaz/kovalanmaz).
  metrics: (id: string, from?: string, to?: string) =>
    apiRequest<MetricPoint[]>(`/api/v1/hosts/${id}/metrics`, { query: { from, to } }),
  // "Genel" sekmesindeki anlık kartlar: yalnızca en son ham örnek. Sunucu hiç rapor vermediyse null (204).
  latestMetric: async (id: string): Promise<MetricPoint | null> =>
    (await apiRequest<MetricPoint | undefined>(`/api/v1/hosts/${id}/metrics/latest`)) ?? null,
  docker: (id: string) => apiRequest<DockerContainerReport[]>(`/api/v1/hosts/${id}/docker`),
}

export const alertsApi = {
  list: (params: { status?: AlertStatus; level?: AlertLevel; hostId?: string; q: string; limit: number; offset: number }) =>
    apiRequestPage<Alert>('/api/v1/alerts', {
      query: {
        status: params.status,
        level: params.level,
        host_id: params.hostId,
        q: params.q || undefined,
        limit: String(params.limit),
        offset: String(params.offset),
      },
    }),
  acknowledge: (id: string) => apiRequest<Alert>(`/api/v1/alerts/${id}/acknowledge`, { method: 'POST' }),
  notifications: (id: string) => apiRequest<AlertNotification[]>(`/api/v1/alerts/${id}/notifications`),
}

export const auditApi = {
  list: (params: { action?: string; cursor?: string; limit?: number }) =>
    apiRequest<AuditLogPage>('/api/v1/audit-logs', {
      query: { action: params.action, cursor: params.cursor, limit: params.limit?.toString() },
    }),
}

// Sistem Araçları (salt okunur; her araç kendi system.* izniyle).
export const systemApi = {
  queue: () => apiRequest<QueueSummary>('/api/v1/system/queue'),
  cache: () => apiRequest<CacheStatus>('/api/v1/system/cache'),
  logFiles: () => apiRequest<LogFiles>('/api/v1/system/logs'),
  logEntries: (params: { day: string; level?: string; q?: string; request_id?: string; from?: string; to?: string; before?: number; limit?: number }) =>
    apiRequest<LogPage>('/api/v1/system/logs/entries', {
      query: {
        day: params.day,
        level: params.level,
        q: params.q,
        request_id: params.request_id,
        from: params.from,
        to: params.to,
        before: params.before?.toString(),
        limit: params.limit?.toString(),
      },
    }),
  downloadLog: (day: string) => apiDownload('/api/v1/system/logs/download', { day }, `healthbeat-server-${day}.log`),
  queueItems: (params: { status?: string; kind?: string; cursor?: string; limit?: number }) =>
    apiRequest<QueueItemPage>('/api/v1/system/queue/items', {
      query: { status: params.status, kind: params.kind, cursor: params.cursor, limit: params.limit?.toString() },
    }),
}

export interface CreateThresholdInput {
  // Verilmezse global varsayılan (yalnızca super_admin).
  organization_id?: string
  metric_type: MetricType
  warning_level: number
  critical_level: number
  // Yalnızca protokol 4 türlerinde; verilmezse hemen.
  duration_seconds?: number
}

// duration_seconds: verilmezse değişmez, null süreyi kaldırır (hemen).
export interface ThresholdPatch {
  warning_level?: number
  critical_level?: number
  duration_seconds?: number | null
}

export const thresholdsApi = {
  list: () => apiRequest<ThresholdConfig[]>('/api/v1/thresholds'),
  create: (input: CreateThresholdInput) => apiRequest<ThresholdConfig>('/api/v1/thresholds', { method: 'POST', body: input }),
  update: (id: string, patch: ThresholdPatch) => apiRequest<ThresholdConfig>(`/api/v1/thresholds/${id}`, { method: 'PUT', body: patch }),
  remove: (id: string) => apiRequest<void>(`/api/v1/thresholds/${id}`, { method: 'DELETE' }),
}

// Durum kurallarının genel ve organizasyon kapsamı (sunucununki hostsApi.statusRules'ta). Liste genel kuralları ve
// çağıranın görebildiği organizasyonlarınkini (üst zincir dahil) döndürür; tanımlanmamış kural kapalıdır.
export const statusRulesApi = {
  list: () => apiRequest<StatusRuleConfig[]>('/api/v1/status-rules'),
  // organizationId null = genel kurallar (yalnızca super_admin). Yanıt yalnızca o kapsamın satırlarıdır.
  set: (organizationId: string | null, rules: StatusRuleChanges) =>
    apiRequest<StatusRuleConfig[]>('/api/v1/status-rules', { method: 'PUT', body: { organization_id: organizationId, rules } }),
}

export const usersApi = {
  list: (params: { q: string; limit: number; offset: number }) =>
    apiRequestPage<User>('/api/v1/users', {
      query: { q: params.q || undefined, limit: String(params.limit), offset: String(params.offset) },
    }),
  create: (input: { email: string; password: string; role: Role; full_name?: string; phone?: string }) =>
    apiRequest<User>('/api/v1/users', { method: 'POST', body: input }),
  // Boş metin alanı temizler; verilmeyen alan değişmez.
  update: (id: string, patch: { full_name?: string; phone?: string; role?: Role; email?: string }) =>
    apiRequest<User>(`/api/v1/users/${id}`, { method: 'PUT', body: patch }),
  remove: (id: string) => apiRequest<void>(`/api/v1/users/${id}`, { method: 'DELETE' }),
  organizations: (id: string) => apiRequest<Organization[]>(`/api/v1/users/${id}/organizations`),
  setOrganizations: (id: string, organizationIds: string[]) =>
    apiRequest<void>(`/api/v1/users/${id}/organizations`, { method: 'PUT', body: { organization_ids: organizationIds } }),
  hosts: (id: string) => apiRequest<{ host_ids: string[] }>(`/api/v1/users/${id}/hosts`),
  setHosts: (id: string, hostIds: string[]) =>
    apiRequest<void>(`/api/v1/users/${id}/hosts`, { method: 'PUT', body: { host_ids: hostIds } }),
  addHostsByOrganization: (id: string, organizationId: string) =>
    apiRequest<{ hosts_added: number }>(`/api/v1/users/${id}/hosts/by-organization`, {
      method: 'POST',
      body: { organization_id: organizationId },
    }),
}

// Sistem ayarları (settings.view okur, settings.manage değiştirir).
export const settingsApi = {
  get: () => apiRequest<SettingsResponse>('/api/v1/settings'),
  update: (patch: Partial<SettingsValues>) => apiRequest<SettingsResponse>('/api/v1/settings', { method: 'PATCH', body: patch }),
  reset: (fields: SettingsField[]) => apiRequest<SettingsResponse>('/api/v1/settings/reset', { method: 'POST', body: { fields } }),
}

export interface ChannelPatch {
  enabled?: boolean
  config?: Partial<SMTPConfig>
  // "" şifreyi siler; verilmezse değişmez.
  secret?: string
  owner_min_level?: AlertLevel
}

export const channelsApi = {
  list: () => apiRequest<ChannelInfo[]>('/api/v1/notification-channels'),
  // Kural ekranı için (notification.view): hangi kanal seçilebilir.
  options: () => apiRequest<ChannelOption[]>('/api/v1/notification-channels/options'),
  update: (channel: string, patch: ChannelPatch) =>
    apiRequest<ChannelInfo>(`/api/v1/notification-channels/${channel}`, { method: 'PATCH', body: patch }),
  test: (channel: string, to: string) => apiRequest<void>(`/api/v1/notification-channels/${channel}/test`, { method: 'POST', body: { to } }),
}

export interface OwnerInput {
  name: string
  email: string | null
  phone: string | null
  email_enabled: boolean
  sms_enabled: boolean
}

export const ownersApi = {
  list: () => apiRequest<NotificationOwner[]>('/api/v1/notification-owners'),
  create: (input: OwnerInput) => apiRequest<NotificationOwner>('/api/v1/notification-owners', { method: 'POST', body: input }),
  update: (id: string, input: OwnerInput) => apiRequest<NotificationOwner>(`/api/v1/notification-owners/${id}`, { method: 'PUT', body: input }),
  remove: (id: string) => apiRequest<void>(`/api/v1/notification-owners/${id}`, { method: 'DELETE' }),
}
