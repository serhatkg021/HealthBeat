package store

import (
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// collect, rows'un her satırını scan ile okur ve rows'u kapatır. Satır yoksa boş (nil olmayan) bir dilim döner; JSON'da
// null değil [] olur.
func collect[T any](rows pgx.Rows, scan func(row interface{ Scan(...any) error }) (T, error)) ([]T, error) {
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (T, error) { return scan(row) })
}

// collectIDs, tek kolonlu bir kimlik sorgusunun satırlarıdır (collect gibi boşken []).
func collectIDs(rows pgx.Rows) ([]uuid.UUID, error) {
	return pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
}
