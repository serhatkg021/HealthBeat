package collector

import (
	"bufio"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"
)

// AutoMounts, "gerçek dosya sistemlerini keşfet" anlamına gelen disk_mounts girdisidir.
const AutoMounts = "auto"

// nonDiskFilesystems, kimsenin alert istemeyeceği dosya sistemi türleridir: çekirdek/sanal
// dosya sistemleri, bellek tabanlılar ve tasarımı gereği hep %100 dolu olan salt okunur
// imajlar (snap paketleri squashfs'tir — tipik bir masaüstünde onlarca vardır). Ağ ve FUSE
// dosya sistemleri dahil geri kalan her şey gerçek disk sayılır.
var nonDiskFilesystems = map[string]struct{}{
	// çekirdek / sanal
	"proc": {}, "sysfs": {}, "devtmpfs": {}, "devpts": {}, "cgroup": {}, "cgroup2": {}, "pstore": {},
	"bpf": {}, "debugfs": {}, "tracefs": {}, "securityfs": {}, "configfs": {}, "fusectl": {},
	"mqueue": {}, "hugetlbfs": {}, "autofs": {}, "binfmt_misc": {}, "rpc_pipefs": {}, "nsfs": {},
	"efivarfs": {}, "selinuxfs": {}, "overlay": {}, "aufs": {},
	// bellek tabanlı
	"tmpfs": {}, "ramfs": {},
	// salt okunur imajlar: hep dolu
	"squashfs": {}, "erofs": {}, "iso9660": {}, "udf": {}, "cramfs": {},
	// depolama olmayan FUSE yardımcıları
	"fuse.portal": {}, "fuse.gvfsd-fuse": {}, "fuse.lxcfs": {}, "fuse.snapfuse": {}, "fuse.gvfs-fuse-daemon": {},
}

// DiscoverMounts, bu sunucudaki gerçek dosya sistemlerinin mount noktalarını
// /proc/self/mountinfo'dan okuyarak listeler.
func DiscoverMounts() ([]string, error) {
	f, err := os.Open("/proc/self/mountinfo")
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return parseMountinfo(f), nil
}

// parseMountinfo, mountinfo biçimindeki girdiden gerçek dosya sistemlerinin mount
// noktalarını çıkarır (bkz. proc(5)). Bir satır şöyle görünür:
//
//	36 35 98:0 /root /mnt/point rw,noatime master:1 - ext4 /dev/sda1 rw,errors=remount-ro
//
// yani mount kimliği, üst kimlik, major:minor, dosya sistemi içindeki kök, mount noktası,
// seçenekler, isteğe bağlı alanlar, "-", dosya sistemi türü, kaynak, süper seçenekler.
func parseMountinfo(r io.Reader) []string {
	type entry struct{ device, point string }
	var entries []entry

	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		sep := -1
		for i, f := range fields {
			if f == "-" {
				sep = i
				break
			}
		}
		if sep < 6 || sep+1 >= len(fields) {
			continue // hatalı biçimli
		}
		fsType := fields[sep+1]
		if _, skip := nonDiskFilesystems[fsType]; skip {
			continue
		}
		point := unescapeMountinfo(fields[4])
		if !strings.HasPrefix(point, "/") {
			continue
		}
		entries = append(entries, entry{device: fields[2], point: point})
	}

	// Aynı dosya sisteminin birden çok yerde mount edilmesi (bind mount'lar, bir aygıtın btrfs
	// alt hacimleri) aynı kullanımı raporlar, dolayısıyla yol başına bir alert üretirdi. Aygıt
	// başına tek mount noktası tutulur: en kısası, yani en az özel olanı ("/srv/bind" değil "/").
	// Major numarası 0 olan aygıtlar anonimdir (NFS, btrfs alt hacimleri, FUSE) ve her mount
	// kendi dosya sistemidir.
	best := map[string]string{}
	var anonymous []string
	for _, e := range entries {
		if strings.HasPrefix(e.device, "0:") {
			anonymous = append(anonymous, e.point)
			continue
		}
		if cur, ok := best[e.device]; !ok || len(e.point) < len(cur) || (len(e.point) == len(cur) && e.point < cur) {
			best[e.device] = e.point
		}
	}
	out := append([]string(nil), anonymous...)
	for _, p := range best {
		out = append(out, p)
	}
	sort.Strings(out)
	return dedupe(out)
}

// unescapeMountinfo, çekirdeğin mount yollarında kullandığı sekizlik kaçışları çözer
// (\040 = boşluk, \011 = sekme, \012 = satır sonu, \134 = ters bölü).
func unescapeMountinfo(s string) string {
	if !strings.Contains(s, `\`) {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+3 < len(s) {
			if n, err := strconv.ParseUint(s[i+1:i+4], 8, 8); err == nil {
				b.WriteByte(byte(n))
				i += 3
				continue
			}
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

func dedupe(in []string) []string {
	seen := make(map[string]struct{}, len(in))
	out := in[:0:0]
	for _, s := range in {
		if _, dup := seen[s]; !dup {
			seen[s] = struct{}{}
			out = append(out, s)
		}
	}
	return out
}

// expandMounts, "auto" girdisini keşfedilen mount'larla değiştirir ve açıkça yapılandırılmış
// her yolu korur (sıra: yapılandırıldığı gibi; keşfedilenler "auto"nun yerinde; tekrarlar atılır).
func expandMounts(configured []string, discover func() ([]string, error)) []string {
	mounts, _ := expandMountsMarked(configured, discover)
	return mounts
}

// expandMountsMarked, döndürdüğü mount'lardan hangilerinin YALNIZCA "auto" yoluyla geldiğini de
// söyleyen expandMounts'tur: bunlar adıyla istenmemiş olanlardır, bu yüzden kopya sayılıp
// elenebilir (bkz. dropSameFilesystem); açıkça yazılmış girdi asla elenmez.
func expandMountsMarked(configured []string, discover func() ([]string, error)) (mounts []string, autoOnly map[string]bool) {
	hasAuto := false
	explicit := make(map[string]bool, len(configured))
	for _, m := range configured {
		if m == AutoMounts {
			hasAuto = true
		} else {
			explicit[m] = true
		}
	}
	if !hasAuto {
		return configured, nil
	}
	found, err := discover()
	if err != nil {
		found = nil // mount tablosu okunamadı: yalnızca açık girdilere dön
	}
	autoOnly = map[string]bool{}
	var out []string
	for _, m := range configured {
		if m == AutoMounts {
			out = append(out, found...)
			for _, f := range found {
				if !explicit[f] {
					autoOnly[f] = true
				}
			}
		} else {
			out = append(out, m)
		}
	}
	return dedupe(out), autoOnly
}
