package collector

import (
	"context"
	"errors"
	"os"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	// topProcesses, CPU'ya ve RAM'e göre raporlanan süreç grubu sayısıdır.
	topProcesses = 5
	// clockTicks, /proc/<pid>/stat'taki CPU zamanının birimidir (USER_HZ). Linux'ta kullanıcı alanına her mimaride 100
	// olarak verilir; çekirdeğin iç zamanlayıcı frekansından bağımsızdır.
	clockTicks = 100
)

// ProcessGroup, aynı adlı süreçlerin toplamıdır (ör. postgres ×23).
type ProcessGroup struct {
	Name   string
	Count  int
	CPUPct float64 // makinenin toplam CPU kapasitesine göre (cpu_usage_pct ile aynı ölçek)
	RSSMB  float64 // grubun yerleşik belleklerinin toplamı (paylaşılan sayfalar her süreçte ayrı sayılır)
}

// ProcessSummary, süreç özetidir. TopCPU yalnızca önceki bir toplamayla karşılaştırılabildiğinde doludur.
type ProcessSummary struct {
	Total, Zombie  int
	TopCPU, TopRAM []ProcessGroup
}

type procKey struct {
	pid   int
	start uint64 // süreç başlangıç zamanı: aynı numarayı yeniden alan başka bir süreç karışmasın
}

// ProcessCollector, süreçleri /proc/<pid>/stat'tan okur. Yalnızca süreç adı (comm) alınır: komut satırı ve kullanıcı
// okunmaz ve gönderilmez (komut satırında parola gibi sırlar olabilir). Çekirdek iş parçacıkları (kworker gibi) toplam
// sayıya girer, gruplara girmez.
type ProcessCollector struct {
	root string           // sahte kök (test); "" = gerçek /
	now  func() time.Time // test için
	cpus func() int

	mu     sync.Mutex
	prev   map[procKey]uint64 // CPU zamanı (tick)
	prevAt time.Time
}

func NewProcessCollector() *ProcessCollector {
	return &ProcessCollector{now: time.Now, cpus: runtime.NumCPU}
}

type procStat struct {
	key    procKey
	name   string
	state  byte
	kernel bool
	ticks  uint64
	rssMB  float64
}

// Collect, süreç özetini döndürür. /proc okunamıyorsa hata; başka süreçleri göremiyorsa (/proc hidepid ile bağlı)
// eksik bir özet yanıltıcı olacağı için yine hata döner.
func (c *ProcessCollector) Collect(ctx context.Context) (ProcessSummary, error) {
	entries, err := os.ReadDir(rootPath(c.root, "/proc"))
	if err != nil {
		return ProcessSummary{}, err
	}
	pageMB := float64(os.Getpagesize()) / (1024 * 1024)
	var procs []procStat
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}
		if ctx.Err() != nil {
			return ProcessSummary{}, ctx.Err()
		}
		b, err := os.ReadFile(rootPath(c.root, "/proc/"+e.Name()+"/stat"))
		if err != nil {
			continue // süreç bu arada bitti
		}
		if p, ok := parsePIDStat(pid, string(b), pageMB); ok {
			procs = append(procs, p)
		}
	}
	if len(procs) == 0 {
		return ProcessSummary{}, errors.New("no processes readable in /proc")
	}
	if !seesOtherUsers(procs) {
		return ProcessSummary{}, errors.New("/proc hides other users' processes (hidepid)")
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.now()
	secs := now.Sub(c.prevAt).Seconds()
	haveCPU := c.prev != nil && secs > 0
	capacity := secs * clockTicks * float64(max(c.cpus(), 1)) // aralıkta makinenin toplam CPU zamanı (tick)

	summary := ProcessSummary{Total: len(procs)}
	groups := map[string]*ProcessGroup{}
	next := make(map[procKey]uint64, len(procs))
	for _, p := range procs {
		next[p.key] = p.ticks
		if p.state == 'Z' {
			summary.Zombie++
		}
		if p.kernel {
			continue
		}
		g := groups[p.name]
		if g == nil {
			g = &ProcessGroup{Name: p.name}
			groups[p.name] = g
		}
		g.Count++
		g.RSSMB += p.rssMB
		if prev, ok := c.prev[p.key]; ok && haveCPU && p.ticks >= prev {
			g.CPUPct += float64(p.ticks-prev) / capacity * 100
		}
	}
	c.prev, c.prevAt = next, now

	all := make([]ProcessGroup, 0, len(groups))
	for _, g := range groups {
		all = append(all, *g)
	}
	summary.TopRAM = topBy(all, func(g ProcessGroup) float64 { return g.RSSMB })
	if haveCPU {
		summary.TopCPU = topBy(all, func(g ProcessGroup) float64 { return g.CPUPct })
	}
	return summary, nil
}

// topBy, değere göre en büyük topProcesses grubu döndürür (eşitlikte ada göre, kararlı çıktı için).
func topBy(groups []ProcessGroup, value func(ProcessGroup) float64) []ProcessGroup {
	sorted := append([]ProcessGroup(nil), groups...)
	sort.Slice(sorted, func(i, j int) bool {
		if vi, vj := value(sorted[i]), value(sorted[j]); vi != vj {
			return vi > vj
		}
		return sorted[i].Name < sorted[j].Name
	})
	return sorted[:min(topProcesses, len(sorted))]
}

// seesOtherUsers: hidepid ile bağlı /proc'ta yalnızca kendi süreçleri görünür; PID 1 (init) her zaman başka bir
// kullanıcınındır ve görünmüyorsa liste eksiktir.
func seesOtherUsers(procs []procStat) bool {
	for _, p := range procs {
		if p.key.pid == 1 {
			return true
		}
	}
	return false
}

// parsePIDStat, /proc/<pid>/stat satırını okur. Süreç adı parantez içindedir ve boşluk ya da parantez içerebilir; bu
// yüzden ad son ")"'e kadar alınır.
func parsePIDStat(pid int, text string, pageMB float64) (procStat, bool) {
	nameStart, nameEnd := strings.IndexByte(text, '('), strings.LastIndexByte(text, ')')
	if nameStart < 0 || nameEnd < nameStart {
		return procStat{}, false
	}
	f := strings.Fields(text[nameEnd+1:])
	// f[0]=state f[1]=ppid … f[11]=utime f[12]=stime … f[19]=starttime … f[21]=rss (sayfa)
	if len(f) < 22 || len(f[0]) == 0 {
		return procStat{}, false
	}
	ppid, err1 := strconv.Atoi(f[1])
	utime, err2 := strconv.ParseUint(f[11], 10, 64)
	stime, err3 := strconv.ParseUint(f[12], 10, 64)
	start, err4 := strconv.ParseUint(f[19], 10, 64)
	rss, err5 := strconv.ParseInt(f[21], 10, 64)
	if err1 != nil || err2 != nil || err3 != nil || err4 != nil || err5 != nil {
		return procStat{}, false
	}
	return procStat{
		key:    procKey{pid: pid, start: start},
		name:   text[nameStart+1 : nameEnd],
		state:  f[0][0],
		kernel: pid == 2 || ppid == 2, // kthreadd ve çocukları
		ticks:  utime + stime,
		rssMB:  float64(max(rss, 0)) * pageMB,
	}, true
}
