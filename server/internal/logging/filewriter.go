package logging

import (
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// FileOptions, kalıcı log dosyasını yapılandırır (bkz. OpenFile).
type FileOptions struct {
	// Path, örn. /var/log/healthbeat/server.log: dosyalar o dizinde <ad>-YYYY-MM-DD.log olarak tutulur.
	Path string
	// MaxAgeDays, bugün dahil kaç günlük dosyanın tutulacağıdır; daha eskiler silinir.
	MaxAgeDays int
	// MaxTotalBytes, bütün log dosyalarının (sıkıştırılmışlar dahil) toplam üst sınırıdır; aşılırsa en eskiler silinir.
	// Bir hata fırtınasında diski korur: o durumda MaxAgeDays'ten daha kısa bir geçmiş kalır.
	MaxTotalBytes int64

	// now testlerde saati sabitler; nil ise time.Now.
	now func() time.Time
	// maxFileBytes, bir günün dosyasının bölündüğü boyuttur; sıfırsa MaxTotalBytes'tan türetilir.
	maxFileBytes int64
	// errOut, dosyanın kendi sorunlarının yazıldığı yerdir (log dosyası bozukken slog'a yazılamaz); nil ise os.Stderr.
	errOut io.Writer
}

// FileWriter, log satırlarını günlük dosyalara yazar: gün değişince yeni dosya açılır ve önceki gzip'lenir; bir günün
// dosyası çok büyürse aynı gün <ad>-YYYY-MM-DD.N.log olarak bölünür. Saklama sınırları her dosya değişiminde ve açılışta
// uygulanır. Yalnızca kendi adlandırmasına uyan dosyalara dokunur.
//
// Yazma hatası (disk dolu, izin yok) çağırana dönmez: log dosyası yüzünden server ya da stdout logu durmamalı. Hata bir
// kez stderr'e yazılır; yazma yeniden başarılı olunca bu da stderr'e bildirilir.
type FileWriter struct {
	opts    FileOptions
	dir     string
	prefix  string
	pattern *regexp.Regexp

	mu      sync.Mutex
	f       *os.File
	day     string
	index   int
	size    int64
	failing bool
	// pending, sıkıştırılması süren dosyalardır; silme onlara dokunmaz.
	pending map[string]bool

	compressing sync.WaitGroup
}

// OpenFile, dizini gerekirse oluşturur, bugünün dosyasını (varsa sonuna ekleyerek) açar, geride kalmış sıkıştırılmamış
// dosyaları sıkıştırır ve saklama sınırlarını uygular.
func OpenFile(opts FileOptions) (*FileWriter, error) {
	if opts.MaxAgeDays < 1 || opts.MaxTotalBytes < 1 {
		return nil, errors.New("log file: MaxAgeDays and MaxTotalBytes must be positive")
	}
	if opts.now == nil {
		opts.now = time.Now
	}
	if opts.errOut == nil {
		opts.errOut = os.Stderr
	}
	if opts.maxFileBytes == 0 {
		// Toplam sınırın dörtte biri, en fazla 100 MB: tek bir gün bütün bütçeyi tek dosyada tüketmez ve silme
		// işlemi dosya dosya ilerleyebilir.
		opts.maxFileBytes = min(opts.MaxTotalBytes/4, 100<<20)
		if opts.maxFileBytes < 1 {
			opts.maxFileBytes = 1
		}
	}
	if strings.HasSuffix(opts.Path, "/") || strings.HasSuffix(opts.Path, string(filepath.Separator)) {
		return nil, fmt.Errorf("log file: %q is a directory; give a file name such as server.log", opts.Path)
	}
	if info, err := os.Stat(opts.Path); err == nil && info.IsDir() {
		return nil, fmt.Errorf("log file: %q is a directory; give a file name such as server.log", opts.Path)
	}
	dir := filepath.Dir(opts.Path)
	prefix := strings.TrimSuffix(filepath.Base(opts.Path), filepath.Ext(opts.Path))
	if prefix == "" || prefix == "." {
		return nil, fmt.Errorf("log file: %q has no file name", opts.Path)
	}
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, fmt.Errorf("log file: %w", err)
	}
	w := &FileWriter{
		opts:    opts,
		dir:     dir,
		prefix:  prefix,
		pattern: regexp.MustCompile(`^` + regexp.QuoteMeta(prefix) + `-(\d{4}-\d{2}-\d{2})(?:\.(\d+))?\.log(\.gz)?$`),
		pending: map[string]bool{},
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if err := w.openLocked(w.today(), 0); err != nil {
		return nil, err
	}
	w.compressLeftoversLocked()
	w.pruneLocked()
	return w, nil
}

// Write, p'yi bugünün dosyasına yazar; gerekirse önce dosyayı değiştirir. Her zaman len(p), nil döner (bkz. FileWriter).
func (w *FileWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	day := w.today()
	full := w.f != nil && day == w.day && w.size > 0 && w.size+int64(len(p)) > w.opts.maxFileBytes
	if w.f == nil || day != w.day || full {
		minIndex := 0
		if full {
			minIndex = w.index + 1
		}
		if err := w.rotateLocked(day, minIndex); err != nil {
			w.reportLocked(err)
			return len(p), nil
		}
	}
	n, err := w.f.Write(p)
	w.size += int64(n)
	if err != nil {
		w.reportLocked(err)
		return len(p), nil
	}
	if w.failing {
		w.failing = false
		fmt.Fprintf(w.opts.errOut, "log file: writing to %s again\n", w.f.Name())
	}
	return len(p), nil
}

// Close, dosyayı kapatır ve süren sıkıştırmaları bekler.
func (w *FileWriter) Close() error {
	w.mu.Lock()
	var err error
	if w.f != nil {
		err = w.f.Close()
		w.f = nil
	}
	w.mu.Unlock()
	w.compressing.Wait()
	return err
}

func (w *FileWriter) today() string { return w.opts.now().Format(time.DateOnly) }

func (w *FileWriter) reportLocked(err error) {
	if !w.failing {
		w.failing = true
		fmt.Fprintf(w.opts.errOut, "log file: %v (logging continues on stdout only until the file is writable again)\n", err)
	}
}

func (w *FileWriter) name(day string, index int) string {
	if index == 0 {
		return filepath.Join(w.dir, fmt.Sprintf("%s-%s.log", w.prefix, day))
	}
	return filepath.Join(w.dir, fmt.Sprintf("%s-%s.%d.log", w.prefix, day, index))
}

// openLocked, day'in en son parçasını açar: dolmamışsa sonuna eklenir (yeniden başlatma aynı günün dosyasına devam
// eder), dolmuşsa ya da sıkıştırılmışsa bir sonraki parça açılır. minIndex, boyuttan bölmede bir sonraki parçayı zorlar.
func (w *FileWriter) openLocked(day string, minIndex int) error {
	index := 0
	for _, lf := range w.listLocked() {
		if lf.day == day && lf.index >= index {
			index = lf.index
			if lf.gz || lf.size >= w.opts.maxFileBytes {
				index = lf.index + 1
			}
		}
	}
	index = max(index, minIndex)
	f, err := os.OpenFile(w.name(day, index), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o640)
	if err != nil {
		return fmt.Errorf("log file: %w", err)
	}
	info, err := f.Stat()
	if err != nil {
		f.Close()
		return fmt.Errorf("log file: %w", err)
	}
	w.f, w.day, w.index, w.size = f, day, index, info.Size()
	return nil
}

func (w *FileWriter) rotateLocked(day string, minIndex int) error {
	var prev string
	if w.f != nil {
		prev = w.f.Name()
		w.f.Close()
		w.f = nil
	}
	err := w.openLocked(day, minIndex)
	if prev != "" && (err != nil || prev != w.f.Name()) {
		w.compressAsyncLocked(prev)
	}
	w.pruneLocked()
	return err
}

// compressAsyncLocked, bitmiş bir dosyayı arka planda gzip'ler; yazmayı bekletmez. Sıkıştırılmış hali tamamlanmadan asıl
// dosya silinmez: yarıda kalırsa bir sonraki açılışta yeniden denenir.
func (w *FileWriter) compressAsyncLocked(path string) {
	if w.pending[path] {
		return
	}
	w.pending[path] = true
	w.compressing.Add(1)
	go func() {
		defer w.compressing.Done()
		err := gzipFile(path)
		if err != nil {
			fmt.Fprintf(w.opts.errOut, "log file: compress %s: %v\n", path, err)
		}
		w.mu.Lock()
		delete(w.pending, path)
		if err == nil {
			// Sıkıştırma toplam boyutu düşürdü ya da süresi dolmuş bir günü bitirdi: sınırları yeniden uygula.
			w.pruneLocked()
		}
		w.mu.Unlock()
	}()
}

func gzipFile(path string) error {
	in, err := os.Open(path)
	if err != nil {
		return err
	}
	defer in.Close()
	tmp := path + ".gz.tmp"
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o640)
	if err != nil {
		return err
	}
	zw := gzip.NewWriter(out)
	_, err = io.Copy(zw, in)
	if cerr := zw.Close(); err == nil {
		err = cerr
	}
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(tmp, path+".gz")
	}
	if err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Remove(path)
}

