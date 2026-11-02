// Package agentpkg, server'a yüklenen agent paket dosyalarını diskte saklar ve adlandırır. Paketler .deb, .rpm ve tarball;
// amd64 ve arm64'tür. Sürüm ve mimari yükleyen tarafından verilir, biçim dosyanın ilk baytlarından anlaşılır. Dosya
// diskte ve indirme adresinde release'teki adıyla durur (scripts/release.sh, agent/packaging/build.sh): FileName üretir,
// Parse geri okur. Kayıtlar veritabanındadır (store.AgentPackages).
package agentpkg

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"healthbeat-server/internal/model"
)

// Dosya adı kalıpları. Önsürüm (1.2.0-rc.1) nfpm paketlerinde '~' ile yazılır (1.2.0~rc.1), tarball'da olduğu gibi.
var (
	debPattern     = regexp.MustCompile(`^healthbeat-agent_(\d+\.\d+\.\d+(?:~[0-9A-Za-z.]+)?)_(amd64|arm64)\.deb$`)
	rpmPattern     = regexp.MustCompile(`^healthbeat-agent-(\d+\.\d+\.\d+(?:~[0-9A-Za-z.]+)?)-1\.(x86_64|aarch64)\.rpm$`)
	tarballPattern = regexp.MustCompile(`^healthbeat-agent_(\d+\.\d+\.\d+(?:-[0-9A-Za-z.]+)?)_linux_(amd64|arm64)\.tar\.gz$`)
)

var (
	rpmArch      = map[string]string{"x86_64": "amd64", "aarch64": "arm64"}
	rpmArchName  = map[string]string{"amd64": "x86_64", "arm64": "aarch64"}
	versionRegex = regexp.MustCompile(`^\d+\.\d+\.\d+(?:-[0-9A-Za-z.]+)?$`)
)

// maxVersionLen, sürümün üst sınırıdır (agent_packages_version_chk).
const maxVersionLen = 64

// ValidVersion, sürümün kabul edilen SemVer biçiminde olup olmadığıdır: 1.2.0 ya da 1.2.0-rc.1 (release.sh'ın kalıbı).
func ValidVersion(v string) bool { return len(v) <= maxVersionLen && versionRegex.MatchString(v) }

// ValidArch, mimarinin desteklenip desteklenmediğidir.
func ValidArch(a string) bool { return a == "amd64" || a == "arm64" }

// ErrInvalidInfo, sürüm, mimari ya da biçimin geçersiz olduğunu söyler.
var ErrInvalidInfo = errors.New("invalid agent package version, arch or format")

// FileName, paketin release'teki adıdır: diskte ve indirme adresinde bu ad kullanılır. Önsürüm .deb ve .rpm'de '~'
// ile yazılır (1.2.0~rc.1).
func FileName(info Info) (string, error) {
	if !ValidVersion(info.Version) || !ValidArch(info.Arch) {
		return "", ErrInvalidInfo
	}
	pkgVersion := strings.Replace(info.Version, "-", "~", 1)
	switch info.Format {
	case model.AgentPackageDeb:
		return fmt.Sprintf("healthbeat-agent_%s_%s.deb", pkgVersion, info.Arch), nil
	case model.AgentPackageRPM:
		return fmt.Sprintf("healthbeat-agent-%s-1.%s.rpm", pkgVersion, rpmArchName[info.Arch]), nil
	case model.AgentPackageTarball:
		return fmt.Sprintf("healthbeat-agent_%s_linux_%s.tar.gz", info.Version, info.Arch), nil
	}
	return "", ErrInvalidInfo
}

// Dosya başlıkları: .deb bir ar arşividir, .rpm'in kendi imzası vardır, tarball gzip'tir.
var formatMagic = []struct {
	format string
	magic  []byte
}{
	{model.AgentPackageDeb, []byte("!<arch>\n")},
	{model.AgentPackageRPM, []byte{0xed, 0xab, 0xee, 0xdb}},
	{model.AgentPackageTarball, []byte{0x1f, 0x8b}},
}

// headerLen, biçimi tanımak için okunan bayt sayısıdır.
const headerLen = 8

// DetectFormat, dosyanın ilk baytlarından biçimini bulur; tanınmazsa "".
func DetectFormat(header []byte) string {
	for _, f := range formatMagic {
		if bytes.HasPrefix(header, f.magic) {
			return f.format
		}
	}
	return ""
}

// ErrUnknownFilename, dosya adının bir agent paketi olmadığını söyler.
var ErrUnknownFilename = errors.New("not an agent package filename")

// Info, paketin sürümü, mimarisi ve biçimidir.
type Info struct {
	Version string // SemVer: 1.2.0 ya da 1.2.0-rc.1
	Arch    string // amd64 | arm64
	Format  string // deb | rpm | tar.gz
}

