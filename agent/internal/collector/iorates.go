package collector

import (
	"bufio"
	"io"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Disk G/Ç (/proc/diskstats), ağ (/proc/net/dev) ve TCP (/proc/net/snmp, /proc/net/sockstat). Oranlar iki rapor
// arasındaki farktır (iostat'ın formülleri): agent'ın ilk raporunda ve sayaç geri gittiğinde (yeniden açılış, arayüzün
// yeniden oluşması, 32 bit sayacın taşması) o disk ya da arayüz raporlanmaz.

// maxNetInterfaces, raporlanan ağ arayüzü sayısının üst sınırıdır.
const maxNetInterfaces = 32

// sectorBytes, /proc/diskstats'taki sektör birimidir; diskin gerçek sektör boyutundan bağımsız olarak hep 512'dir.
const sectorBytes = 512

// DiskIORate, bir diskin aralıktaki G/Ç'sidir.
type DiskIORate struct {
	Name                string
	ReadIOPS, WriteIOPS float64
	ReadBps, WriteBps   float64 // bayt/sn
	UtilPct             float64 // meşgul geçen zamanın yüzdesi
	AwaitMs             float64 // bir işlemin ortalama süresi; aralıkta işlem yoksa 0
	QueueDepth          float64 // ortalama bekleyen işlem
}

// NetIORate, bir ağ arayüzünün aralıktaki trafiğidir.
type NetIORate struct {
	Interface                            string
	RxBps, TxBps                         float64 // bit/sn
	RxErrors, TxErrors, RxDrops, TxDrops uint64  // aralıktaki fark
}

// TCPStats, makine geneli TCP durumudur; nil = bilinmiyor.
type TCPStats struct {
	RetransPct  *float64 // aralıkta yeniden gönderilen segment / gönderilen segment; aralıkta hiç gönderim yoksa bilinmiyor
	Established *int     // kurulu bağlantı (CurrEstab)
	TimeWait    *int     // TIME_WAIT soketleri
}

// IOSample, bir rapordaki G/Ç ve ağ oranlarıdır.
type IOSample struct {
	Disks []DiskIORate
	Net   []NetIORate
	TCP   TCPStats
}

type diskCounters struct {
	reads, sectorsRead, msRead, writes, sectorsWritten, msWrite, ioTicks, weighted uint64
}

type netCounters struct {
	rxBytes, rxErrs, rxDrop, txBytes, txErrs, txDrop uint64
}

// IOCollector, oranlar için önceki örneği tutar. Eşzamanlı çağrılabilir.
type IOCollector struct {
	root string           // sahte kök (test); "" = gerçek /
	now  func() time.Time // test için

	mu         sync.Mutex
	prevAt     time.Time
	prevDisk   map[string]diskCounters
	prevNet    map[string]netCounters
	prevOut    *uint64 // TCP OutSegs
	prevRetran *uint64 // TCP RetransSegs
}

func NewIOCollector() *IOCollector { return &IOCollector{now: time.Now} }

// Sample, verilen fiziksel disklerin G/Ç'sini, ağ arayüzlerinin trafiğini ve TCP durumunu döndürür.
func (c *IOCollector) Sample(disks []string) IOSample {
	diskNow := readDiskstats(rootPath(c.root, "/proc/diskstats"))
	netNow, netOrder := readNetDev(rootPath(c.root, "/proc/net/dev"))
	snmp := readSNMPTcp(rootPath(c.root, "/proc/net/snmp"))
	tw := readSockstatTW(rootPath(c.root, "/proc/net/sockstat"))

	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.now()
	var out IOSample
	secs := now.Sub(c.prevAt).Seconds()
	havePrev := !c.prevAt.IsZero() && secs > 0

	if havePrev {
		for _, name := range disks {
			cur, ok1 := diskNow[name]
			prev, ok2 := c.prevDisk[name]
			if ok1 && ok2 {
				if r, ok := diskRate(name, prev, cur, secs); ok {
					out.Disks = append(out.Disks, r)
				}
			}
		}
		for _, name := range netOrder {
			if len(out.Net) == maxNetInterfaces {
				break
			}
			prev, ok := c.prevNet[name]
			if !ok || ignoredInterface(name) {
				continue
			}
			if r, ok := netRate(name, prev, netNow[name], secs); ok {
				out.Net = append(out.Net, r)
			}
		}
	}

	if v, ok := snmp["CurrEstab"]; ok {
		n := int(v)
		out.TCP.Established = &n
	}
	if tw != nil {
		out.TCP.TimeWait = tw
	}
	outSegs, okOut := snmp["OutSegs"]
	retrans, okRet := snmp["RetransSegs"]
	if okOut && okRet && havePrev && c.prevOut != nil && c.prevRetran != nil &&
		outSegs > *c.prevOut && retrans >= *c.prevRetran {
		pct := min(float64(retrans-*c.prevRetran)/float64(outSegs-*c.prevOut)*100, 100)
		out.TCP.RetransPct = &pct
	}

	c.prevAt, c.prevDisk, c.prevNet = now, diskNow, netNow
	c.prevOut, c.prevRetran = nil, nil
	if okOut && okRet {
		c.prevOut, c.prevRetran = &outSegs, &retrans
	}
	return out
}

func diskRate(name string, p, c diskCounters, secs float64) (DiskIORate, bool) {
	if c.reads < p.reads || c.writes < p.writes || c.sectorsRead < p.sectorsRead || c.sectorsWritten < p.sectorsWritten ||
		c.msRead < p.msRead || c.msWrite < p.msWrite || c.ioTicks < p.ioTicks || c.weighted < p.weighted {
		return DiskIORate{}, false
	}
	ops := float64(c.reads - p.reads + c.writes - p.writes)
	r := DiskIORate{
		Name:       name,
		ReadIOPS:   float64(c.reads-p.reads) / secs,
		WriteIOPS:  float64(c.writes-p.writes) / secs,
		ReadBps:    float64(c.sectorsRead-p.sectorsRead) * sectorBytes / secs,
		WriteBps:   float64(c.sectorsWritten-p.sectorsWritten) * sectorBytes / secs,
		UtilPct:    min(float64(c.ioTicks-p.ioTicks)/(secs*1000)*100, 100),
		QueueDepth: float64(c.weighted-p.weighted) / (secs * 1000),
	}
	if ops > 0 {
		r.AwaitMs = float64(c.msRead-p.msRead+c.msWrite-p.msWrite) / ops
	}
	return r, true
}

func netRate(name string, p, c netCounters, secs float64) (NetIORate, bool) {
	if c.rxBytes < p.rxBytes || c.txBytes < p.txBytes || c.rxErrs < p.rxErrs || c.txErrs < p.txErrs ||
		c.rxDrop < p.rxDrop || c.txDrop < p.txDrop {
		return NetIORate{}, false
	}
	return NetIORate{
		Interface: name,
		RxBps:     float64(c.rxBytes-p.rxBytes) * 8 / secs,
		TxBps:     float64(c.txBytes-p.txBytes) * 8 / secs,
		RxErrors:  c.rxErrs - p.rxErrs, TxErrors: c.txErrs - p.txErrs,
		RxDrops: c.rxDrop - p.rxDrop, TxDrops: c.txDrop - p.txDrop,
	}, true
}

// ignoredInterface: geri döngü ve container'ların iç trafiği (dışarı giden trafik fiziksel arayüzde zaten görünür).
func ignoredInterface(name string) bool {
	return name == "lo" || name == "docker0" || strings.HasPrefix(name, "veth") || strings.HasPrefix(name, "br-")
}

// readDiskstats, /proc/diskstats'ı okur: "major minor ad" + en az 11 sayaç (2.6+ çekirdek).
func readDiskstats(path string) map[string]diskCounters {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	return parseDiskstats(f)
}

func parseDiskstats(r io.Reader) map[string]diskCounters {
	out := map[string]diskCounters{}
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		f := strings.Fields(scanner.Text())
		if len(f) < 14 {
			continue
		}
		v := make([]uint64, 11)
		ok := true
		for i := range v {
			n, err := strconv.ParseUint(f[3+i], 10, 64)
			if err != nil {
				ok = false
				break
			}
			v[i] = n
		}
		if !ok {
			continue
		}
		// sütunlar: reads merged sectors ms | writes merged sectors ms | in-flight io_ticks weighted
		out[f[2]] = diskCounters{reads: v[0], sectorsRead: v[2], msRead: v[3], writes: v[4], sectorsWritten: v[6],
			msWrite: v[7], ioTicks: v[9], weighted: v[10]}
	}
	return out
}

