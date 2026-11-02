// Package agentpkg, server'a yüklenen agent paket dosyalarını tanır ve diskte saklar. Kabul edilen dosyalar release'in
// paketleridir (scripts/release.sh, agent/packaging/build.sh): .deb, .rpm ve tarball; amd64 ve arm64. Sürüm, mimari ve
// biçim dosya adından okunur; kalıba uymayan ad reddedilir. Kayıtlar veritabanındadır (store.AgentPackages).
package agentpkg

import (
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

var rpmArch = map[string]string{"x86_64": "amd64", "aarch64": "arm64"}

// ErrUnknownFilename, dosya adının bir agent paketi olmadığını söyler.
var ErrUnknownFilename = errors.New("not an agent package filename")

// Info, dosya adından okunan paket bilgisidir.
type Info struct {
	Version string // SemVer: 1.2.0 ya da 1.2.0-rc.1
	Arch    string // amd64 | arm64
	Format  string // deb | rpm | tar.gz
}

// Parse, dosya adını tanır. Ad, yol ayırıcısı içeremez (kalıplar yalnızca bilinen karakterlere izin verir).
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

// ErrTooLarge, yüklenen dosyanın üst sınırı aştığını; ErrEmpty boş olduğunu söyler.
var (
	ErrTooLarge = errors.New("agent package exceeds the size limit")
	ErrEmpty    = errors.New("agent package is empty")
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
}

// Stage, r'yi kök dizinde geçici bir dosyaya yazar ve boyutunu ve SHA256'sını hesaplar. maxBytes'ı aşan ya da boş
// gövde reddedilir (geçici dosya silinir).
func (d *Dir) Stage(r io.Reader, maxBytes int64) (*Staged, error) {
	f, err := os.CreateTemp(d.root, ".upload-*")
	if err != nil {
		return nil, err
	}
	s := &Staged{tmp: f.Name(), root: d.root}
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(f, h), io.LimitReader(r, maxBytes+1))
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
