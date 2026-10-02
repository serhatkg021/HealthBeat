package logging

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Log dosyalarının panelden okunması (Sistem Araçları → Log Analiz). Okuyucu yalnızca bu FileWriter'ın kendi dosyalarını
// (adlandırmasına uyanları) açar: çağıran bir yol değil, yalnızca bir gün verir. Dosyalar akışla okunur; bir günün
// bütün parçaları (boyuttan bölünmüş ve gzip'li olanlar dahil) sırayla taranır.

var (
	// ErrBadDay, gün YYYY-AA-GG biçiminde değilse döner.
	ErrBadDay = errors.New("log reader: day must be YYYY-MM-DD")
	// ErrNoSuchDay, o güne ait log dosyası yoksa döner.
	ErrNoSuchDay = errors.New("log reader: no log file for that day")
)

var dayPattern = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)

const (
	// maxLineBytes, bir satırın okunan en çok baytıdır; daha uzun satırın kalanı atılır (bir hata gövdesi satırı
	// şişirse bile bellek sınırlı kalır).
	maxLineBytes = 256 << 10
	// ctxCheckEvery, taramanın kaç satırda bir iptal/süre denetlediğidir.
	ctxCheckEvery = 2000
)

// DayFiles, bir günün log dosyalarının özetidir.
type DayFiles struct {
	Day string `json:"day"`
	// Parts, o günün dosya sayısıdır (boyuttan bölündüyse birden çok).
	Parts int `json:"parts"`
	// Bytes, diskteki toplam boyuttur (sıkıştırılmış dosyalar sıkıştırılmış hâliyle).
	Bytes int64 `json:"bytes"`
	// Compressed, günün bütün dosyalarının gzip'li olduğunu söyler (geçmiş gün).
	Compressed bool `json:"compressed"`
}

