package collector

import (
	"bufio"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Protokol 4'ün dosya okumasıyla alınan sistem durumu: bellek ayrıntısı ve swap/OOM sayaçları (/proc/meminfo,
// /proc/vmstat), PSI (/proc/pressure) ve yazılım RAID (/proc/mdstat). Hepsi yetkisiz okunur ve her raporda okunur
// (mikrosaniyeler). Okunamayan alan nil'dir ("bilinmiyor").

// MemoryStats, RAM yüzdesinin ötesindeki bellek durumudur.
type MemoryStats struct {
	AvailableMB *int64   // MemAvailable (3.14+ çekirdek)
	CachedMB    *int64   // sayfa önbelleği + tamponlar + geri kazanılabilir slab (`free` komutundaki "buff/cache")
	SwapInPerS  *float64 // swap'tan okunan sayfa/sn
	SwapOutPerS *float64 // swap'a yazılan sayfa/sn
	OOMKills    *uint64  // açılıştan beri (4.13+ çekirdek)
}

// MemoryCollector, swap hızı için önceki /proc/vmstat örneğini tutar. Eşzamanlı çağrılabilir.
type MemoryCollector struct {
	root string           // sahte kök (test); "" = gerçek /
	now  func() time.Time // test için

	mu   sync.Mutex
	prev *swapSample
}

type swapSample struct {
	in, out uint64
	at      time.Time
}

func NewMemoryCollector() *MemoryCollector { return &MemoryCollector{now: time.Now} }

// Sample, bellek ayrıntısını okur. Swap hızı ilk çağrıda ve sayaç geri gittiğinde (yeniden açılış) bilinmiyor.
func (c *MemoryCollector) Sample() MemoryStats {
	var out MemoryStats
	if f, err := os.Open(rootPath(c.root, "/proc/meminfo")); err == nil {
		out.AvailableMB, out.CachedMB = parseMemoryDetail(f)
		f.Close()
	}

	f, err := os.Open(rootPath(c.root, "/proc/vmstat"))
	if err != nil {
		return out
	}
	vm := parseVMStat(f)
	f.Close()
	if v, ok := vm["oom_kill"]; ok {
		out.OOMKills = &v
	}
	in, okIn := vm["pswpin"]
	outPages, okOut := vm["pswpout"]
	if !okIn || !okOut {
		return out
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	cur := swapSample{in: in, out: outPages, at: c.now()}
	if p := c.prev; p != nil && cur.in >= p.in && cur.out >= p.out {
		if secs := cur.at.Sub(p.at).Seconds(); secs > 0 {
			inRate, outRate := float64(cur.in-p.in)/secs, float64(cur.out-p.out)/secs
			out.SwapInPerS, out.SwapOutPerS = &inRate, &outRate
		}
	}
	c.prev = &cur
	return out
}

// parseMemoryDetail, /proc/meminfo'dan kullanılabilir belleği ve önbelleği MB olarak okur.
func parseMemoryDetail(r io.Reader) (available, cached *int64) {
	values := map[string]uint64{}
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		name, _, ok := strings.Cut(scanner.Text(), ":")
		if !ok {
			continue
		}
		switch name {
		case "MemAvailable", "Buffers", "Cached", "SReclaimable":
			values[name] = parseMeminfoKB(scanner.Text())
		}
	}
	if v, ok := values["MemAvailable"]; ok {
		mb := int64(v / 1024)
		available = &mb
	}
	if _, ok := values["Cached"]; ok {
		mb := int64((values["Buffers"] + values["Cached"] + values["SReclaimable"]) / 1024)
		cached = &mb
	}
	return available, cached
}

// parseVMStat, /proc/vmstat'ın "ad değer" satırlarını okur.
func parseVMStat(r io.Reader) map[string]uint64 {
	out := map[string]uint64{}
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) != 2 {
			continue
		}
		if v, err := strconv.ParseUint(fields[1], 10, 64); err == nil {
			out[fields[0]] = v
		}
	}
	return out
}

// PressureStall, bir kaynağın PSI değerleridir: süreçlerin beklerken geçirdiği zamanın yüzdesi.
type PressureStall struct {
	Some10, Some60 float64
	Full10, Full60 *float64
}

// SystemPressure, CPU, bellek ve G/Ç'nin PSI'ıdır; dosyası olmayan kaynak nil.
type SystemPressure struct {
	CPU, Memory, IO *PressureStall
}

// ReadPressure, /proc/pressure'ı okur. PSI'ı olmayan çekirdekte (4.20 öncesi ya da RHEL 8'deki gibi kapalı) hepsi nil.
func ReadPressure(root string) SystemPressure {
	read := func(name string, withFull bool) *PressureStall {
		b, err := os.ReadFile(rootPath(root, "/proc/pressure/"+name))
		if err != nil {
			return nil
		}
		return parsePressure(string(b), withFull)
	}
	// CPU'nun "full" satırı sistem düzeyinde tanımsızdır (çekirdek 5.13+ hep 0 yazar): gönderilmez.
	return SystemPressure{CPU: read("cpu", false), Memory: read("memory", true), IO: read("io", true)}
}

