package logging

import (
	"compress/gzip"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

// fakeClock, testin ilerlettiği saattir.
type fakeClock struct{ t time.Time }

func (c *fakeClock) now() time.Time { return c.t }

func newClock(day string) *fakeClock {
	t, _ := time.Parse(time.DateOnly, day)
	return &fakeClock{t.Add(12 * time.Hour)}
}

func openTestWriter(t *testing.T, dir string, clock *fakeClock, maxAge int, maxTotal, maxFile int64) *FileWriter {
	t.Helper()
	w, err := OpenFile(FileOptions{
		Path: filepath.Join(dir, "server.log"), MaxAgeDays: maxAge, MaxTotalBytes: maxTotal,
		now: clock.now, maxFileBytes: maxFile, errOut: io.Discard,
	})
	if err != nil {
		t.Fatal(err)
	}
	return w
}

func write(t *testing.T, w *FileWriter, s string) {
	t.Helper()
	if n, err := w.Write([]byte(s)); err != nil || n != len(s) {
		t.Fatalf("Write = %d, %v", n, err)
	}
}

func files(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	sort.Strings(names)
	return names
}

func readLog(t *testing.T, path string) string {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var r io.Reader = f
	if strings.HasSuffix(path, ".gz") {
		zr, err := gzip.NewReader(f)
		if err != nil {
			t.Fatal(err)
		}
		r = zr
	}
	b, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func touch(t *testing.T, path string, size int) {
	t.Helper()
	if err := os.WriteFile(path, []byte(strings.Repeat("x", size)), 0o640); err != nil {
		t.Fatal(err)
	}
}

func TestFileWriterAppendsAcrossRestartsOnTheSameDay(t *testing.T) {
	dir := t.TempDir()
	clock := newClock("2026-09-26")

	w := openTestWriter(t, dir, clock, 14, 1<<20, 1<<20)
	write(t, w, "first\n")
	w.Close()
	w = openTestWriter(t, dir, clock, 14, 1<<20, 1<<20)
	write(t, w, "second\n")
	w.Close()

	if got := files(t, dir); len(got) != 1 || got[0] != "server-2026-09-26.log" {
		t.Fatalf("files = %v", got)
	}
	if got := readLog(t, filepath.Join(dir, "server-2026-09-26.log")); got != "first\nsecond\n" {
		t.Fatalf("content = %q", got)
	}
}

func TestFileWriterStartsANewFileEachDayAndCompressesThePrevious(t *testing.T) {
	dir := t.TempDir()
	clock := newClock("2026-09-26")
	w := openTestWriter(t, dir, clock, 14, 1<<20, 1<<20)
	write(t, w, "day one\n")

	clock.t = clock.t.Add(24 * time.Hour)
	write(t, w, "day two\n")
	w.Close() // bekleyen sıkıştırmayı da bekler

	want := []string{"server-2026-09-26.log.gz", "server-2026-09-27.log"}
	if got := files(t, dir); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("files = %v, want %v", got, want)
	}
	if got := readLog(t, filepath.Join(dir, want[0])); got != "day one\n" {
		t.Fatalf("compressed content = %q", got)
	}
}

func TestFileWriterSplitsALargeDay(t *testing.T) {
	dir := t.TempDir()
	w := openTestWriter(t, dir, newClock("2026-09-26"), 14, 1<<20, 10)
	for _, line := range []string{"line-1\n", "line-2\n", "line-3\n"} {
		write(t, w, line)
	}
	w.Close()

	want := []string{"server-2026-09-26.1.log.gz", "server-2026-09-26.2.log", "server-2026-09-26.log.gz"}
	if got := files(t, dir); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("files = %v, want %v", got, want)
	}
	if readLog(t, filepath.Join(dir, "server-2026-09-26.log.gz"))+readLog(t, filepath.Join(dir, "server-2026-09-26.1.log.gz"))+
		readLog(t, filepath.Join(dir, "server-2026-09-26.2.log")) != "line-1\nline-2\nline-3\n" {
		t.Fatal("lines lost or reordered across parts")
	}
}

