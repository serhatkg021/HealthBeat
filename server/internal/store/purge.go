package store

import (
	"context"
	"time"
)

// purgeInBatches, query'yi ($1 = cutoff, $2 = parti boyutu) parti parti çalıştırır: her DELETE en fazla batchSize satır
// siler, böylece büyük bir temizlik tabloyu uzun süre kilitlemez. Silinen toplam satır sayısını döndürür.
func purgeInBatches(ctx context.Context, db DB, query string, cutoff time.Time, batchSize int) (int64, error) {
	if batchSize < 1 {
		batchSize = 1
	}
	var total int64
	for {
		tag, err := db.Exec(ctx, query, cutoff, batchSize)
		if err != nil {
			return total, err
		}
		total += tag.RowsAffected()
		if tag.RowsAffected() < int64(batchSize) {
			return total, nil
		}
		if err := ctx.Err(); err != nil {
			return total, err
		}
	}
}