// Days, log dosyası olan günleri en yeniden eskiye döndürür.
func (w *FileWriter) Days() []DayFiles {
	out := []DayFiles{}
	for _, lf := range w.files("") {
		if n := len(out); n > 0 && out[n-1].Day == lf.day {
			out[n-1].Parts++
			out[n-1].Bytes += lf.size
			out[n-1].Compressed = out[n-1].Compressed && lf.gz
			continue
		}
		out = append(out, DayFiles{Day: lf.day, Parts: 1, Bytes: lf.size, Compressed: lf.gz})
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out
}

// HasDay, day'e ait log dosyası olup olmadığını denetler: biçim yanlışsa ErrBadDay, dosya yoksa ErrNoSuchDay.
func (w *FileWriter) HasDay(day string) error {
	if !dayPattern.MatchString(day) {
		return ErrBadDay
	}
	if len(w.files(day)) == 0 {
		return ErrNoSuchDay
	}
	return nil
}

// Limits, saklama sınırlarını döndürür; henüz bilinmiyorsa (ayarlar okunmadan önce) known false'tur.
func (w *FileWriter) Limits() (maxAgeDays int, maxTotalBytes int64, known bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.opts.MaxAgeDays, w.opts.MaxTotalBytes, w.limitsKnown
}

// files, day'in (boşsa bütün günlerin) dosyalarını eskiden yeniye döndürür. Bir parça sıkıştırılırken kısa bir süre hem
// düz hem gzip'li hâli bulunur: o parça bir kez (düz hâliyle) sayılır.
func (w *FileWriter) files(day string) []logFile {
	w.mu.Lock()
	all := w.listLocked()
	w.mu.Unlock()
	out := all[:0]
	for _, lf := range all {
		if day != "" && lf.day != day {
			continue
		}
		if n := len(out); n > 0 && out[n-1].day == lf.day && out[n-1].index == lf.index {
			continue // aynı parçanın gzip'li kopyası (listLocked düz olanı önce sıralar)
		}
		out = append(out, lf)
	}
	return out
}

// openPart, bir parçayı açar ve (gzip'liyse açarak) okur. Liste alındıktan sonra sıkıştırılıp adı değişmiş parça
// gzip'li adıyla denenir.
func openPart(lf logFile) (io.ReadCloser, error) {
	path, gz := lf.path, lf.gz
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) && !gz {
		path, gz = path+".gz", true
		f, err = os.Open(path)
	}
	if err != nil {
		return nil, err
	}
	if !gz {
		return f, nil
	}
	zr, err := gzip.NewReader(f)
	if err != nil {
		f.Close()
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return struct {
		io.Reader
		io.Closer
	}{zr, closers{zr, f}}, nil
}

type closers []io.Closer

func (c closers) Close() error {
	var first error
	for _, x := range c {
		if err := x.Close(); err != nil && first == nil {
			first = err
		}
	}
	return first
}

// WriteDay, day'in bütün satırlarını (parçalar sırayla, gzip açılmış) dst'ye düz metin olarak yazar.
func (w *FileWriter) WriteDay(ctx context.Context, day string, dst io.Writer) error {
	if !dayPattern.MatchString(day) {
		return ErrBadDay
	}
	files := w.files(day)
	if len(files) == 0 {
		return ErrNoSuchDay
	}
	for _, lf := range files {
		if err := ctx.Err(); err != nil {
			return err
		}
		r, err := openPart(lf)
		if errors.Is(err, os.ErrNotExist) {
			continue // bu arada saklama sınırıyla silinmiş
		}
		if err != nil {
			return err
		}
		_, err = io.Copy(dst, r)
		r.Close()
		if err != nil {
			return err
		}
	}
	return nil
}

// Attr, bir log satırının bir alanıdır (yazıldığı sırayla).
type Attr struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

// Entry, ayrıştırılmış bir log satırıdır. Ayrıştırılamayan satır (ör. çok satırlı bir yığın izi) Message'da ham hâliyle
// döner; Time nil ve Level boştur.
type Entry struct {
	// Line, satırın o gün içindeki sıra numarasıdır (1'den başlar, parçalar boyunca sürer); sayfalama imlecidir.
	Line    int        `json:"line"`
	Time    *time.Time `json:"time"`
	Level   string     `json:"level"`
	Message string     `json:"message"`
	Attrs   []Attr     `json:"attrs"`
	// Truncated, satırın okuma sınırından uzun olduğunu ve kesildiğini söyler.
	Truncated bool `json:"truncated,omitempty"`
}

// Query, bir günün satırlarını daraltır; sıfır alanlar "süzgeç yok" demektir.
type Query struct {
	Day string
	// MinLevel verilirse yalnızca bu ve üstü seviyeler döner ("debug", "info", "warn", "error"). Seviyesi okunamayan
	// (ayrıştırılamayan) satırlar her zaman döner: genellikle bir hatanın devamıdırlar.
	MinLevel string
	// Text, satırın ham hâlinde büyük-küçük harf ayırmadan aranır.
	Text string
	// RequestID, request_id alanı tam bu olan satırlardır.
	RequestID string
	// From ve To, satır zamanını sınırlar (From dahil, To hariç). Zamanı olmayan satır kendinden önceki satırın
	// zamanını alır.
	From, To *time.Time
	// Before, yalnızca numarası bundan küçük satırları döndürür (bir önceki sayfanın NextBefore'u); 0 = günün sonundan.
	Before int
	Limit  int
}

// Result, Read'in bir sayfasıdır: Entries en yeniden eskiye sıralıdır.
type Result struct {
	Entries []Entry `json:"entries"`
	// NextBefore, daha eski eşleşen satır varsa bir sonraki sayfanın Before değeridir; yoksa 0.
	NextBefore int `json:"next_before"`
	// Scanned, taranan satır sayısıdır.
	Scanned int `json:"scanned"`
}

// Read, q.Day'in satırlarını süzer ve en yeni q.Limit eşleşmeyi döndürür. Gün baştan sona taranır (dosyalar zaman
// sırasıyla yazılır, en yeniler sondadır); ctx'in süresi dolarsa hata döner.
func (w *FileWriter) Read(ctx context.Context, q Query) (Result, error) {
	if !dayPattern.MatchString(q.Day) {
		return Result{}, ErrBadDay
	}
	if q.Limit < 1 {
		return Result{}, errors.New("log reader: limit must be positive")
	}
	minRank := -1
	if q.MinLevel != "" {
		lvl, err := ParseLevel(q.MinLevel)
		if err != nil {
			return Result{}, fmt.Errorf("log reader: level %w", err)
		}
		minRank = int(lvl)
	}
	files := w.files(q.Day)
	if len(files) == 0 {
		return Result{}, ErrNoSuchDay
	}
	needle := []byte(strings.ToLower(q.Text))

	// Halka: en son Limit+1 eşleşme. Fazladan bir tane, daha eski eşleşme kalıp kalmadığını söyler.
	ring := make([]Entry, 0, q.Limit+1)
	var res Result
	var lastTime *time.Time
	line := 0

scan:
	for _, lf := range files {
		r, err := openPart(lf)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return Result{}, err
		}
		br := bufio.NewReaderSize(r, 64<<10)
		for {
			raw, truncated, err := readLine(br)
			if err == io.EOF {
				break
			}
			if err != nil {
				r.Close()
				return Result{}, fmt.Errorf("%s: %w", lf.path, err)
			}
			line++
			if q.Before > 0 && line >= q.Before {
				r.Close()
				break scan
			}
			if line%ctxCheckEvery == 0 {
				if err := ctx.Err(); err != nil {
					r.Close()
					return Result{}, err
				}
			}
			if len(needle) > 0 && !bytes.Contains(bytes.ToLower(raw), needle) {
				// Zaman süzgeci için son görülen zamanı izlemeye gerek yok: bu satır zaten elendi; zamanı olan
				// bir sonraki eşleşen satır kendi zamanını taşır.
				continue
			}
			e := ParseLine(string(raw))
			e.Line, e.Truncated = line, truncated
			if e.Time != nil {
				lastTime = e.Time
			}
			if !q.matches(e, lastTime, minRank) {
				continue
			}
			if len(ring) == cap(ring) {
				copy(ring, ring[1:])
				ring = ring[:len(ring)-1]
			}
			ring = append(ring, e)
		}
		r.Close()
	}
	res.Scanned = line
	if q.Before > 0 && line >= q.Before {
		res.Scanned = q.Before - 1
	}

	if len(ring) > q.Limit {
		ring = ring[1:]
		res.NextBefore = ring[0].Line
	}
	res.Entries = make([]Entry, len(ring))
	for i, e := range ring {
		res.Entries[len(ring)-1-i] = e
	}
	return res, nil
}