func TestFileWriterDeletesDaysOlderThanMaxAgeAndLeavesForeignFiles(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{
		"server-2026-09-22.log.gz", "server-2026-09-23.1.log.gz", // 3 günlük pencerenin dışında
		"server-2026-09-24.log.gz", "server-2026-09-25.log.gz", // içinde
		"other-2026-01-01.log.gz", "server-notes.txt", "server-2026-01-01.log.bak", // başkasının
	} {
		touch(t, filepath.Join(dir, name), 10)
	}
	w := openTestWriter(t, dir, newClock("2026-09-26"), 3, 1<<20, 1<<20)
	w.Close()

	want := []string{"other-2026-01-01.log.gz", "server-2026-01-01.log.bak", "server-2026-09-24.log.gz",
		"server-2026-09-25.log.gz", "server-2026-09-26.log", "server-notes.txt"}
	if got := files(t, dir); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("files = %v, want %v", got, want)
	}
}

func TestFileWriterEnforcesTheTotalSizeCapOldestFirst(t *testing.T) {
	dir := t.TempDir()
	touch(t, filepath.Join(dir, "server-2026-09-23.log.gz"), 400)
	touch(t, filepath.Join(dir, "server-2026-09-24.log.gz"), 400)
	touch(t, filepath.Join(dir, "server-2026-09-25.log.gz"), 400)

	w := openTestWriter(t, dir, newClock("2026-09-26"), 14, 1000, 1000)
	write(t, w, strings.Repeat("y", 100)+"\n")
	w.Close()

	// 1200 + bugün > 1000: en eski gün gider, bugünkü açık dosyaya dokunulmaz.
	want := []string{"server-2026-09-24.log.gz", "server-2026-09-25.log.gz", "server-2026-09-26.log"}
	if got := files(t, dir); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("files = %v, want %v", got, want)
	}
}

func TestFileWriterCompressesLeftoversFromAPreviousRun(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "server-2026-09-25.log"), []byte("yesterday\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	touch(t, filepath.Join(dir, "server-2026-09-25.log.gz.tmp"), 5) // yarıda kalmış sıkıştırma

	w := openTestWriter(t, dir, newClock("2026-09-26"), 14, 1<<20, 1<<20)
	w.Close()

	want := []string{"server-2026-09-25.log.gz", "server-2026-09-26.log"}
	if got := files(t, dir); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("files = %v, want %v", got, want)
	}
	if got := readLog(t, filepath.Join(dir, want[0])); got != "yesterday\n" {
		t.Fatalf("content = %q", got)
	}
}

func TestFileWriterSwallowsWriteErrorsAndRecovers(t *testing.T) {
	dir := t.TempDir()
	clock := newClock("2026-09-26")
	w := openTestWriter(t, dir, clock, 14, 1<<20, 1<<20)
	defer w.Close()

	w.f.Close() // disk hatası gibi: sonraki yazma başarısız olur
	write(t, w, "lost\n")
	if !w.failing {
		t.Fatal("a failed write must be remembered")
	}

	clock.t = clock.t.Add(24 * time.Hour) // yeni gün yeni dosya açar ve yazma düzelir
	write(t, w, "back\n")
	if w.failing {
		t.Fatal("a successful write must clear the failure")
	}
	if got := readLog(t, filepath.Join(dir, "server-2026-09-27.log")); got != "back\n" {
		t.Fatalf("content = %q", got)
	}
}

func TestOpenFileRejectsBadOptions(t *testing.T) {
	dir := t.TempDir()
	for _, opts := range []FileOptions{
		{Path: filepath.Join(dir, "server.log"), MaxAgeDays: 0, MaxTotalBytes: 1},
		{Path: filepath.Join(dir, "server.log"), MaxAgeDays: 1, MaxTotalBytes: 0},
		{Path: dir + "/", MaxAgeDays: 1, MaxTotalBytes: 1},
		{Path: dir, MaxAgeDays: 1, MaxTotalBytes: 1}, // var olan bir dizin
	} {
		if w, err := OpenFile(opts); err == nil {
			w.Close()
			t.Fatalf("OpenFile(%+v): expected an error", opts)
		}
	}
}