// parsePressure, "some avg10=0.00 avg60=0.00 avg300=0.00 total=0" biçimindeki satırları okur; "some" yoksa nil.
func parsePressure(text string, withFull bool) *PressureStall {
	var out PressureStall
	haveSome := false
	for _, line := range strings.Split(text, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		avg := map[string]float64{}
		for _, f := range fields[1:] {
			k, v, ok := strings.Cut(f, "=")
			if !ok {
				continue
			}
			if x, err := strconv.ParseFloat(v, 64); err == nil && x >= 0 && x <= 100 {
				avg[k] = x
			}
		}
		a10, ok10 := avg["avg10"]
		a60, ok60 := avg["avg60"]
		if !ok10 || !ok60 {
			continue
		}
		switch fields[0] {
		case "some":
			out.Some10, out.Some60, haveSome = a10, a60, true
		case "full":
			if withFull {
				out.Full10, out.Full60 = &a10, &a60
			}
		}
	}
	if !haveSome {
		return nil
	}
	return &out
}

// RAIDArray, bir yazılım RAID dizisidir.
type RAIDArray struct {
	Name    string
	Level   string
	State   string // clean | degraded | recovering | resyncing | reshaping | checking | inactive
	Devices int
	Active  int
	SyncPct *float64
}

var (
	mdHeader   = regexp.MustCompile(`^(md\S*)\s*:\s*(\S+)\s*(.*)$`)
	mdCounts   = regexp.MustCompile(`\[(\d+)/(\d+)\]`)
	mdProgress = regexp.MustCompile(`\b(recovery|resync|reshape|check|repair)\s*=\s*([\d.]+)%`)
	mdDelayed  = regexp.MustCompile(`\b(resync|recovery)\s*=\s*(DELAYED|PENDING)`)
)

// ReadRAID, /proc/mdstat'ı okur; dosya yoksa (md modülü yüklü değil) ya da dizi yoksa nil.
func ReadRAID(root string) []RAIDArray {
	b, err := os.ReadFile(rootPath(root, "/proc/mdstat"))
	if err != nil {
		return nil
	}
	return parseMDStat(string(b))
}

// parseMDStat, /proc/mdstat metnini dizilere çevirir. Durum önceliği: eşitleme/yeniden kurulum sürüyorsa o, değilse
// eksik üye ya da (F) işaretli üye varsa degraded, yoksa clean.
func parseMDStat(text string) []RAIDArray {
	var out []RAIDArray
	var cur *RAIDArray
	failed := false
	flush := func() {
		if cur == nil {
			return
		}
		if cur.State == "" {
			switch {
			case failed || cur.Active < cur.Devices:
				cur.State = "degraded"
			default:
				cur.State = "clean"
			}
		}
		out = append(out, *cur)
		cur, failed = nil, false
	}
	for _, line := range strings.Split(text, "\n") {
		if m := mdHeader.FindStringSubmatch(line); m != nil && !strings.HasPrefix(line, " ") {
			flush()
			cur = &RAIDArray{Name: m[1]}
			if m[2] == "inactive" {
				cur.State = "inactive"
			}
			// "active (auto-read-only) raid1 sda[0] sdb[1]": parantezli işaretler atlanır, ilk düz sözcük seviyedir.
			for _, tok := range strings.Fields(m[3]) {
				switch {
				case strings.Contains(tok, "["):
					cur.Devices++
					if strings.Contains(tok, "(F)") {
						failed = true
					}
				case strings.HasPrefix(tok, "("):
				case cur.Level == "" && m[2] != "inactive":
					cur.Level = tok
				}
			}
			cur.Active = cur.Devices
			continue
		}
		if cur == nil {
			continue
		}
		if m := mdCounts.FindStringSubmatch(line); m != nil {
			cur.Devices, _ = strconv.Atoi(m[1])
			cur.Active, _ = strconv.Atoi(m[2])
		}
		if m := mdProgress.FindStringSubmatch(line); m != nil && cur.State == "" {
			cur.State = map[string]string{"recovery": "recovering", "resync": "resyncing", "reshape": "reshaping",
				"check": "checking", "repair": "checking"}[m[1]]
			if pct, err := strconv.ParseFloat(m[2], 64); err == nil {
				cur.SyncPct = &pct
			}
		} else if m := mdDelayed.FindStringSubmatch(line); m != nil && cur.State == "" {
			cur.State = map[string]string{"recovery": "recovering", "resync": "resyncing"}[m[1]]
		}
	}
	flush()
	return out
}

func rootPath(root, p string) string {
	if root == "" {
		return p
	}
	return filepath.Join(root, p)
}
