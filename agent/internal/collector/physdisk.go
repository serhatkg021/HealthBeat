package collector

import (
	"bufio"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// PhysicalDisk, bir fiziksel (ya da sanal) blok aygıtını ve üzerindeki raporlanan mount'ları
// tanımlar. Bir mount birden çok diske düşebilir (LVM/mdraid birden çok aygıta yayılır) ve bazı
// mount'lar hiçbir diske düşmez (NFS, tmpfs); bu yüzden ilişki "mount → 0..N disk"tir.
type PhysicalDisk struct {
	Name      string   // çekirdeğin adı: "nvme0n1", "sda"
	Model     string   // /sys/block/<ad>/device/model; sanal aygıtlarda boş olabilir
	SizeBytes int64    // aygıtın ham boyutu (dosya sistemi boyutu değil)
	Kind      string   // "nvme", "ssd" ya da "hdd" (bkz. diskKind)
	Mounts    []string // bu diske düşen, raporlanan mount noktaları (sıralı)
}

// PhysicalDisks, raporlanan mount'ların üzerinde durduğu fiziksel diskleri keşfeder. Hiçbir disk
// bulunamazsa (konteyner, salt sanal dosya sistemleri) nil döner; server bu durumu "bilinmiyor"
// sayıp son bilinen değeri korur.
func PhysicalDisks(mounts []string) []PhysicalDisk {
	f, err := os.Open("/proc/self/mountinfo")
	if err != nil {
		return nil
	}
	defer f.Close()
	return physicalDisks("/sys", f, mounts)
}

// MountsOf, disk örneğindeki mount noktalarını döndürür (PhysicalDisks'in girdisi).
func MountsOf(disks []DiskUsage) []string {
	out := make([]string, len(disks))
	for i, d := range disks {
		out[i] = d.Mount
	}
	return out
}

// mountDevice, mountinfo'daki bir satırın aygıt tarafıdır.
type mountDevice struct {
	point  string
	majMin string // "259:2"; major'ı 0 olan aygıtlar anonimdir (btrfs, NFS, tmpfs)
	source string // "/dev/nvme0n1p2", "tmpfs", "server:/export"
}

// parseMountDevices, mountinfo'daki TÜM satırları (sanal dosya sistemleri dahil) döndürür:
// bir mount'un hangi dosya sisteminde olduğunu bulmak için "en uzun ön ek" araması yapılır ve
// tmpfs gibi bir mount atlanırsa arama yanlışlıkla "/"in diskine düşerdi.
func parseMountDevices(r io.Reader) []mountDevice {
	var out []mountDevice
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
		if sep < 6 || sep+2 >= len(fields) {
			continue // hatalı biçimli
		}
		out = append(out, mountDevice{
			point:  unescapeMountinfo(fields[4]),
			majMin: fields[2],
			source: unescapeMountinfo(fields[sep+2]),
		})
	}
	return out
}

// owningMount, path'i içeren dosya sisteminin mountinfo satırını bulur: yol sınırına saygılı en
// uzun mount noktası ("/data" "/database"i kapsamaz). Aynı noktaya sonradan yapılan mount
// öncekinin üstünü örter; bu yüzden eşit uzunlukta SONUNCUSU kazanır.
func owningMount(devs []mountDevice, path string) (mountDevice, bool) {
	var best mountDevice
	found := false
	for _, d := range devs {
		if !pathWithin(d.point, path) {
			continue
		}
		if !found || len(d.point) >= len(best.point) {
			best, found = d, true
		}
	}
	return best, found
}

func pathWithin(mountPoint, path string) bool {
	if mountPoint == "/" {
		return strings.HasPrefix(path, "/")
	}
	return path == mountPoint || strings.HasPrefix(path, mountPoint+"/")
}

