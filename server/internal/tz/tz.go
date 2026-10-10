// Package tz, kurulumun saat dilimini çözer ve zamanları saat dilimi adı ve UTC ofsetiyle yazar. Saat dilimi verisi
// binary'ye gömülüdür (time/tzdata): imaj dışında çalıştırılan binary de IANA adlarını tanır. Ofset her an için
// o andaki kurala göre hesaplanır (yaz saati uygulanan bölgede yazın ve kışın farklıdır).
package tz

import (
	"fmt"
	"os"
	"strings"
	"sync"
	"time"
	_ "time/tzdata"
)

// Valid, name'in yüklenebilen bir IANA saat dilimi adı olup olmadığını söyler. "Local" (sürecin yerel saati) ad
// değildir: kurulumun saatini makineye bağlamamak için kabul edilmez.
func Valid(name string) bool {
	if name == "" || name == "Local" {
		return false
	}
	_, err := load(name)
	return err == nil
}

var (
	mu    sync.Mutex
	cache = map[string]*time.Location{}
)

func load(name string) (*time.Location, error) {
	mu.Lock()
	defer mu.Unlock()
	if loc, ok := cache[name]; ok {
		return loc, nil
	}
	loc, err := time.LoadLocation(name)
	if err != nil {
		return nil, err
	}
	cache[name] = loc
	return loc, nil
}

// Resolve, kurulumun saat dilimidir: ayar (setting) geçerliyse o, değilse server sürecinin TZ ortam değişkeni
// (geçerliyse), o da yoksa UTC.
func Resolve(setting string) *time.Location {
	for _, name := range []string{setting, strings.TrimPrefix(os.Getenv("TZ"), ":")} {
		if Valid(name) {
			loc, _ := load(name)
			return loc
		}
	}
	return time.UTC
}

// FormatOffset, saniye cinsinden UTC ofsetini yazar: "UTC+3", "UTC+5:30", "UTC−4"; sıfır ofset "UTC".
func FormatOffset(seconds int) string {
	if seconds == 0 {
		return "UTC"
	}
	sign := "+"
	if seconds < 0 {
		sign, seconds = "−", -seconds
	}
	h, m := seconds/3600, seconds%3600/60
	if m == 0 {
		return fmt.Sprintf("UTC%s%d", sign, h)
	}
	return fmt.Sprintf("UTC%s%d:%02d", sign, h, m)
}

// Offset, t anında loc'un UTC ofsetidir ("UTC+3").
func Offset(t time.Time, loc *time.Location) string {
	_, off := t.In(loc).Zone()
	return FormatOffset(off)
}

// Label, t anında loc'u adı ve ofsetiyle yazar: "Europe/Istanbul, UTC+3". UTC'nin kendisi yalnızca "UTC".
func Label(t time.Time, loc *time.Location) string {
	if loc == time.UTC || loc.String() == "UTC" {
		return "UTC"
	}
	return loc.String() + ", " + Offset(t, loc)
}
