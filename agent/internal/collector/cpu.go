package collector

import (
	"bufio"
	"fmt"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

type cpuSample struct {
	idle   uint64
	total  uint64
	iowait uint64
	steal  uint64
	// hasIOWait/hasSteal: çok eski çekirdeklerde bu sütunlar yoktur; yokluğu "0" sayılmaz.
	hasIOWait, hasSteal bool
}

// CPUBreakdown, CPU zamanının kullanım dışındaki dağılımıdır; nil = bilinmiyor.
type CPUBreakdown struct {
	IOWaitPct    *float64 // diske bekleyen CPU
	StealPct     *float64 // sanal makinede hipervizörün başkasına verdiği CPU
	ProcsBlocked *int     // G/Ç'de takılı (D durumunda) süreç sayısı; anlık
}

// CPUCollector, CPU kullanımını iki /proc/stat örneği arasındaki fark olarak raporlar.
// İlk çağrıdan sonraki her çağrı, bir önceki çağrının örneğini başlangıç değeri alır;
// böylece kullanım push'lar arasındaki gerçek aralığı yansıtır. İlk çağrının başlangıç
// değeri henüz yoktur, bu yüzden 200 ms sonra hızlı bir ek örnek alır. Eşzamanlı çağrılabilir
// (pull modunda istekler paralel gelir).
type CPUCollector struct {
	mu        sync.Mutex
	prev      *cpuSample
	breakdown CPUBreakdown
}

func NewCPUCollector() *CPUCollector {
	return &CPUCollector{}
}

func (c *CPUCollector) Sample() (float64, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	cur, blocked, err := readProcStat()
	if err != nil {
		return 0, err
	}

	if c.prev == nil {
		first := cur // ayrı kopya: cur aşağıda ikinci örnekle değişir
		c.prev = &first
		time.Sleep(200 * time.Millisecond)
		cur2, blocked2, err := readProcStat()
		if err != nil {
			return 0, err
		}
		cur, blocked = cur2, blocked2
	}

	pct := percentFromDelta(*c.prev, cur)
	c.breakdown = breakdownFromDelta(*c.prev, cur)
	c.breakdown.ProcsBlocked = blocked
	c.prev = &cur
	return pct, nil
}

// Breakdown, son Sample'ın aralığındaki CPU dağılımıdır (Sample'dan sonra çağrılır).
func (c *CPUCollector) Breakdown() CPUBreakdown {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.breakdown
}

// breakdownFromDelta, iowait ve steal'in aralıktaki payını verir; sütun yoksa ya da sayaçlar geri gittiyse bilinmiyor.
func breakdownFromDelta(prev, cur cpuSample) CPUBreakdown {
	var out CPUBreakdown
	if cur.total <= prev.total {
		return out
	}
	totalDelta := float64(cur.total - prev.total)
	share := func(p, c uint64, ok bool) *float64 {
		if !ok || c < p {
			return nil
		}
		v := min(float64(c-p)/totalDelta*100, 100)
		return &v
	}
	out.IOWaitPct = share(prev.iowait, cur.iowait, cur.hasIOWait && prev.hasIOWait)
	out.StealPct = share(prev.steal, cur.steal, cur.hasSteal && prev.hasSteal)
	return out
}

// CPUCores, mantıksal çekirdek sayısını döndürür (bu sürecin çalışabildiği CPU'lar).
func CPUCores() int {
	return runtime.NumCPU()
}

// readProcStat, toplam CPU satırını ve G/Ç'de takılı süreç sayısını (procs_blocked; yoksa nil) okur.
func readProcStat() (cpuSample, *int, error) {
	f, err := os.Open("/proc/stat")
	if err != nil {
		return cpuSample{}, nil, err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	if !scanner.Scan() {
		return cpuSample{}, nil, fmt.Errorf("empty /proc/stat")
	}
	sample, err := parseProcStatLine(scanner.Text())
	if err != nil {
		return cpuSample{}, nil, err
	}
	var blocked *int
	for scanner.Scan() {
		if v, ok := strings.CutPrefix(scanner.Text(), "procs_blocked "); ok {
			if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil && n >= 0 {
				blocked = &n
			}
			break
		}
	}
	return sample, blocked, nil
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
	sample := cpuSample{idle: values[3], total: total}
	if len(values) > 4 {
		sample.idle += values[4]
		sample.iowait, sample.hasIOWait = values[4], true
	}
	if len(values) > 7 {
		sample.steal, sample.hasSteal = values[7], true
	}
	return sample, nil
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
