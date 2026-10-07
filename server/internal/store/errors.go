package store

import (
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// ErrNotFound ve ErrConflict genel hata sınıflarıdır; handler'lar durum kodunu errors.Is ile bunlardan seçer.
var (
	ErrNotFound = errors.New("not found")
	ErrConflict = errors.New("conflict")
)

// Adlandırılmış iş kuralı hataları: her biri ErrNotFound ya da ErrConflict'i sarar ve istemcinin düzeltebileceği bir
// durumu anlatır. İstemciye giden Türkçe metinleri httpapi verir (errors.Is ile tanır; bir neden ek bağlamla sarılabilir).
var (
	ErrParentOrganizationMissing = reason(ErrNotFound, "parent organization does not exist")
	ErrOrganizationMissing       = reason(ErrNotFound, "organization does not exist")
	ErrOrganizationNameTaken     = reason(ErrConflict, "organization name already used under this parent")
	ErrOrganizationCycle         = reason(ErrConflict, "organization cannot move under its own subtree")
	ErrOrganizationNotEmpty      = reason(ErrConflict, "organization still has child organizations or hosts")

	ErrHostMissing      = reason(ErrNotFound, "host does not exist")
	ErrHostTitleTaken   = reason(ErrConflict, "host title already used in this organization")
	ErrHostModeMismatch = reason(ErrConflict, "host fields do not match the selected mode")

	ErrEmailTaken         = reason(ErrConflict, "email already in use")
	ErrTwoFactorNoChannel = reason(ErrConflict, "two-factor authentication requires a channel")
	// ErrLastSuperAdmin, bir değişiklik sistemi hiç super_admin'siz bırakacağında döndürülür — o zaman kimse
	// kullanıcıları ya da rolleri yeniden yönetemezdi (bootstrap admin yalnızca bir migration olarak vardır).
	ErrLastSuperAdmin = reason(ErrConflict, "cannot demote or delete the last super_admin")

	ErrThresholdExists        = reason(ErrConflict, "threshold already exists for this scope and metric")
	ErrThresholdLevelsInvalid = reason(ErrConflict, "warning level exceeds critical level")
	ErrHostThresholdInvalid   = reason(ErrConflict, "invalid host threshold")
	ErrStatusRuleInvalid      = reason(ErrConflict, "invalid status alert rule")

	ErrRouteExists        = reason(ErrConflict, "route already exists for this recipient and channel in this scope")
	ErrRouteTargetMissing = reason(ErrNotFound, "route organization, host, user or contact does not exist")
	ErrRouteShape         = reason(ErrConflict, "route needs exactly one scope and one recipient")

	ErrContactManagerInvalid = reason(ErrConflict, "manager must be a contact of the same organization")
	ErrContactInvalid        = reason(ErrConflict, "contact needs a phone or email and cannot manage itself")
)

func reason(class error, text string) error { return fmt.Errorf("%w: %s", class, text) }

// pgErrorCode, varsa err'den bir PostgreSQL SQLSTATE kodunu çıkarır.
func pgErrorCode(err error) string {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code
	}
	return ""
}

func isNoRows(err error) bool {
	return errors.Is(err, pgx.ErrNoRows)
}

const (
	pgUniqueViolation     = "23505"
	pgForeignKeyViolation = "23503"
	pgCheckViolation      = "23514"
)
