package collector

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// updatesTimeout, `apt list --upgradable`ın süresidir: paket veritabanını okur (~0,3 sn), yavaş disklerde birkaç sn.
const updatesTimeout = 30 * time.Second

// UpdatesInfo, bekleyen paket güncellemeleridir.
type UpdatesInfo struct {
	Pending, Security int
	// ListsUpdatedAt, paket listelerinin en son güncellendiği andır (apt update; çoğu dağıtımda günlük zamanlayıcı).
	// Agent listeleri kendisi güncellemez (root ve ağ gerekir): listeler eskiyse "0 güncelleme" yanıltıcı olur, panel
	// bunu bu tarihle gösterir. Sıfır = bilinmiyor.
	ListsUpdatedAt time.Time
}

// UpdatesCollector, bekleyen güncellemeleri yalnızca apt ailesinde (Debian, Ubuntu) sayar; `apt list` yetkisiz
// çalışır. Diğer ailelerde (RHEL: dnf yetkisizken paket listesini indirmeye kalkar) bilinmiyor.
type UpdatesCollector struct {
	root string // sahte kök (test); "" = gerçek /
	run  func(ctx context.Context, name string, args ...string) (string, error)
}

func NewUpdatesCollector() *UpdatesCollector {
	return &UpdatesCollector{run: func(ctx context.Context, name string, args ...string) (string, error) {
		return runCommandTimeout(ctx, updatesTimeout, name, args...)
	}}
}

// Collect, apt ailesi dışındaki makinede nil, nil ("bilinmiyor": alan gönderilmez) döndürür.
func (c *UpdatesCollector) Collect(ctx context.Context) (*UpdatesInfo, error) {
	b, _ := os.ReadFile(rootPath(c.root, "/etc/os-release"))
	if !isDebianFamily(parseOSRelease(strings.NewReader(string(b)))) {
		return nil, nil
	}
	out, err := c.run(ctx, "apt", "list", "--upgradable")
	if err != nil {
		return nil, err
	}
	info := parseAptUpgradable(out)
	info.ListsUpdatedAt = newestMtime(rootPath(c.root, "/var/lib/apt/lists"))
	return &info, nil
}

// parseAptUpgradable, `apt list --upgradable` satırlarını sayar: "ad/kaynak1,kaynak2 sürüm mimari [upgradable from: …]".
// Kaynaklardan biri "-security" ile bitiyorsa (noble-security, bookworm-security) güvenlik güncellemesidir.
func parseAptUpgradable(text string) UpdatesInfo {
	var info UpdatesInfo
	for _, line := range strings.Split(text, "\n") {
		if !strings.Contains(line, "[upgradable from") {
			continue
		}
		name, _, _ := strings.Cut(line, " ")
		_, suites, ok := strings.Cut(name, "/")
		if !ok {
			continue
		}
		info.Pending++
		for _, s := range strings.Split(suites, ",") {
			if strings.HasSuffix(s, "-security") {
				info.Security++
				break
			}
		}
	}
	return info
}

// newestMtime, dizindeki dosyaların en yeni değişiklik zamanıdır; okunamazsa sıfır.
func newestMtime(dir string) time.Time {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return time.Time{}
	}
	var newest time.Time
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), "Release") && !strings.HasSuffix(e.Name(), "Packages") {
			continue
		}
		if fi, err := os.Stat(filepath.Join(dir, e.Name())); err == nil && fi.ModTime().After(newest) {
			newest = fi.ModTime()
		}
	}
	return newest
}

// CapacityStats, çekirdeğin sınırlı tablolarının doluluğudur; nil = bilinmiyor.
type CapacityStats struct {
	FileHandles, FileHandlesMax *int64
	Conntrack, ConntrackMax     *int64 // bağlantı izleme modülü yüklü değilse yok
	Tasks, PIDMax               *int64 // görev = süreç + iş parçacığı: pid_max sınırı bunlara uygulanır
}

// ReadCapacity, /proc/sys ve /proc/loadavg'dan sınırları okur.
func ReadCapacity(root string) CapacityStats {
	var out CapacityStats
	// file-nr: ayrılmış, boşta (2.6'dan beri hep 0), en çok
	if f := strings.Fields(readText(rootPath(root, "/proc/sys/fs/file-nr"))); len(f) == 3 {
		alloc, e1 := strconv.ParseInt(f[0], 10, 64)
		free, e2 := strconv.ParseInt(f[1], 10, 64)
		maxN, e3 := strconv.ParseInt(f[2], 10, 64)
		if e1 == nil && e2 == nil && e3 == nil && alloc >= free {
			used := alloc - free
			out.FileHandles, out.FileHandlesMax = &used, &maxN
		}
	}
	out.Conntrack = readInt64(rootPath(root, "/proc/sys/net/netfilter/nf_conntrack_count"))
	out.ConntrackMax = readInt64(rootPath(root, "/proc/sys/net/netfilter/nf_conntrack_max"))
	if out.Conntrack == nil || out.ConntrackMax == nil {
		out.Conntrack, out.ConntrackMax = nil, nil
	}
	// loadavg: "0.10 0.20 0.30 çalışan/toplam son_pid"
	if f := strings.Fields(readText(rootPath(root, "/proc/loadavg"))); len(f) >= 4 {
		if _, total, ok := strings.Cut(f[3], "/"); ok {
			if n, err := strconv.ParseInt(total, 10, 64); err == nil {
				out.Tasks = &n
			}
		}
	}
	out.PIDMax = readInt64(rootPath(root, "/proc/sys/kernel/pid_max"))
	return out
}

func readInt64(path string) *int64 {
	n, err := strconv.ParseInt(strings.TrimSpace(readText(path)), 10, 64)
	if err != nil || n < 0 {
		return nil
	}
	return &n
}
