package logging

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestParseLineText(t *testing.T) {
	e := ParseLine(`time=2026-10-02T19:41:10.631Z level=WARN msg="request failed" method=GET path=/api/v1/meta status=401 resp_body="{\"code\":\"unauthorized\"}" request_id=118df6b0 ip=172.19.0.1`)
	if e.Time == nil || e.Time.Format(time.RFC3339Nano) != "2026-10-02T19:41:10.631Z" || e.Level != "WARN" || e.Message != "request failed" {
		t.Fatalf("entry = %+v", e)
	}
	want := []Attr{{"method", "GET"}, {"path", "/api/v1/meta"}, {"status", "401"}, {"resp_body", `{"code":"unauthorized"}`}, {"request_id", "118df6b0"}, {"ip", "172.19.0.1"}}
	if fmt.Sprint(e.Attrs) != fmt.Sprint(want) {
		t.Fatalf("attrs = %v, want %v", e.Attrs, want)
	}

	// Tırnaksız ileti, boş değer ve tırnaklı anahtar.
	e = ParseLine(`time=2026-10-02T19:39:15Z level=INFO msg=request empty= "a key"=1`)
	if e.Message != "request" || fmt.Sprint(e.Attrs) != fmt.Sprint([]Attr{{"empty", ""}, {"a key", "1"}}) {
		t.Fatalf("entry = %+v", e)
	}
}

// slog'un kendi yazdığı satırlar (iki biçimde de) geri okunur: özel karakterler, iç içe grup, sayı.
func TestParseLineRoundTripsSlogOutput(t *testing.T) {
	for _, format := range []string{FormatText, FormatJSON} {
		var buf bytes.Buffer
		logger := New(&buf, slog.LevelDebug, format)
		logger.Error("çift \"tırnak\" ve\nsatır sonu", "status", 502, "took", 1.5, slog.Group("req_headers", "User-Agent", "curl/8 x"), "boş", "")
		e := ParseLine(strings.TrimSuffix(buf.String(), "\n"))
		if e.Time == nil || e.Level != "ERROR" || e.Message != "çift \"tırnak\" ve\nsatır sonu" {
			t.Fatalf("%s: entry = %+v (line %q)", format, e, buf.String())
		}
		attrs := map[string]string{}
		for _, a := range e.Attrs {
			attrs[a.Key] = a.Value
		}
		if attrs["status"] != "502" || attrs["took"] != "1.5" || attrs["boş"] != "" || len(e.Attrs) != 4 {
			t.Fatalf("%s: attrs = %v", format, e.Attrs)
		}
		// Grup text'te noktalı anahtar, JSON'da iç içe nesnedir.
		if format == FormatText && attrs["req_headers.User-Agent"] != "curl/8 x" {
			t.Fatalf("text group = %v", e.Attrs)
		}
		if format == FormatJSON && attrs["req_headers"] != `{"User-Agent":"curl/8 x"}` {
			t.Fatalf("json group = %v", e.Attrs)
		}
	}
}

func TestParseLineKeepsUnparsableLinesRaw(t *testing.T) {
	for _, raw := range []string{
		"goroutine 1 [running]:",
		"\tmain.main()",
		"",
		`{"msg": "zamansız"}`,
		`{bozuk json`,
		`time=dün level=INFO msg=x`,
		`time=2026-10-02T19:39:15Z level=INFO msg="kapanmayan tırnak`,
	} {
		if e := ParseLine(raw); e.Time != nil || e.Level != "" || e.Message != raw || e.Attrs == nil {
			t.Errorf("ParseLine(%q) = %+v, want it raw", raw, e)
		}
	}
}

func logLine(clock string, level, msg string, kv ...string) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "time=2026-10-02T%sZ level=%s msg=%q", clock, level, msg)
	for i := 0; i+1 < len(kv); i += 2 {
		fmt.Fprintf(&sb, " %s=%s", kv[i], kv[i+1])
	}
	return sb.String() + "\n"
}

func messages(entries []Entry) string {
	var out []string
	for _, e := range entries {
		out = append(out, e.Message)
	}
	return strings.Join(out, " ")
}

