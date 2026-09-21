package collector

import (
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

// fakeSys, gerçek sysfs'in ilgili kısmını (devices altında gerçek dizinler; class/block, block ve
// dev/block altında bunlara sembolik bağlar) bir geçici dizinde kurar.
type fakeSys struct {
	t    *testing.T
	root string
}

func newFakeSys(t *testing.T) *fakeSys {
	return &fakeSys{t: t, root: t.TempDir()}
}

func (f *fakeSys) mkdir(rel string) string {
	f.t.Helper()
	p := filepath.Join(f.root, rel)
	if err := os.MkdirAll(p, 0o755); err != nil {
		f.t.Fatal(err)
	}
	return p
}

func (f *fakeSys) write(rel, content string) {
	f.t.Helper()
	f.mkdir(filepath.Dir(rel))
	if err := os.WriteFile(filepath.Join(f.root, rel), []byte(content), 0o644); err != nil {
		f.t.Fatal(err)
	}
}

func (f *fakeSys) link(rel, target string) {
	f.t.Helper()
	f.mkdir(filepath.Dir(rel))
	if err := os.Symlink(target, filepath.Join(f.root, rel)); err != nil {
		f.t.Fatal(err)
	}
}

// disk, tam bir disk kurar: devices/<ad> + block/<ad> + class/block/<ad> + dev/block/<majmin>.
func (f *fakeSys) disk(name, majMin, model string, sectors int, rotational string) {
	f.mkdir("devices/pci/" + name)
	f.link("block/"+name, "../devices/pci/"+name)
	f.link("class/block/"+name, "../../devices/pci/"+name)
	f.link("dev/block/"+majMin, "../../devices/pci/"+name)
	f.write("devices/pci/"+name+"/size", strconv.Itoa(sectors)+"\n")
	f.write("devices/pci/"+name+"/queue/rotational", rotational+"\n")
	if model != "" {
		f.write("devices/pci/"+name+"/device/model", model+"   \n") // sysfs boşlukla doldurur
	}
}

// part, bir diskin altına bölüm ekler.
func (f *fakeSys) part(disk, name, majMin string) {
	f.mkdir("devices/pci/" + disk + "/" + name)
	f.write("devices/pci/"+disk+"/"+name+"/partition", "2\n")
	f.link("class/block/"+name, "../../devices/pci/"+disk+"/"+name)
	f.link("dev/block/"+majMin, "../../devices/pci/"+disk+"/"+name)
}

// virt, slaves ile alttaki aygıtlara bağlanan bir dm/md aygıtı kurar.
func (f *fakeSys) virt(name, majMin string, slaves ...string) {
	f.mkdir("devices/virtual/" + name)
	f.link("class/block/"+name, "../../devices/virtual/"+name)
	f.link("dev/block/"+majMin, "../../devices/virtual/"+name)
	for _, s := range slaves {
		f.link("devices/virtual/"+name+"/slaves/"+s, "../../../pci/"+s)
	}
}

func TestPhysicalDisksMapsPartitionsToDiskAndGroupsMounts(t *testing.T) {
	s := newFakeSys(t)
	s.disk("nvme0n1", "259:0", "CT500P2SSD8", 976773168, "0")
	s.part("nvme0n1", "nvme0n1p1", "259:1")
	s.part("nvme0n1", "nvme0n1p2", "259:2")
	s.disk("sda", "8:0", "ST2000DM008", 3907029168, "1")
	s.part("sda", "sda1", "8:1")

	mountinfo := strings.Join([]string{
		"33 2 259:2 / / rw,relatime shared:1 - ext4 /dev/nvme0n1p2 rw",
		"125 33 259:1 / /boot/efi rw,relatime shared:94 - vfat /dev/nvme0n1p1 rw",
		"140 33 8:1 / /data rw,relatime - ext4 /dev/sda1 rw",
	}, "\n")

	got := physicalDisks(s.root, strings.NewReader(mountinfo), []string{"/", "/boot/efi", "/data"})
	want := []PhysicalDisk{
		{Name: "nvme0n1", Model: "CT500P2SSD8", SizeBytes: 976773168 * 512, Kind: "nvme", Mounts: []string{"/", "/boot/efi"}},
		{Name: "sda", Model: "ST2000DM008", SizeBytes: 3907029168 * 512, Kind: "hdd", Mounts: []string{"/data"}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("physicalDisks =\n %+v\nwant\n %+v", got, want)
	}
}

// LVM/mdraid: tek bir mount birden çok fiziksel diske düşer.
func TestPhysicalDisksFollowsSlavesAcrossMultipleDisks(t *testing.T) {
	s := newFakeSys(t)
	s.disk("sda", "8:0", "DiskA", 2000, "0")
	s.part("sda", "sda1", "8:1")
	s.disk("sdb", "8:16", "DiskB", 4000, "0")
	s.part("sdb", "sdb1", "8:17")
	s.virt("dm-0", "253:0", "sda1", "sdb1")

	mountinfo := "40 2 253:0 / /srv rw - xfs /dev/mapper/vg-data rw"
	got := physicalDisks(s.root, strings.NewReader(mountinfo), []string{"/srv"})
	if len(got) != 2 || got[0].Name != "sda" || got[1].Name != "sdb" ||
		!reflect.DeepEqual(got[0].Mounts, []string{"/srv"}) || !reflect.DeepEqual(got[1].Mounts, []string{"/srv"}) {
		t.Errorf("got %+v, want /srv under both sda and sdb", got)
	}
}

// btrfs'te mountinfo'nun major:minor'ı anonimdir (0:xx); disk /dev kaynağından bulunmalı.
func TestPhysicalDisksResolvesAnonymousDeviceThroughSource(t *testing.T) {
	s := newFakeSys(t)
	s.disk("nvme0n1", "259:0", "M", 1000, "0")
	s.part("nvme0n1", "nvme0n1p2", "259:2")

	// Kaynak "/dev/nvme0n1p2": test makinesinde bu yol yoksa da ad sysfs'te aranır.
	mountinfo := "50 2 0:45 /@ / rw - btrfs /dev/nvme0n1p2 rw,subvol=/@"
	got := physicalDisks(s.root, strings.NewReader(mountinfo), []string{"/"})
	if len(got) != 1 || got[0].Name != "nvme0n1" || !reflect.DeepEqual(got[0].Mounts, []string{"/"}) {
		t.Errorf("got %+v, want / on nvme0n1", got)
	}
}

// mount noktası olmayan bir yol (yapılandırmada istenen "/var/lib/x"), onu içeren dosya
// sisteminin diskine düşer; "/data" "/database"i kapsamaz.
func TestPhysicalDisksUsesLongestPathBoundedMountPrefix(t *testing.T) {
	s := newFakeSys(t)
	s.disk("sda", "8:0", "A", 1000, "1")
	s.part("sda", "sda1", "8:1")
	s.disk("sdb", "8:16", "B", 1000, "1")
	s.part("sdb", "sdb1", "8:17")

	mountinfo := strings.Join([]string{
		"1 0 8:1 / / rw - ext4 /dev/sda1 rw",
		"2 1 8:17 / /data rw - ext4 /dev/sdb1 rw",
	}, "\n")
	got := physicalDisks(s.root, strings.NewReader(mountinfo), []string{"/data/sub", "/database"})
	byName := map[string][]string{}
	for _, d := range got {
		byName[d.Name] = d.Mounts
	}
	if !reflect.DeepEqual(byName["sdb"], []string{"/data/sub"}) || !reflect.DeepEqual(byName["sda"], []string{"/database"}) {
		t.Errorf("got %+v", got)
	}
}

// Diski olmayan dosya sistemleri ve bellek/dosya destekli aygıtlar hiçbir diske düşmez ve
// altındaki dosya sisteminin (/) diskine yanlışlıkla atfedilmez.
func TestPhysicalDisksIgnoresVirtualAndRemoteFilesystems(t *testing.T) {
	s := newFakeSys(t)
	s.disk("sda", "8:0", "A", 1000, "1")
	s.part("sda", "sda1", "8:1")
	s.disk("loop0", "7:0", "", 100, "0")

	mountinfo := strings.Join([]string{
		"1 0 8:1 / / rw - ext4 /dev/sda1 rw",
		"2 1 0:30 / /run/user/1000 rw - tmpfs tmpfs rw",
		"3 1 0:40 / /mnt/nfs rw - nfs4 server:/export rw",
		"4 1 7:0 / /snap/core rw - squashfs /dev/loop0 ro",
	}, "\n")
	got := physicalDisks(s.root, strings.NewReader(mountinfo), []string{"/", "/run/user/1000", "/mnt/nfs", "/snap/core"})
	if len(got) != 1 || got[0].Name != "sda" || !reflect.DeepEqual(got[0].Mounts, []string{"/"}) {
		t.Errorf("got %+v, want only / on sda", got)
	}
}

func TestPhysicalDisksEmptyWhenNothingResolves(t *testing.T) {
	s := newFakeSys(t)
	got := physicalDisks(s.root, strings.NewReader("1 0 0:30 / / rw - overlay overlay rw"), []string{"/"})
	if got != nil {
		t.Errorf("got %+v, want nil", got)
	}
}

func TestPhysicalDisksSurvivesSlaveCycles(t *testing.T) {
	s := newFakeSys(t)
	s.virt("dm-0", "253:0", "dm-1")
	s.virt("dm-1", "253:1", "dm-0")
	got := physicalDisks(s.root, strings.NewReader("1 0 253:0 / / rw - ext4 /dev/dm-0 rw"), []string{"/"})
	if got != nil {
		t.Errorf("got %+v, want nil (no physical disk behind the cycle)", got)
	}
}

func TestDiskKind(t *testing.T) {
	for _, tc := range []struct{ name, rot, want string }{
		{"nvme0n1", "0", "nvme"}, {"nvme0n1", "1", "nvme"},
		{"sda", "1", "hdd"}, {"sda", "0", "ssd"}, {"vda", "", ""},
	} {
		if got := diskKind(tc.name, tc.rot); got != tc.want {
			t.Errorf("diskKind(%q,%q) = %q, want %q", tc.name, tc.rot, got, tc.want)
		}
	}
}

// Aynı noktaya sonradan yapılan mount öncekinin üstünü örter: mountinfo sırasıyla en sondaki
// gerçekte görünen dosya sistemidir.
func TestPhysicalDisksLaterMountOnSamePointWins(t *testing.T) {
	s := newFakeSys(t)
	s.disk("sda", "8:0", "A", 1000, "1")
	s.part("sda", "sda1", "8:1")
	s.disk("sdb", "8:16", "B", 1000, "1")
	s.part("sdb", "sdb1", "8:17")

	mountinfo := strings.Join([]string{
		"1 0 8:1 / /data rw - ext4 /dev/sda1 rw",
		"2 1 8:17 / /data rw - ext4 /dev/sdb1 rw", // üstüne mount edildi
	}, "\n")
	got := physicalDisks(s.root, strings.NewReader(mountinfo), []string{"/data"})
	if len(got) != 1 || got[0].Name != "sdb" {
		t.Errorf("got %+v, want /data on sdb (the later mount)", got)
	}
}

// İki slave aynı fiziksel diskin bölümleriyse disk bir kez sayılmalı.
func TestDisksOfCountsSharedDiskOnce(t *testing.T) {
	s := newFakeSys(t)
	s.disk("sda", "8:0", "A", 1000, "1")
	s.part("sda", "sda1", "8:1")
	s.part("sda", "sda2", "8:2")
	s.virt("dm-0", "253:0", "sda1", "sda2")

	got := disksOf(s.root, mountDevice{point: "/", majMin: "253:0", source: "/dev/mapper/x"})
	if !reflect.DeepEqual(got, []string{"sda"}) {
		t.Errorf("disksOf = %v, want [sda] exactly once", got)
	}
}

// Kabul edilebilir bir zincir (LUKS içinde LVM) çözülür; sınırı aşan bir zincir sonsuza kadar
// (ya da devasa bir maliyetle) inmez.
func TestPhysicalDisksBoundsNestedDeviceChains(t *testing.T) {
	build := func(depth int) *fakeSys {
		s := newFakeSys(t)
		s.disk("sda", "8:0", "A", 1000, "1")
		s.part("sda", "sda1", "8:1")
		below := "sda1"
		for i := 0; i < depth; i++ {
			name := "dm-" + strconv.Itoa(i)
			s.virt(name, "253:"+strconv.Itoa(i), below)
			below = name
		}
		return s
	}
	top := func(depth int) string {
		return "1 0 253:" + strconv.Itoa(depth-1) + " / / rw - ext4 /dev/dm-" + strconv.Itoa(depth-1) + " rw"
	}

	if got := physicalDisks(build(3).root, strings.NewReader(top(3)), []string{"/"}); len(got) != 1 || got[0].Name != "sda" {
		t.Errorf("3-deep chain = %+v, want sda", got)
	}
	deep := maxSlaveDepth + 4
	if got := physicalDisks(build(deep).root, strings.NewReader(top(deep)), []string{"/"}); got != nil {
		t.Errorf("%d-deep chain = %+v, want nil (bounded)", deep, got)
	}
}