func physicalDisks(sysRoot string, mountinfo io.Reader, mounts []string) []PhysicalDisk {
	devs := parseMountDevices(mountinfo)
	byDisk := map[string]map[string]struct{}{} // disk adı -> mount kümesi
	for _, mount := range mounts {
		dev, ok := owningMount(devs, mount)
		if !ok {
			continue
		}
		for _, disk := range disksOf(sysRoot, dev) {
			if byDisk[disk] == nil {
				byDisk[disk] = map[string]struct{}{}
			}
			byDisk[disk][mount] = struct{}{}
		}
	}

	var out []PhysicalDisk
	for name, set := range byDisk {
		d := readDiskInfo(sysRoot, name)
		for m := range set {
			d.Mounts = append(d.Mounts, m)
		}
		sort.Strings(d.Mounts)
		out = append(out, d)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// disksOf, bir mount'un üzerinde durduğu fiziksel disk adlarını döndürür.
func disksOf(sysRoot string, dev mountDevice) []string {
	name := blockNameOf(sysRoot, dev)
	if name == "" {
		return nil
	}
	seen := map[string]struct{}{}
	var disks []string
	wholeDisks(sysRoot, name, 0, seen, &disks)
	sort.Strings(disks)
	return disks
}

// blockNameOf, çekirdeğin blok aygıt adını bulur: önce major:minor (/sys/dev/block), anonim
// aygıtlarda (btrfs) mountinfo kaynağı olan /dev yolu. Bulunamayan (NFS, tmpfs, FUSE) "" döner.
func blockNameOf(sysRoot string, dev mountDevice) string {
	if major, _, ok := strings.Cut(dev.majMin, ":"); ok && major != "0" {
		if target, err := os.Readlink(filepath.Join(sysRoot, "dev", "block", dev.majMin)); err == nil {
			return filepath.Base(target)
		}
	}
	if strings.HasPrefix(dev.source, "/dev/") {
		path := dev.source
		if resolved, err := filepath.EvalSymlinks(path); err == nil {
			path = resolved // /dev/mapper/vg-root -> /dev/dm-0
		}
		name := filepath.Base(path)
		if _, err := os.Stat(filepath.Join(sysRoot, "class", "block", name)); err == nil {
			return name
		}
	}
	return ""
}

// maxSlaveDepth, iç içe aygıt zincirini (LUKS içinde LVM içinde mdraid...) sınırlar; döngüye
// karşı da görülenler kümesi tutulur.
const maxSlaveDepth = 8

// wholeDisks, bir blok aygıtından fiziksel disklere iner: dm/md aygıtları "slaves" altındaki
// aygıtlara, bölümler üst diske çözülür, geri kalanlar zaten disktir. Döngü (loop), ram ve zram
// gibi bellek/dosya destekli aygıtlar fiziksel disk sayılmaz.
func wholeDisks(sysRoot, name string, depth int, seen map[string]struct{}, out *[]string) {
	if _, dup := seen[name]; dup || depth > maxSlaveDepth || isVirtualBlock(name) {
		return
	}
	seen[name] = struct{}{}

	dir := filepath.Join(sysRoot, "class", "block", name)
	if slaves, err := os.ReadDir(filepath.Join(dir, "slaves")); err == nil && len(slaves) > 0 {
		for _, s := range slaves {
			wholeDisks(sysRoot, s.Name(), depth+1, seen, out)
		}
		return
	}
	if _, err := os.Stat(filepath.Join(dir, "partition")); err == nil {
		// Bir bölümün gerçek sysfs yolu üst diskin dizininin altındadır: .../nvme0n1/nvme0n1p2
		if real, err := filepath.EvalSymlinks(dir); err == nil {
			wholeDisks(sysRoot, filepath.Base(filepath.Dir(real)), depth+1, seen, out)
		}
		return
	}
	if _, err := os.Stat(filepath.Join(sysRoot, "block", name)); err == nil {
		*out = append(*out, name)
	}
}

func isVirtualBlock(name string) bool {
	for _, p := range []string{"loop", "ram", "zram"} {
		if strings.HasPrefix(name, p) {
			return true
		}
	}
	return false
}

func readDiskInfo(sysRoot, name string) PhysicalDisk {
	base := filepath.Join(sysRoot, "block", name)
	d := PhysicalDisk{Name: name, Model: readTrimmed(filepath.Join(base, "device", "model"))}
	if sectors, err := strconv.ParseInt(readTrimmed(filepath.Join(base, "size")), 10, 64); err == nil && sectors > 0 {
		d.SizeBytes = sectors * 512 // sysfs boyutu her zaman 512 baytlık birimdir
	}
	d.Kind = diskKind(name, readTrimmed(filepath.Join(base, "queue", "rotational")))
	return d
}

// diskKind: NVMe adından, aksi halde çekirdeğin "rotational" bayrağından türetilir. Sanal
// makinelerde bu bayrak gerçeği yansıtmayabilir (sanal disk kendini dönen diskmiş gibi
// bildirebilir); bilinmiyorsa "" döner.
func diskKind(name, rotational string) string {
	switch {
	case strings.HasPrefix(name, "nvme"):
		return "nvme"
	case rotational == "1":
		return "hdd"
	case rotational == "0":
		return "ssd"
	default:
		return ""
	}
}

func readTrimmed(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}