// Bir gün: gzip'li eski parça + bölünmüş parçalar; süzgeçler, en yeni önce sıra ve sayfalama.
func TestReadFiltersAndPagesADayAcrossParts(t *testing.T) {
	dir := t.TempDir()
	clock := newClock("2026-10-01")
	w := openTestWriter(t, dir, clock, 14, 1<<30, 150) // küçük parça: gün birkaç dosyaya bölünür
	defer w.Close()
	write(t, w, "time=2026-10-01T23:00:00Z level=INFO msg=dün\n")

	clock.t = clock.t.Add(24 * time.Hour)
	for _, l := range []string{
		logLine("10:00:00", "INFO", "bir", "request_id", "r1", "path", "/api/v1/meta"),
		logLine("10:00:01", "DEBUG", "iki", "request_id", "r1"),
		logLine("10:00:02", "WARN", "üç", "request_id", "r2", "path", "/API/v1/Hosts"),
		"goroutine 7 [running]:\n",
		logLine("10:00:03", "ERROR", "dört", "request_id", "r2"),
		logLine("11:30:00", "INFO", "beş", "request_id", "r3"),
	} {
		write(t, w, l)
	}
	w.compressing.Wait()
	ctx := context.Background()

	days := w.Days()
	if len(days) != 2 || days[0].Day != "2026-10-02" || days[0].Parts < 3 || days[0].Compressed || days[1].Day != "2026-10-01" || !days[1].Compressed || days[1].Bytes == 0 {
		t.Fatalf("days = %+v (files %v)", days, files(t, dir))
	}
	if _, _, known := w.Limits(); !known {
		t.Fatal("limits are not known")
	}

	read := func(q Query) Result {
		t.Helper()
		q.Day = "2026-10-02"
		if q.Limit == 0 {
			q.Limit = 50
		}
		res, err := w.Read(ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		return res
	}
	at := func(clock string) *time.Time {
		tm, _ := time.Parse(time.RFC3339, "2026-10-02T"+clock+"Z")
		return &tm
	}
	for name, tc := range map[string]struct {
		q    Query
		want string
	}{
		"all, newest first":        {Query{}, "beş dört goroutine 7 [running]: üç iki bir"},
		"warn and above keeps raw": {Query{MinLevel: "warn"}, "dört goroutine 7 [running]: üç"},
		"error":                    {Query{MinLevel: "error"}, "dört goroutine 7 [running]:"},
		"info":                     {Query{MinLevel: "info"}, "beş dört goroutine 7 [running]: üç bir"},
		"text, case-insensitive":   {Query{Text: "/api/v1/hosts"}, "üç"},
		"text matches any field":   {Query{Text: "REQUEST_ID=R1"}, "iki bir"},
		"request id":               {Query{RequestID: "r2"}, "dört üç"},
		"request id is exact":      {Query{RequestID: "r"}, ""},
		"from":                     {Query{From: at("10:00:03")}, "beş dört"},
		"to is exclusive":          {Query{To: at("10:00:02")}, "iki bir"},
		"range; raw line inherits": {Query{From: at("10:00:02"), To: at("10:00:03")}, "goroutine 7 [running]: üç"},
		"combined":                 {Query{MinLevel: "info", RequestID: "r1"}, "bir"},
	} {
		if got := messages(read(tc.q).Entries); got != tc.want {
			t.Errorf("%s: %q, want %q", name, got, tc.want)
		}
	}

	// Sayfalama: en yeni iki satır, sonra imleçle daha eskiler.
	page := read(Query{Limit: 2})
	if messages(page.Entries) != "beş dört" || page.NextBefore != page.Entries[1].Line || page.Scanned != 6 || page.Entries[0].Line != 6 {
		t.Fatalf("first page = %+v", page)
	}
	page = read(Query{Limit: 2, Before: page.NextBefore})
	if messages(page.Entries) != "goroutine 7 [running]: üç" || page.NextBefore == 0 {
		t.Fatalf("second page = %+v", page)
	}
	page = read(Query{Limit: 2, Before: page.NextBefore})
	if messages(page.Entries) != "iki bir" || page.NextBefore != 0 {
		t.Fatalf("last page = %+v", page)
	}

	// Sıkıştırılmış gün de okunur.
	res, err := w.Read(ctx, Query{Day: "2026-10-01", Limit: 10})
	if err != nil || messages(res.Entries) != "dün" {
		t.Fatalf("compressed day: %+v err=%v", res, err)
	}

	// Günün düz metni: parçalar sırayla, olduğu gibi.
	var buf bytes.Buffer
	if err := w.WriteDay(ctx, "2026-10-02", &buf); err != nil || strings.Count(buf.String(), "\n") != 6 || !strings.HasPrefix(buf.String(), "time=2026-10-02T10:00:00Z") {
		t.Fatalf("WriteDay: err=%v\n%s", err, buf.String())
	}
	buf.Reset()
	if err := w.WriteDay(ctx, "2026-10-01", &buf); err != nil || buf.String() != "time=2026-10-01T23:00:00Z level=INFO msg=dün\n" {
		t.Fatalf("WriteDay of a compressed day: err=%v %q", err, buf.String())
	}
}

// İstemci yalnızca bir gün verebilir: yol, başka dosya ya da var olmayan gün okunamaz.
func TestReadOnlyOpensTheWritersOwnFiles(t *testing.T) {
	dir := t.TempDir()
	w := openTestWriter(t, dir, newClock("2026-10-02"), 14, 1<<30, 1<<20)
	defer w.Close()
	write(t, w, logLine("10:00:00", "INFO", "bir"))
	if err := os.WriteFile(filepath.Join(dir, "gizli-2026-10-03.log"), []byte("time=2026-10-03T00:00:00Z level=INFO msg=gizli\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	for _, day := range []string{"", "../etc/passwd", "2026-10-02/../..", "2026-10-2", "2026-10-02.log", "gizli-2026-10-03", "2026-10-02\n"} {
		if _, err := w.Read(ctx, Query{Day: day, Limit: 10}); !errors.Is(err, ErrBadDay) {
			t.Errorf("Read(%q): err=%v, want ErrBadDay", day, err)
		}
		if err := w.WriteDay(ctx, day, &bytes.Buffer{}); !errors.Is(err, ErrBadDay) {
			t.Errorf("WriteDay(%q): err=%v, want ErrBadDay", day, err)
		}
	}
	// Başka bir önekle yazılmış dosya bu yazıcının günü değildir.
	if _, err := w.Read(ctx, Query{Day: "2026-10-03", Limit: 10}); !errors.Is(err, ErrNoSuchDay) {
		t.Fatalf("foreign file: err=%v, want ErrNoSuchDay", err)
	}
	if days := w.Days(); len(days) != 1 || days[0].Day != "2026-10-02" {
		t.Fatalf("days = %+v", days)
	}
	if _, err := w.Read(ctx, Query{Day: "2026-10-02", Limit: 0}); err == nil {
		t.Fatal("limit 0 was accepted")
	}
	if _, err := w.Read(ctx, Query{Day: "2026-10-02", Limit: 10, MinLevel: "trace"}); err == nil {
		t.Fatal("an unknown level was accepted")
	}
}

// Çok uzun satır kesilir (bellek sınırlı kalır) ve işaretlenir; sonraki satır etkilenmez. İptal edilen tarama durur.
func TestReadTruncatesHugeLinesAndHonoursCancellation(t *testing.T) {
	dir := t.TempDir()
	w := openTestWriter(t, dir, newClock("2026-10-02"), 14, 1<<30, 1<<30)
	defer w.Close()
	write(t, w, logLine("10:00:00", "ERROR", "dev", "resp_body", strings.Repeat("x", maxLineBytes+5000)))
	write(t, w, logLine("10:00:01", "INFO", "sonraki"))

	res, err := w.Read(context.Background(), Query{Day: "2026-10-02", Limit: 10})
	if err != nil || len(res.Entries) != 2 || res.Entries[0].Message != "sonraki" || res.Entries[0].Truncated ||
		!res.Entries[1].Truncated || res.Entries[1].Message != "dev" || len(res.Entries[1].Attrs[0].Value) > maxLineBytes {
		t.Fatalf("entries = %d err=%v", len(res.Entries), err)
	}

	for range ctxCheckEvery {
		write(t, w, logLine("10:00:02", "INFO", "dolgu"))
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := w.Read(ctx, Query{Day: "2026-10-02", Limit: 10}); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled read: err=%v", err)
	}
}
