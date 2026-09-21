// Package rbac izin denetimlerini role_permissions tablosunu sorgulayarak uygular — uygulama
// kodunda asla "if role == ..." sabit kodlanmaz (bkz. docs/MIMARI.md bölüm 3 ve 4'ün
// role_permissions şemasının üstündeki notu).
package rbac

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
)

func HasPermission(ctx context.Context, pool *pgxpool.Pool, role, permissionKey string) (bool, error) {
	var exists bool
	err := pool.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM role_permissions WHERE role = $1 AND permission_key = $2)`,
		role, permissionKey,
	).Scan(&exists)
	if err != nil {
		return false, err
	}
	return exists, nil
}