func (q Query) matches(e Entry, at *time.Time, minRank int) bool {
	if minRank >= 0 && e.Level != "" && levelRank(e.Level) < minRank {
		return false
	}
	if q.RequestID != "" {
		found := false
		for _, a := range e.Attrs {
			if a.Key == "request_id" && a.Value == q.RequestID {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	if q.From != nil && (at == nil || at.Before(*q.From)) {
		return false
	}
	if q.To != nil && (at == nil || !at.Before(*q.To)) {
		return false
	}
	return true
}

// levelRank, bir seviye adını slog sırasına çevirir ("WARN" → 4, "ERROR+2" → 10); tanınmayan ad en düşük sayılır.
func levelRank(level string) int {
	name, offset, _ := strings.Cut(level, "+")
	base, err := ParseLevel(name)
	if err != nil {
		return -1 << 30
	}
	n, _ := strconv.Atoi(offset)
	return int(base) + n
}

// readLine, bir sonraki satırı (satır sonu olmadan) okur. maxLineBytes'tan uzun satırın kalanı atılır.
func readLine(br *bufio.Reader) (line []byte, truncated bool, err error) {
	for {
		chunk, isPrefix, err := br.ReadLine()
		if err != nil {
			if err == io.EOF && len(line) > 0 {
				return line, truncated, nil
			}
			return nil, false, err
		}
		if room := maxLineBytes - len(line); room > 0 {
			if len(chunk) > room {
				chunk, truncated = chunk[:room], true
			}
			line = append(line, chunk...)
		} else if len(chunk) > 0 {
			truncated = true
		}
		if !isPrefix {
			if line == nil {
				line = []byte{}
			}
			return line, truncated, nil
		}
	}
}

// ParseLine, bir log satırını ayrıştırır: slog'un text (key=value) ve JSON biçimlerini tanır. Tanıyamadığı satırı
// Message'da ham hâliyle döndürür.
func ParseLine(raw string) Entry {
	s := strings.TrimRight(raw, "\r")
	var e Entry
	var ok bool
	if strings.HasPrefix(s, "{") {
		e, ok = parseJSONLine(s)
	} else if strings.HasPrefix(s, "time=") {
		e, ok = parseTextLine(s)
	}
	if !ok {
		return Entry{Message: s, Attrs: []Attr{}}
	}
	return e
}

// set, tanınan alanları (time, level, msg) Entry'ye, diğerlerini Attrs'a koyar.
func (e *Entry) set(key, value string) {
	switch key {
	case "time":
		if t, err := time.Parse(time.RFC3339Nano, value); err == nil && e.Time == nil {
			e.Time = &t
			return
		}
	case "level":
		if e.Level == "" {
			e.Level = strings.ToUpper(value)
			return
		}
	case "msg":
		if e.Message == "" {
			e.Message = value
			return
		}
	}
	e.Attrs = append(e.Attrs, Attr{Key: key, Value: value})
}

func parseTextLine(s string) (Entry, bool) {
	e := Entry{Attrs: []Attr{}}
	for {
		s = strings.TrimLeft(s, " ")
		if s == "" {
			break
		}
		var key string
		if strings.HasPrefix(s, `"`) {
			q, err := strconv.QuotedPrefix(s)
			if err != nil {
				return Entry{}, false
			}
			key, _ = strconv.Unquote(q)
			s = s[len(q):]
			if !strings.HasPrefix(s, "=") {
				return Entry{}, false
			}
			s = s[1:]
		} else {
			eq := strings.IndexByte(s, '=')
			if eq <= 0 || strings.ContainsRune(s[:eq], ' ') {
				return Entry{}, false
			}
			key, s = s[:eq], s[eq+1:]
		}
		var value string
		if strings.HasPrefix(s, `"`) {
			q, err := strconv.QuotedPrefix(s)
			if err != nil {
				return Entry{}, false
			}
			value, _ = strconv.Unquote(q)
			s = s[len(q):]
		} else if sp := strings.IndexByte(s, ' '); sp >= 0 {
			value, s = s[:sp], s[sp:]
		} else {
			value, s = s, ""
		}
		e.set(key, value)
	}
	return e, e.Time != nil
}

func parseJSONLine(s string) (Entry, bool) {
	dec := json.NewDecoder(strings.NewReader(s))
	if tok, err := dec.Token(); err != nil || tok != json.Delim('{') {
		return Entry{}, false
	}
	e := Entry{Attrs: []Attr{}}
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return Entry{}, false
		}
		key, ok := tok.(string)
		if !ok {
			return Entry{}, false
		}
		var raw json.RawMessage
		if err := dec.Decode(&raw); err != nil {
			return Entry{}, false
		}
		// Metin değerler tırnaksız, diğerleri (sayı, iç içe grup) JSON hâliyle gösterilir.
		value := string(raw)
		var str string
		if json.Unmarshal(raw, &str) == nil {
			value = str
		}
		e.set(key, value)
	}
	return e, e.Time != nil
}
