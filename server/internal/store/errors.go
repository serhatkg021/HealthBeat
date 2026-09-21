package store

import (
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

var (
	ErrNotFound = errors.New("bulunamadı")
	ErrConflict = errors.New("çakışma")
)

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