// Parse, release adını tanır (FileName'in tersi); indirme ucu adresteki adı böyle denetler. Ad, yol ayırıcısı içeremez
// (kalıplar yalnızca bilinen karakterlere izin verir).
func Parse(filename string) (Info, error) {
	if m := debPattern.FindStringSubmatch(filename); m != nil {
		return Info{Version: strings.Replace(m[1], "~", "-", 1), Arch: m[2], Format: model.AgentPackageDeb}, nil
	}
	if m := rpmPattern.FindStringSubmatch(filename); m != nil {
		return Info{Version: strings.Replace(m[1], "~", "-", 1), Arch: rpmArch[m[2]], Format: model.AgentPackageRPM}, nil
	}
	if m := tarballPattern.FindStringSubmatch(filename); m != nil {
		return Info{Version: m[1], Arch: m[2], Format: model.AgentPackageTarball}, nil
	}
	return Info{}, ErrUnknownFilename
}

// ErrTooLarge, yüklenen dosyanın üst sınırı aştığını; ErrEmpty boş olduğunu; ErrUnknownFormat .deb, .rpm ya da
// tarball olmadığını söyler.
var (
	ErrTooLarge      = errors.New("agent package exceeds the size limit")
	ErrEmpty         = errors.New("agent package is empty")
	ErrUnknownFormat = errors.New("not a .deb, .rpm or .tar.gz file")
)

// Dir, paket dosyalarının dizinidir: <kök>/<sürüm>/<dosya adı>. Dizinler 0750, dosyalar 0640 açılır.
type Dir struct {
	root string
}

// NewDir, kök dizini hazırlar (yoksa oluşturur) ve yazılabilir olduğunu denetler.
func NewDir(root string) (*Dir, error) {
	if err := os.MkdirAll(root, 0o750); err != nil {
		return nil, err
	}
	probe, err := os.CreateTemp(root, ".probe-*")
	if err != nil {
		return nil, fmt.Errorf("agent packages directory is not writable: %w", err)
	}
	name := probe.Name()
	_ = probe.Close()
	_ = os.Remove(name)
	return &Dir{root: root}, nil
}

// Root, kök dizindir.
func (d *Dir) Root() string { return d.root }

func (d *Dir) path(version, filename string) string {
	return filepath.Join(d.root, filepath.Base(version), filepath.Base(filename))
}

// Staged, diske yazılmış ama henüz yerine konmamış bir yüklemedir. Place ya da Discard ile kapatılır.
type Staged struct {
	tmp    string
	root   string
	Size   int64
	SHA256 string
	Format string // ilk baytlardan: deb | rpm | tar.gz
}

// headWriter, kendisine yazılanın ilk headerLen baytını tutar.
type headWriter struct{ buf []byte }

func (h *headWriter) Write(p []byte) (int, error) {
	if n := headerLen - len(h.buf); n > 0 {
		h.buf = append(h.buf, p[:min(n, len(p))]...)
	}
	return len(p), nil
}

// Stage, r'yi kök dizinde geçici bir dosyaya yazar; boyutunu, SHA256'sını ve biçimini bulur. maxBytes'ı aşan, boş ya
// da biçimi tanınmayan gövde reddedilir (geçici dosya silinir).
func (d *Dir) Stage(r io.Reader, maxBytes int64) (*Staged, error) {
	f, err := os.CreateTemp(d.root, ".upload-*")
	if err != nil {
		return nil, err
	}
	s := &Staged{tmp: f.Name(), root: d.root}
	h := sha256.New()
	head := &headWriter{}
	n, err := io.Copy(io.MultiWriter(f, h, head), io.LimitReader(r, maxBytes+1))
	if err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	switch {
	case err != nil:
	case n > maxBytes:
		err = ErrTooLarge
	case n == 0:
		err = ErrEmpty
	default:
		if s.Format = DetectFormat(head.buf); s.Format == "" {
			err = ErrUnknownFormat
		}
	}
	if err != nil {
		s.Discard()
		return nil, err
	}
	s.Size, s.SHA256 = n, hex.EncodeToString(h.Sum(nil))
	return s, nil
}

// Place, geçici dosyayı <sürüm>/<dosya adı> olarak yerine koyar (aynı adlı bir dosya varsa üzerine yazar: kaydı
// olmayan, yarım kalmış bir yüklemeden kalmıştır — kayıt tekliğini veritabanı sağlar).
func (s *Staged) Place(version, filename string) error {
	d := Dir{root: s.root}
	final := d.path(version, filename)
	if err := os.MkdirAll(filepath.Dir(final), 0o750); err != nil {
		return err
	}
	if err := os.Chmod(s.tmp, 0o640); err != nil {
		return err
	}
	return os.Rename(s.tmp, final)
}

// Discard, geçici dosyayı siler (Place'ten sonra bir şey yapmaz).
func (s *Staged) Discard() { _ = os.Remove(s.tmp) }

// Open, paket dosyasını okumak için açar.
func (d *Dir) Open(version, filename string) (*os.File, error) {
	return os.Open(d.path(version, filename))
}

// Remove, paket dosyasını siler; dosya zaten yoksa hata değildir. Sürüm dizini boşaldıysa o da silinir.
func (d *Dir) Remove(version, filename string) error {
	p := d.path(version, filename)
	if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	_ = os.Remove(filepath.Dir(p)) // boş değilse başarısız olur; sorun değil
	return nil
}
