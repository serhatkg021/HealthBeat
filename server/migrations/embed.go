// Package migrations, SQL migration dosyalarını server binary'sine gömer; böylece tek bir
// çalıştırılabilir kendi şemasını taşır (nasıl uygulandıkları için bkz. internal/migrate).
//
// Dosya adlandırma: NNNNNN_aciklama.up.sql ve NNNNNN_aciklama.down.sql, sürümler boşluksuz
// artar. .down dosyaları dokümantasyon/geri alma yardımcılarıdır ve asla otomatik çalıştırılmaz.
package migrations

import "embed"

//go:embed *.sql
var FS embed.FS
