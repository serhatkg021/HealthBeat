package httpapi

// ResetPermissionCache, role_permissions'ı çalışırken değiştiren testlerin izin önbelleğini boşaltmasını sağlar
// (üretimde değişiklik en geç rbac.DefaultCacheTTL sonra etkili olur).
func (d *Deps) ResetPermissionCache() { d.perms.Invalidate() }
