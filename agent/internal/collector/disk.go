package collector

import (
	"sync"
	"syscall"
	"time"
)

type DiskUsage struct {
	Mount   string
	UsedPct float64
	Total   int64
	Free    int64
	// InodesUsedPct, inode doluluğudur; nil = bilinmiyor (btrfs gibi dosya sistemleri inode sayısı bildirmez).
	// Disk yüzdesinden önce dolabilir ve dosya oluşturmayı engeller.
	InodesUsedPct *float64
}

// statfsTimeout tek bir statfs() çağrısını sınırlar. Takılan bir ağ mount'unda (yanıt
// vermeyi bırakan bir NFS sunucusu) syscall dakikalarca bloke olabilir ve tüm toplama
// döngüsünü — CPU, RAM ve Docker dahil — dondurabilirdi.
var statfsTimeout = 2 * time.Second // değişken, testler kısaltabilsin diye

var (
	statfsFn       = syscall.Statfs // testlerde değiştirilir
	statfsInFlight sync.Map         // mount -> struct{}: henüz dönmemiş bir çağrı
)

// SampleDisk her mount noktasının kullanımını raporlar. "auto" girdisi (bkz. AutoMounts)
// sunucudaki tüm gerçek dosya sistemlerini temsil eder. statfs edilemeyen bir mount
// (yapılandırma hatası, unmount edilmiş birim, takılan ağ paylaşımı) tüm toplama döngüsünü
// bozmak yerine atlanır ve döngüyü asla statfsTimeout'tan uzun bekletmez.
func SampleDisk(mounts []string) []DiskUsage {
	return sampleDisk(mounts, DiscoverMounts)
}

func sampleDisk(configured []string, discover func() ([]string, error)) []DiskUsage {
	mounts, autoOnly := expandMountsMarked(configured, discover)

	type result struct {
		usage DiskUsage
		fsid  syscall.Fsid
		ok    bool
	}
	results := make([]result, len(mounts))
	var wg sync.WaitGroup
	for i, mount := range mounts {
		wg.Add(1)
		go func() { // paralel, böylece N takılı mount N değil bir zaman aşımına mal olur
			defer wg.Done()
			stat, ok := statWithTimeout(mount)
			if !ok {
				return
			}
			total := int64(stat.Blocks) * int64(stat.Bsize)
			free := int64(stat.Bavail) * int64(stat.Bsize)
			var usedPct float64
			if total > 0 {
				usedPct = float64(total-free) / float64(total) * 100
			}
			results[i] = result{DiskUsage{Mount: mount, UsedPct: usedPct, Total: total, Free: free, InodesUsedPct: inodePct(uint64(stat.Files), uint64(stat.Ffree))}, stat.Fsid, true}
		}()
	}
	wg.Wait()

	type entry struct {
		usage DiskUsage
		fsid  syscall.Fsid
	}
	var entries []entry
	for _, r := range results {
		if r.ok {
			entries = append(entries, entry{r.usage, r.fsid})
		}
	}

	// btrfs'te "/" ve "/home" gibi alt hacimler ayrı mount noktasıdır (mountinfo'da farklı
	// aygıt numarası taşırlar) ama aynı dosya sistemidir: statfs aynı Fsid'i verir ve doluluk
	// aynıdır. Yalnızca "auto" ile bulunanlar arasında, aynı Fsid'e sahip olanlardan en kısa yol
	// kalır; adıyla istenmiş bir mount asla elenmez. Fsid'i sıfır olan dosya sistemleri
	// (bazı ağ/FUSE sistemleri) birbirine benzetilmez.
	keep := map[syscall.Fsid]string{} // fsid -> tutulacak "auto" mount
	for _, e := range entries {
		if e.fsid == (syscall.Fsid{}) || !autoOnly[e.usage.Mount] {
			continue
		}
		if cur, ok := keep[e.fsid]; !ok || len(e.usage.Mount) < len(cur) || (len(e.usage.Mount) == len(cur) && e.usage.Mount < cur) {
			keep[e.fsid] = e.usage.Mount
		}
	}
	explicitFsid := map[syscall.Fsid]bool{} // adıyla istenmiş bir mount'un bulunduğu dosya sistemleri
	for _, e := range entries {
		if e.fsid != (syscall.Fsid{}) && !autoOnly[e.usage.Mount] {
			explicitFsid[e.fsid] = true
		}
	}

	out := make([]DiskUsage, 0, len(entries))
	for _, e := range entries {
		if autoOnly[e.usage.Mount] && e.fsid != (syscall.Fsid{}) {
			// Aynı dosya sistemi adıyla da istenmişse "auto" kopyası gereksizdir.
			if explicitFsid[e.fsid] || keep[e.fsid] != e.usage.Mount {
				continue
			}
		}
		out = append(out, e.usage)
	}
	return out
}

func statWithTimeout(mount string) (syscall.Statfs_t, bool) {
	// Bu mount için önceki çağrı hâlâ takılı: her döngüde üzerine bir goroutine (ve kesilemez
	// bir syscall) daha yığma.
	if _, busy := statfsInFlight.LoadOrStore(mount, struct{}{}); busy {
		return syscall.Statfs_t{}, false
	}
	type outcome struct {
		stat syscall.Statfs_t
		err  error
	}
	done := make(chan outcome, 1)
	go func() {
		var st syscall.Statfs_t
		err := statfsFn(mount, &st)
		statfsInFlight.Delete(mount)
		done <- outcome{st, err}
	}()
	select {
	case o := <-done:
		return o.stat, o.err == nil
	case <-time.After(statfsTimeout):
		return syscall.Statfs_t{}, false
	}
}

// inodePct, toplam ve boş inode sayısından doluluk yüzdesini verir; toplam 0 ise (inode bildirmeyen dosya
// sistemi) ya da tutarsızsa nil.
func inodePct(files, free uint64) *float64 {
	if files == 0 || free > files {
		return nil
	}
	v := float64(files-free) / float64(files) * 100
	return &v
}