type logFile struct {
	path  string
	day   string
	index int
	gz    bool
	size  int64
}

// listLocked, dizindeki bu yazıcıya ait dosyaları eskiden yeniye sıralı döndürür.
func (w *FileWriter) listLocked() []logFile {
	entries, err := os.ReadDir(w.dir)
	if err != nil {
		return nil
	}
	var files []logFile
	for _, e := range entries {
		m := w.pattern.FindStringSubmatch(e.Name())
		if m == nil || !e.Type().IsRegular() {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		index, _ := strconv.Atoi(m[2])
		files = append(files, logFile{path: filepath.Join(w.dir, e.Name()), day: m[1], index: index, gz: m[3] != "", size: info.Size()})
	}
	sort.Slice(files, func(i, j int) bool {
		if files[i].day != files[j].day {
			return files[i].day < files[j].day
		}
		if files[i].index != files[j].index {
			return files[i].index < files[j].index
		}
		return !files[i].gz && files[j].gz
	})
	return files
}

// compressLeftoversLocked, önceki bir çalışmadan kalan (açık dosya dışındaki) sıkıştırılmamış dosyaları sıkıştırır: ör.
// server gece yarısından önce durup ertesi gün açıldıysa ya da sıkıştırma yarıda kaldıysa.
func (w *FileWriter) compressLeftoversLocked() {
	for _, lf := range w.listLocked() {
		if !lf.gz && lf.path != w.f.Name() {
			os.Remove(lf.path + ".gz.tmp")
			w.compressAsyncLocked(lf.path)
		}
	}
}

// pruneLocked, saklama süresini aşan dosyaları, sonra toplam sınır aşılıyorsa en eskilerden başlayarak diğerlerini
// siler. Açık dosyaya ve sıkıştırılması süren dosyalara dokunmaz (onlar sıkıştırma bitince yeniden değerlendirilir).
func (w *FileWriter) pruneLocked() {
	oldest := w.opts.now().AddDate(0, 0, -(w.opts.MaxAgeDays - 1)).Format(time.DateOnly)
	current := ""
	if w.f != nil {
		current = w.f.Name()
	}
	files := w.listLocked()
	var kept []logFile
	var total int64
	for _, lf := range files {
		if w.pending[lf.path] || lf.path == current {
			kept = append(kept, lf)
			total += lf.size
			continue
		}
		if lf.day < oldest {
			w.remove(lf.path)
			continue
		}
		kept = append(kept, lf)
		total += lf.size
	}
	for _, lf := range kept {
		if total <= w.opts.MaxTotalBytes {
			break
		}
		if lf.path == current || w.pending[lf.path] {
			continue
		}
		w.remove(lf.path)
		total -= lf.size
	}
}

func (w *FileWriter) remove(path string) {
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		fmt.Fprintf(w.opts.errOut, "log file: remove %s: %v\n", path, err)
	}
}
