package collector

import (
	"errors"
	"reflect"
	"sort"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

// Gerçek bir sunucunun ürettiği biçimde kısaltılmış, anonimleştirilmiş /proc/self/mountinfo ve
// zor durumlar: boşluklu bir yol (\040), zaten mount edilmiş bir aygıtın bind mount'u, NFS ve
// btrfs alt hacimleri (anonim 0:N aygıtlar) ve hatalı biçimli bir satır.
const sampleMountinfo = `22 28 0:21 / /sys rw,nosuid,nodev,noexec,relatime shared:7 - sysfs sysfs rw
23 28 0:22 / /proc rw,nosuid,nodev,noexec,relatime shared:14 - proc proc rw
24 28 0:5 / /dev rw,nosuid,relatime shared:2 - devtmpfs udev rw,size=8000k
25 24 0:23 / /dev/pts rw,nosuid,noexec,relatime shared:3 - devpts devpts rw,gid=5,mode=620
26 28 0:24 / /run rw,nosuid,nodev,noexec,relatime shared:5 - tmpfs tmpfs rw,size=1600k
28 1 259:2 / / rw,relatime shared:1 - ext4 /dev/nvme0n1p2 rw,errors=remount-ro
29 22 0:6 / /sys/kernel/security rw,nosuid,nodev,noexec,relatime shared:8 - securityfs securityfs rw
31 22 0:26 / /sys/fs/cgroup rw,nosuid,nodev,noexec,relatime shared:9 - cgroup2 cgroup2 rw
40 28 7:0 / /snap/core22/2292 ro,nodev,relatime shared:60 - squashfs /dev/loop0 ro,errors=continue
41 28 7:1 / /snap/firefox/8863 ro,nodev,relatime shared:61 - squashfs /dev/loop1 ro,errors=continue
50 28 259:1 / /boot/efi rw,relatime shared:70 - vfat /dev/nvme0n1p1 rw,fmask=0077,dmask=0077
51 28 1:1 / /run/user/1000/doc rw,nosuid,nodev,relatime shared:80 - fuse.portal portal rw,user_id=1000
52 28 1:2 / /run/user/1000/gvfs rw,nosuid,nodev,relatime shared:81 - fuse.gvfsd-fuse gvfsd-fuse rw,user_id=1000
53 28 8:17 / /data rw,relatime shared:90 - xfs /dev/sdb1 rw,attr2,inode64
54 28 8:17 /media /srv/bind-of-data rw,relatime shared:90 - xfs /dev/sdb1 rw,attr2,inode64
55 28 8:33 / /mnt/My\040Disk rw,relatime shared:91 - ext4 /dev/sdc1 rw
56 28 0:60 / /mnt/nfs/share rw,relatime shared:92 - nfs4 10.0.0.9:/export rw,vers=4.2
57 28 0:61 / /mnt/nfs/other rw,relatime shared:93 - nfs4 10.0.0.9:/other rw,vers=4.2
58 28 0:70 / /pool rw,relatime shared:94 - btrfs /dev/sdd1 rw,subvolid=5
59 28 0:71 /home /pool/home rw,relatime shared:95 - btrfs /dev/sdd1 rw,subvolid=256,subvol=/home
60 28 0:80 / /var/lib/docker/overlay2/abc/merged rw,relatime shared:96 - overlay overlay rw,lowerdir=x
61 28 0:81 / /mnt/cdrom ro,relatime shared:97 - iso9660 /dev/sr0 ro
62 28 0:82 / /dev/shm rw,nosuid,nodev shared:98 - tmpfs tmpfs rw
this line is garbage
63 28 0:90 / relative/path rw,relatime shared:99 - ext4 /dev/sde1 rw
`

func TestParseMountinfoKeepsOnlyRealFilesystems(t *testing.T) {
	got := parseMountinfo(strings.NewReader(sampleMountinfo))
	want := []string{
		"/", "/boot/efi", "/data", "/mnt/My Disk", "/mnt/nfs/other", "/mnt/nfs/share", "/pool", "/pool/home",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("mounts = %q\nwant     %q", got, want)
	}
}

func TestParseMountinfoIsRobust(t *testing.T) {
	for name, in := range map[string]string{
		"empty":               "",
		"only garbage":        "nonsense\n\n- - -\n",
		"no separator":        "28 1 259:2 / / rw shared:1 ext4 /dev/x rw\n",
		"truncated after sep": "28 1 259:2 / / rw shared:1 -\n",
		"too few fields":      "28 1 - ext4 x\n",
	} {
		if got := parseMountinfo(strings.NewReader(in)); len(got) != 0 {
			t.Errorf("%s: got %q, want nothing", name, got)
		}
	}
	// Hatalı biçimli tek bir satır sağlam olanları gizlememeli.
	got := parseMountinfo(strings.NewReader("garbage\n28 1 259:2 / / rw shared:1 - ext4 /dev/x rw\n"))
	if !reflect.DeepEqual(got, []string{"/"}) {
		t.Errorf("good line after garbage: %q", got)
	}
}

// Aynı aygıtın iki yerde mount edilmesi tek disk için iki kez alert üretirdi.
func TestBindMountsOfOneDeviceAreReportedOnce(t *testing.T) {
	got := parseMountinfo(strings.NewReader(`1 0 8:17 / /data rw - xfs /dev/sdb1 rw
2 0 8:17 /media /srv/bind rw - xfs /dev/sdb1 rw
3 0 8:17 /other /a rw - xfs /dev/sdb1 rw
`))
	if !reflect.DeepEqual(got, []string{"/a"}) && !reflect.DeepEqual(got, []string{"/data"}) {
		t.Fatalf("got %q", got)
	}
	if len(got) != 1 || got[0] != "/a" { // "/a" en kısa yoldur
		t.Fatalf("got %q, want the shortest mount point of the device, once", got)
	}
}

func TestUnescapeMountinfo(t *testing.T) {
	for in, want := range map[string]string{
		`/plain`:            "/plain",
		`/mnt/My\040Disk`:   "/mnt/My Disk",
		`/tab\011here`:      "/tab\there",
		`/back\134slash`:    `/back\slash`,
		`/nl\012x`:          "/nl\nx",
		`/trailing\`:        `/trailing\`,
		`/short\04`:         `/short\04`,
		`/notoctal\9zz/end`: `/notoctal\9zz/end`,
	} {
		if got := unescapeMountinfo(in); got != want {
			t.Errorf("unescapeMountinfo(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestExpandMounts(t *testing.T) {
	found := func() ([]string, error) { return []string{"/", "/data", "/boot/efi"}, nil }
	boom := func() ([]string, error) { return nil, errors.New("no /proc") }

	if got := expandMounts([]string{"/", "/var"}, found); !reflect.DeepEqual(got, []string{"/", "/var"}) {
		t.Errorf("without auto the list must be untouched, got %q", got)
	}
	if got := expandMounts([]string{"auto"}, found); !reflect.DeepEqual(got, []string{"/", "/data", "/boot/efi"}) {
		t.Errorf("auto alone: %q", got)
	}
	// Açık ekstralar kalır (ör. keşfin listelemeyeceği bir yol) ve tekrarlar tek olur.
	if got := expandMounts([]string{"/mnt/extra", "auto", "/data"}, found); !reflect.DeepEqual(got, []string{"/mnt/extra", "/", "/data", "/boot/efi"}) {
		t.Errorf("auto with extras: %q", got)
	}
	// Mount tablosu okunamıyor: açık girdiler yine çalışır.
	if got := expandMounts([]string{"/x", "auto"}, boom); !reflect.DeepEqual(got, []string{"/x"}) {
		t.Errorf("discovery failure: %q", got)
	}
}

func TestDiscoverMountsOnThisHost(t *testing.T) {
	mounts, err := DiscoverMounts()
	if err != nil {
		t.Skipf("no /proc/self/mountinfo: %v", err)
	}
	seen := map[string]bool{}
	for _, m := range mounts {
		if !strings.HasPrefix(m, "/") || seen[m] {
			t.Errorf("bad or duplicate mount %q in %q", m, mounts)
		}
		seen[m] = true
		for _, virtual := range []string{"/proc", "/sys", "/dev", "/run", "/snap"} {
			if m == virtual || strings.HasPrefix(m, virtual+"/") {
				t.Errorf("virtual mount %q was discovered as a disk", m)
			}
		}
	}
	if len(mounts) == 0 {
		t.Fatal("found no mounts at all; every host has at least /")
	}
	t.Logf("discovered on this host: %q", mounts)
}

func TestSampleDiskAutoReportsRealMounts(t *testing.T) {
	if _, err := DiscoverMounts(); err != nil {
		t.Skip("no /proc/self/mountinfo")
	}
	disks := SampleDisk([]string{"auto"})
	if len(disks) == 0 {
		t.Fatal("auto reported no disks")
	}
	for _, d := range disks {
		if d.Total <= 0 || d.UsedPct < 0 || d.UsedPct > 100 || d.Free < 0 || d.Free > d.Total {
			t.Errorf("implausible usage for %s: %+v", d.Mount, d)
		}
	}
}

// Takılan bir ağ mount'u en fazla bir zaman aşımına mal olmalı — toplamayı dondurmamalı ve
// her döngüde takılı bir goroutine biriktirmemeli.
func TestHungMountDoesNotFreezeCollection(t *testing.T) {
	origFn, origTimeout := statfsFn, statfsTimeout
	defer func() { statfsFn, statfsTimeout = origFn, origTimeout }()
	statfsTimeout = 150 * time.Millisecond

	release := make(chan struct{})
	var hungCalls atomic.Int32
	statfsFn = func(path string, st *syscall.Statfs_t) error {
		if path == "/hung" {
			hungCalls.Add(1)
			<-release
			return nil
		}
		st.Blocks, st.Bavail, st.Bsize = 1000, 250, 4096
		return nil
	}

	start := time.Now()
	got := SampleDisk([]string{"/ok", "/hung", "/ok2"})
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("collection took %v with one hung mount", elapsed)
	}
	if len(got) != 2 || got[0].Mount != "/ok" || got[1].Mount != "/ok2" {
		t.Fatalf("got %+v, want the two responsive mounts, in order", got)
	}
	if got[0].UsedPct != 75 {
		t.Fatalf("used%% = %v, want 75", got[0].UsedPct)
	}

	// İlk çağrı hâlâ takılıyken sonraki döngüler yeni bir bloklu çağrı başlatmamalı.
	for i := 0; i < 3; i++ {
		SampleDisk([]string{"/hung"})
	}
	if n := hungCalls.Load(); n != 1 {
		t.Fatalf("statfs was called %d times on the hung mount, want 1 (no pile-up)", n)
	}

	// Yeniden yanıt vermeye başlayınca mount normal şekilde raporlanır.
	close(release)
	time.Sleep(50 * time.Millisecond)
	if got := SampleDisk([]string{"/hung"}); len(got) != 1 {
		t.Fatalf("mount not reported after it recovered: %+v", got)
	}
}

// fakeFilesystems, mount -> fsid numarası (0 = Fsid bildirmeyen dosya sistemi) olan bir statfs
// taklidi kurar.
func fakeFilesystems(t *testing.T, fsids map[string]int32) {
	t.Helper()
	orig := statfsFn
	t.Cleanup(func() { statfsFn = orig })
	statfsFn = func(path string, st *syscall.Statfs_t) error {
		id, ok := fsids[path]
		if !ok {
			return syscall.ENOENT
		}
		st.Blocks, st.Bavail, st.Bsize = 1000, 250, 4096
		st.Fsid = syscall.Fsid{X__val: [2]int32{id, id}}
		return nil
	}
}

func mountNames(disks []DiskUsage) []string {
	var out []string
	for _, d := range disks {
		out = append(out, d.Mount)
	}
	sort.Strings(out)
	return out
}

// btrfs alt hacimleri ("/" ve "/home") ayrı mount ama aynı dosya sistemidir: "auto" ikisini de
// bulur, agent tek doluluk raporlar.
func TestAutoDiscoveredSubvolumesOfOneFilesystemAreReportedOnce(t *testing.T) {
	fakeFilesystems(t, map[string]int32{"/": 7, "/home": 7, "/var/log": 7, "/data": 9})
	discover := func() ([]string, error) { return []string{"/home", "/data", "/var/log", "/"}, nil }

	got := mountNames(sampleDisk([]string{"auto"}, discover))
	if !reflect.DeepEqual(got, []string{"/", "/data"}) {
		t.Fatalf("mounts = %v, want [/ /data]: /home and /var/log are subvolumes of the filesystem of /", got)
	}
}

// Adıyla istenen bir mount asla elenmez; "auto"nun aynı dosya sisteminden bulduğu kopya elenir.
func TestExplicitMountsAreNeverDroppedAsDuplicates(t *testing.T) {
	fakeFilesystems(t, map[string]int32{"/": 7, "/home": 7, "/data": 9})
	discover := func() ([]string, error) { return []string{"/", "/home", "/data"}, nil }

	// İki alt hacim de adıyla istendi: ikisi de raporlanır.
	if got := mountNames(sampleDisk([]string{"/", "/home"}, discover)); !reflect.DeepEqual(got, []string{"/", "/home"}) {
		t.Fatalf("both named subvolumes must be reported, got %v", got)
	}
	// "/home" adıyla istendi, "auto" "/" ve "/data"yı da buldu: "/" aynı dosya sistemi olduğundan
	// (adıyla istenen "/home" varken) kopya sayılır.
	if got := mountNames(sampleDisk([]string{"/home", "auto"}, discover)); !reflect.DeepEqual(got, []string{"/data", "/home"}) {
		t.Fatalf("got %v, want [/data /home]: the named /home wins over the auto-found /", got)
	}
	// Hem "auto" hem adıyla yazılmış: adıyla yazılan sayılır, elenmez.
	if got := mountNames(sampleDisk([]string{"auto", "/home"}, discover)); !reflect.DeepEqual(got, []string{"/data", "/home"}) {
		t.Fatalf("got %v, want [/data /home]", got)
	}
}

// Fsid'i olmayan (0) dosya sistemleri birbirine benzetilmez: iki ayrı NFS mount'u iki ayrı disktir.
func TestFilesystemsWithoutAnFsidAreNeverMerged(t *testing.T) {
	fakeFilesystems(t, map[string]int32{"/mnt/a": 0, "/mnt/b": 0, "/": 7})
	discover := func() ([]string, error) { return []string{"/", "/mnt/a", "/mnt/b"}, nil }
	if got := mountNames(sampleDisk([]string{"auto"}, discover)); !reflect.DeepEqual(got, []string{"/", "/mnt/a", "/mnt/b"}) {
		t.Fatalf("got %v, want all three", got)
	}
}

// Sonuç, mount'ların bulunma sırasına bağlı olmamalı (en kısa yol tutulur).
func TestSubvolumeChoiceDoesNotDependOnDiscoveryOrder(t *testing.T) {
	fakeFilesystems(t, map[string]int32{"/": 7, "/home": 7, "/srv": 7})
	for _, order := range [][]string{{"/", "/home", "/srv"}, {"/srv", "/home", "/"}, {"/home", "/", "/srv"}} {
		order := order
		got := mountNames(sampleDisk([]string{"auto"}, func() ([]string, error) { return order, nil }))
		if !reflect.DeepEqual(got, []string{"/"}) {
			t.Errorf("order %v: got %v, want [/]", order, got)
		}
	}
}
