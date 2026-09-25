package httpapi_test

import (
	"flag"
	"io"
	"log/slog"
	"os"
	"testing"
)

// TestMain, istek loglarını (hata alan isteklerde gövdeleriyle) yalnızca -v ile gösterir; aksi halde test çıktısı
// beklenen 4xx'lerin satırlarıyla dolar. Logu doğrulayan testler kendi logger'ını kurar (bkz. logRecords).
func TestMain(m *testing.M) {
	flag.Parse()
	if !testing.Verbose() {
		slog.SetDefault(slog.New(slog.NewTextHandler(io.Discard, nil)))
	}
	os.Exit(m.Run())
}
