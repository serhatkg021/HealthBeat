package store

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// DB, bir bağlantı havuzunun ya da açık bir transaction'ın sorgu yüzüdür; iki tip de karşılar. Bir işlemi başka bir
// yazmayla aynı transaction'da yapması gereken store'lar (Alerts, Outbox) bunu tutar (bkz. WithTx).
type DB interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// InTx, fn'i tek bir transaction'da çalıştırır: fn hata döndürürse (ya da panic'lerse) geri alınır, yoksa commit edilir.
func InTx(ctx context.Context, pool *pgxpool.Pool, fn func(tx pgx.Tx) error) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }() // commit'ten sonra etkisizdir
	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
