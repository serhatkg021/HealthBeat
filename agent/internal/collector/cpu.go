package collector

import (
	"bufio"
	"fmt"
	"os"
	"runtime"
	"strconv"
	"strings"
	"time"
)

type cpuSample struct {
	idle  uint64
	total uint64
}

// CPUCollector, CPU kullanımını iki /proc/stat örneği arasındaki fark olarak raporlar.
// İlk çağrıdan sonraki her çağrı, bir önceki çağrının örneğini başlangıç değeri alır;
// böylece kullanım push'lar arasındaki gerçek aralığı yansıtır. İlk çağrının başlangıç
// değeri henüz yoktur, bu yüzden 200 ms sonra hızlı bir ek örnek alır.
type CPUCollector struct {
	prev *cpuSample
}

func NewCPUCollector() *CPUCollector {
	return &CPUCollector{}
}

func (c *CPUCollector) Sample() (float64, error) {
	cur, err := readProcStat()
	if err != nil {
		return 0, err
	}

	if c.prev == nil {
		c.prev = &cur
		time.Sleep(200 * time.Millisecond)
		cur2, err := readProcStat()
		if err != nil {
			return 0, err
		}
		pct := percentFromDelta(*c.prev, cur2)
		c.prev = &cur2
		return pct, nil
	}

	pct := percentFromDelta(*c.prev, cur)
	c.prev = &cur
	return pct, nil
}

// CPUCores, mantıksal çekirdek sayısını döndürür (bu sürecin çalışabildiği CPU'lar).
func CPUCores() int {
	return runtime.NumCPU()
}

func readProcStat() (cpuSample, error) {
	f, err := os.Open("/proc/stat")
	if err != nil {
		return cpuSample{}, err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	if !scanner.Scan() {
		return cpuSample{}, fmt.Errorf("empty /proc/stat")
	}

	return parseProcStatLine(scanner.Text())
}

// parseProcStatLine, /proc/stat'ın toplam "cpu ..." satırını ayrıştırır.
func parseProcStatLine(line string) (cpuSample, error) {
	fields := strings.Fields(line)
	if len(fields) < 5 || fields[0] != "cpu" {
		return cpuSample{}, fmt.Errorf("unexpected /proc/stat format: %q", line)
	}

	values := make([]uint64, 0, len(fields)-1)
	for _, tok := range fields[1:] {
		v, err := strconv.ParseUint(tok, 10, 64)
		if err != nil {
			return cpuSample{}, fmt.Errorf("parse /proc/stat field %q: %w", tok, err)
		}
		values = append(values, v)
	}

	var total uint64
	for _, v := range values {
		total += v
	}
	// alanlar: user nice system idle iowait irq softirq steal guest guest_nice
	// boşta süre (kullanım hesabı için) idle + iowait'tir. Çok eski çekirdeklerde iowait
	// yoktur; bu yüzden aralık dışı bir indeks yerine isteğe bağlı okunur.
	idle := values[3]
	if len(values) > 4 {
		idle += values[4]
	}

	return cpuSample{idle: idle, total: total}, nil
}

func percentFromDelta(prev, cur cpuSample) float64 {
	// Geriye giden sayaçlar (sıfırlama) devasa bir işaretsiz farka dönüşürdü; o aralık
	// için raporlanacak anlamlı bir değer yok.
	if cur.total < prev.total || cur.idle < prev.idle {
		return 0
	}
	totalDelta := float64(cur.total - prev.total)
	if totalDelta <= 0 {
		return 0
	}
	idleDelta := float64(cur.idle - prev.idle)
	usage := (totalDelta - idleDelta) / totalDelta * 100
	switch {
	case usage < 0:
		return 0
	case usage > 100:
		return 100
	default:
		return usage
	}
}