// readNetDev, /proc/net/dev'i okur; arayüzleri dosyadaki sırasıyla da döndürür.
func readNetDev(path string) (map[string]netCounters, []string) {
	f, err := os.Open(path)
	if err != nil {
		return nil, nil
	}
	defer f.Close()
	return parseNetDev(f)
}

func parseNetDev(r io.Reader) (map[string]netCounters, []string) {
	out := map[string]netCounters{}
	var order []string
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		name, rest, ok := strings.Cut(scanner.Text(), ":") // eski çekirdeklerde "eth0:1234" bitişik yazılır
		if !ok {
			continue
		}
		name = strings.TrimSpace(name)
		f := strings.Fields(rest)
		if name == "" || strings.Contains(name, "|") || len(f) < 12 {
			continue
		}
		v := make([]uint64, 12)
		valid := true
		for i := range v {
			n, err := strconv.ParseUint(f[i], 10, 64)
			if err != nil {
				valid = false
				break
			}
			v[i] = n
		}
		if !valid {
			continue
		}
		// alım: bytes packets errs drop fifo frame compressed multicast | gönderim: bytes packets errs drop …
		out[name] = netCounters{rxBytes: v[0], rxErrs: v[2], rxDrop: v[3], txBytes: v[8], txErrs: v[10], txDrop: v[11]}
		order = append(order, name)
	}
	return out, order
}

// readSNMPTcp, /proc/net/snmp'nin "Tcp:" başlık ve değer satırlarını ad → değer olarak okur.
func readSNMPTcp(path string) map[string]uint64 {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	return parseSNMPTcp(string(b))
}

func parseSNMPTcp(text string) map[string]uint64 {
	var header []string
	for _, line := range strings.Split(text, "\n") {
		if !strings.HasPrefix(line, "Tcp:") {
			continue
		}
		fields := strings.Fields(line)[1:]
		if header == nil {
			header = fields
			continue
		}
		out := map[string]uint64{}
		for i, f := range fields {
			if i < len(header) {
				if n, err := strconv.ParseUint(f, 10, 64); err == nil { // MaxConn -1'dir: atlanır
					out[header[i]] = n
				}
			}
		}
		return out
	}
	return nil
}

// readSockstatTW, /proc/net/sockstat'taki TIME_WAIT soket sayısıdır.
func readSockstatTW(path string) *int {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	for _, line := range strings.Split(string(b), "\n") {
		f := strings.Fields(line)
		if len(f) == 0 || f[0] != "TCP:" {
			continue
		}
		for i := 1; i+1 < len(f); i += 2 {
			if f[i] == "tw" {
				if n, err := strconv.Atoi(f[i+1]); err == nil && n >= 0 {
					return &n
				}
			}
		}
	}
	return nil
}
