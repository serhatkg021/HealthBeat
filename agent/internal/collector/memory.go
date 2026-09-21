package collector

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
)

// SampleMemory, RAM kullanımını yüzde olarak döndürür; naif MemFree yerine /proc/meminfo'nun
// MemAvailable değerini (geri kazanılabilir önbellek/tamponları zaten hesaba katar) kullanır.
func SampleMemory() (float64, error) {
	f, err := os.Open("/proc/meminfo")
	if err != nil {
		return 0, err
	}
	defer f.Close()
	return memoryUsedPct(f)
}

// TotalMemoryMB, makinenin toplam RAM'ini MB olarak döndürür (/proc/meminfo MemTotal).
// Okunamazsa 0 ("bilinmiyor") döner; server bu durumda son bilinen değeri korur.
func TotalMemoryMB() int64 {
	f, err := os.Open("/proc/meminfo")
	if err != nil {
		return 0
	}
	defer f.Close()
	return totalMemoryMB(f)
}

// totalMemoryMB, /proc/meminfo biçimindeki girdiden MemTotal'ı MB'a çevirir.
func totalMemoryMB(r io.Reader) int64 {
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		if line := scanner.Text(); strings.HasPrefix(line, "MemTotal:") {
			return int64(parseMeminfoKB(line) / 1024)
		}
	}
	return 0
}

// memoryUsedPct, /proc/meminfo biçimindeki girdiden kullanımı hesaplar.
func memoryUsedPct(r io.Reader) (float64, error) {
	var total, available, free, buffers, cached uint64
	haveAvailable := false
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		line := scanner.Text()
		switch {
		case strings.HasPrefix(line, "MemTotal:"):
			total = parseMeminfoKB(line)
		case strings.HasPrefix(line, "MemAvailable:"):
			available, haveAvailable = parseMeminfoKB(line), true
		case strings.HasPrefix(line, "MemFree:"):
			free = parseMeminfoKB(line)
		case strings.HasPrefix(line, "Buffers:"):
			buffers = parseMeminfoKB(line)
		case strings.HasPrefix(line, "Cached:"):
			cached = parseMeminfoKB(line)
		}
	}
	if err := scanner.Err(); err != nil {
		return 0, err
	}
	if total == 0 {
		return 0, fmt.Errorf("could not read MemTotal from /proc/meminfo")
	}

	// 3.14 öncesi çekirdeklerde MemAvailable yoktur; "yok"u "0 kullanılabilir" (= %100 kullanım,
	// sahte alert) saymak yerine yaklaşık hesaplanır.
	if !haveAvailable {
		available = free + buffers + cached
	}
	if available >= total { // aşağıdaki işaretsiz çıkarmayı da korur
		return 0, nil
	}

	used := float64(total-available) / float64(total) * 100
	if used > 100 {
		return 100, nil
	}
	return used, nil
}

func parseMeminfoKB(line string) uint64 {
	fields := strings.Fields(line)
	if len(fields) < 2 {
		return 0
	}
	v, _ := strconv.ParseUint(fields[1], 10, 64)
	return v
}
