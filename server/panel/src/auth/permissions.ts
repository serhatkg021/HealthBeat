import type { User } from '../types/api'

// Server'ın izin anahtarları (role_permissions, bkz. docs/MIMARI.md bölüm 4). Panel bir menüyü ya da düğmeyi rol adına
// değil bu izinlere göre gösterir; izinler GET /api/v1/me'den gelir, böylece rol ↔ izin tablosu yalnızca server'da durur.
export type Permission =
  | 'organization.create'
  | 'organization.view'
  | 'organization.update'
  | 'organization.delete'
  | 'host.create'
  | 'host.view'
  | 'host.update'
  | 'host.delete'
  | 'threshold.view'
  | 'threshold.edit'
  | 'alert.view'
  | 'alert.acknowledge'
  | 'dashboard.view'
  | 'user.create'
  | 'user.view'
  | 'user.update'
  | 'user.delete'
  | 'audit.view'
  | 'contact.view'
  | 'contact.edit'
  | 'notification.view'
  | 'notification.edit'
  | 'settings.view'
  | 'settings.manage'
  | 'system.queue.view'
  | 'system.cache.view'
  | 'system.logs.view'

// hasPermission, kullanıcının izni olup olmadığıdır. İzinler henüz yüklenmediyse (eski oturumdan kalan kullanıcı, /me
// yanıtı gelmeden) hiçbir izin yok sayılır: düğme bir an geç görünür ama hiçbir zaman yetkisiz birine görünmez.
export function hasPermission(user: Pick<User, 'permissions'> | null | undefined, permission: Permission): boolean {
  return !!user?.permissions?.includes(permission)
}
