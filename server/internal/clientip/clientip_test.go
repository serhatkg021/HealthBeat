package clientip

import (
	"context"
	"errors"
	"net/http/httptest"
	"net/netip"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestParseProxies(t *testing.T) {
	p, err := ParseProxies(" 10.0.0.5 , 172.18.0.0/16, fd00::/8, ::ffff:192.168.1.9, Panel ,,")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"10.0.0.5/32", "172.18.0.0/16", "fd00::/8", "192.168.1.9/32"}
	if len(p.Prefixes) != len(want) {
		t.Fatalf("prefixes = %v, want %v", p.Prefixes, want)
	}
	for i, w := range want {
		if p.Prefixes[i].String() != w {
			t.Fatalf("prefix %d = %s, want %s", i, p.Prefixes[i], w)
		}
	}
	if len(p.Hosts) != 1 || p.Hosts[0] != "panel" {
		t.Fatalf("hosts = %v, want [panel]", p.Hosts)
	}

	if p, err := ParseProxies(""); err != nil || !p.Empty() {
		t.Fatalf("empty input: %+v, %v", p, err)
	}

	for _, bad := range []string{"0.0.0.0/0", "::/0", "10.0.0.0/33", "*", "pa nel", "-panel", "panel_x", "http://panel"} {
		if _, err := ParseProxies(bad); err == nil {
			t.Errorf("ParseProxies(%q) succeeded, want an error", bad)
		}
	}
}

func TestParseProxiesMappedPrefix(t *testing.T) {
	p, err := ParseProxies("::ffff:10.0.0.0/104")
	if err != nil {
		t.Fatal(err)
	}
	if got := p.Prefixes[0].String(); got != "10.0.0.0/8" {
		t.Fatalf("prefix = %s, want 10.0.0.0/8", got)
	}
}

func clientIP(t *testing.T, r *Resolver, remoteAddr string, xff ...string) string {
	t.Helper()
	req := httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = remoteAddr
	for _, v := range xff {
		req.Header.Add("X-Forwarded-For", v)
	}
	return r.ClientIP(req)
}

func mustResolver(t *testing.T, raw string) *Resolver {
	t.Helper()
	p, err := ParseProxies(raw)
	if err != nil {
		t.Fatal(err)
	}
	return New(p)
}

func TestClientIP(t *testing.T) {
	r := mustResolver(t, "172.18.0.5, 10.1.0.0/16")

	cases := []struct {
		name   string
		remote string
		xff    []string
		want   string
	}{
		{"untrusted peer ignores the header", "66.6.6.6:4000", []string{"1.2.3.4"}, "66.6.6.6"},
		{"untrusted peer, no header", "203.0.113.7:4000", nil, "203.0.113.7"},
		{"trusted peer, single entry", "172.18.0.5:5000", []string{"85.1.1.1"}, "85.1.1.1"},
		{"trusted peer, no header", "172.18.0.5:5000", nil, "172.18.0.5"},
		{"trusted peer, empty header", "172.18.0.5:5000", []string{""}, "172.18.0.5"},
		{"spoofed left entry is skipped", "172.18.0.5:5000", []string{"6.6.6.6, 85.1.1.1"}, "85.1.1.1"},
		{"chain through another trusted proxy", "172.18.0.5:5000", []string{"85.1.1.1, 10.1.2.3"}, "85.1.1.1"},
		{"all entries trusted", "172.18.0.5:5000", []string{"10.1.2.3"}, "172.18.0.5"},
		{"malformed rightmost entry", "172.18.0.5:5000", []string{"85.1.1.1, garbage"}, "172.18.0.5"},
		{"multiple header lines are one list", "172.18.0.5:5000", []string{"6.6.6.6", "85.1.1.1"}, "85.1.1.1"},
		{"entry with port", "172.18.0.5:5000", []string{"85.1.1.1:5555"}, "85.1.1.1"},
		{"ipv6 client", "172.18.0.5:5000", []string{"2001:db8::7"}, "2001:db8::7"},
		{"ipv4-mapped peer", "[::ffff:172.18.0.5]:5000", []string{"85.1.1.1"}, "85.1.1.1"},
		{"ipv6 peer", "[2001:db8::1]:443", nil, "2001:db8::1"},
		{"remote addr without port", "203.0.113.7", nil, "203.0.113.7"},
	}
	for _, c := range cases {
		if got := clientIP(t, r, c.remote, c.xff...); got != c.want {
			t.Errorf("%s: ClientIP = %q, want %q", c.name, got, c.want)
		}
	}
}

func TestClientIPWithoutTrustedProxies(t *testing.T) {
	r := mustResolver(t, "")
	if got := clientIP(t, r, "172.18.0.5:5000", "85.1.1.1"); got != "172.18.0.5" {
		t.Fatalf("ClientIP = %q, want the peer: nothing is trusted", got)
	}
	var nilResolver *Resolver
	if got := clientIP(t, nilResolver, "172.18.0.5:5000", "85.1.1.1"); got != "172.18.0.5" {
		t.Fatalf("nil resolver: ClientIP = %q, want the peer", got)
	}
	if got := clientIP(t, r, "not-an-address", "85.1.1.1"); got != "not-an-address" {
		t.Fatalf("unparsable remote addr: ClientIP = %q, want it returned as is", got)
	}
}

func TestHostNamesAreTrustedOnceResolved(t *testing.T) {
	r := mustResolver(t, "panel")
	answer := []netip.Addr{netip.MustParseAddr("172.18.0.5")}
	var lookupErr error
	r.lookup = func(_ context.Context, host string) ([]netip.Addr, error) {
		if host != "panel" {
			t.Fatalf("looked up %q, want panel", host)
		}
		return append([]netip.Addr(nil), answer...), lookupErr
	}

	// Çözülmeden önce güvenilmez: başlık yok sayılır.
	if got := clientIP(t, r, "172.18.0.5:5000", "85.1.1.1"); got != "172.18.0.5" {
		t.Fatalf("before refresh: ClientIP = %q, want the peer", got)
	}

	r.Refresh(context.Background())
	if got := clientIP(t, r, "172.18.0.5:5000", "85.1.1.1"); got != "85.1.1.1" {
		t.Fatalf("after refresh: ClientIP = %q, want 85.1.1.1", got)
	}

	// Container yeniden oluştu, IP'si değişti: eski adrese artık güvenilmez.
	answer = []netip.Addr{netip.MustParseAddr("172.18.0.9")}
	r.Refresh(context.Background())
	if got := clientIP(t, r, "172.18.0.5:5000", "85.1.1.1"); got != "172.18.0.5" {
		t.Fatalf("old address after change: ClientIP = %q, want the peer", got)
	}
	if got := clientIP(t, r, "172.18.0.9:5000", "85.1.1.1"); got != "85.1.1.1" {
		t.Fatalf("new address: ClientIP = %q, want 85.1.1.1", got)
	}

	// Anlık DNS hatası son bilinen adresi düşürmez.
	answer, lookupErr = nil, errors.New("temporary failure")
	r.Refresh(context.Background())
	if got := clientIP(t, r, "172.18.0.9:5000", "85.1.1.1"); got != "85.1.1.1" {
		t.Fatalf("after failed lookup: ClientIP = %q, want the last known address still trusted", got)
	}
}

func TestProxiesString(t *testing.T) {
	p, err := ParseProxies("10.0.0.5, panel")
	if err != nil {
		t.Fatal(err)
	}
	if s := p.String(); !strings.Contains(s, "10.0.0.5/32") || !strings.Contains(s, "panel") {
		t.Fatalf("String() = %q", s)
	}
}

// fakeDNS, Run'ın goroutine'inden eşzamanlı çağrılabilen sahte bir çözücüdür.
type fakeDNS struct {
	mu     sync.Mutex
	answer []netip.Addr
	err    error
	calls  int
}

func (f *fakeDNS) set(addr string, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.answer, f.err = nil, err
	if addr != "" {
		f.answer = []netip.Addr{netip.MustParseAddr(addr)}
	}
}

func (f *fakeDNS) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

func (f *fakeDNS) lookup(context.Context, string) ([]netip.Addr, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	return append([]netip.Addr(nil), f.answer...), f.err
}

// runResolver, "panel"e güvenen bir Resolver'ı verilen zamanlamayla çalıştırır.
func runResolver(t *testing.T, dns *fakeDNS, refresh, retry, gap time.Duration) *Resolver {
	t.Helper()
	r := mustResolver(t, "panel")
	r.lookup = dns.lookup
	r.refreshEvery, r.retryEvery, r.minTriggeredGap = refresh, retry, gap
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { r.Run(ctx); close(done) }()
	t.Cleanup(func() { cancel(); <-done })
	return r
}

// eventually, cond doğru olana kadar (en çok 2 sn) bekler.
func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for: %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// Açılışta panel container'ı henüz yokken ad çözülemez; uzun aralığı beklemeden kısa aralıkla yeniden denenmeli.
func TestRunRetriesQuicklyUntilResolved(t *testing.T) {
	dns := &fakeDNS{}
	dns.set("", errors.New("no such host"))
	r := runResolver(t, dns, time.Hour, 10*time.Millisecond, time.Hour)

	eventually(t, "a few quick retries", func() bool { return dns.count() >= 3 })
	dns.set("172.18.0.5", nil)
	eventually(t, "panel trusted once it resolves", func() bool {
		return clientIP(t, r, "172.18.0.5:5000", "85.1.1.1") == "85.1.1.1"
	})

	// Çözüldükten sonra uzun aralığa geçer: yeni sorgu yapılmaz.
	n := dns.count()
	time.Sleep(50 * time.Millisecond)
	if got := dns.count(); got != n {
		t.Fatalf("lookups after resolving: %d -> %d, want no more on the long interval", n, got)
	}
}

// Panel yeniden oluşup yeni IP aldığında, yeni adresten gelen X-Forwarded-For'lu istek hemen yeniden çözümü tetikler.
func TestForwardedRequestFromUnknownPeerTriggersRefresh(t *testing.T) {
	dns := &fakeDNS{}
	dns.set("172.18.0.5", nil)
	r := runResolver(t, dns, time.Hour, time.Hour, 0)
	eventually(t, "initial resolution", func() bool {
		return clientIP(t, r, "172.18.0.5:5000", "85.1.1.1") == "85.1.1.1"
	})

	dns.set("172.18.0.9", nil)
	if got := clientIP(t, r, "172.18.0.9:5000", "85.1.1.1"); got != "172.18.0.9" {
		t.Fatalf("first request from the new address = %q, want the peer until it is re-resolved", got)
	}
	eventually(t, "new address trusted after the triggered refresh", func() bool {
		return clientIP(t, r, "172.18.0.9:5000", "85.1.1.1") == "85.1.1.1"
	})

	// Başlıksız istekler (ör. doğrudan bağlanan agent'lar) çözüm tetiklemez.
	n := dns.count()
	for i := 0; i < 5; i++ {
		clientIP(t, r, "203.0.113.7:4000")
	}
	time.Sleep(50 * time.Millisecond)
	if got := dns.count(); got != n {
		t.Fatalf("requests without X-Forwarded-For triggered lookups: %d -> %d", n, got)
	}
}

// Başlığı herkes gönderebilir: tetiklenen çözümler minTriggeredGap ile sınırlı olmalı.
func TestTriggeredRefreshesAreRateLimited(t *testing.T) {
	dns := &fakeDNS{}
	dns.set("172.18.0.5", nil)
	r := runResolver(t, dns, time.Hour, time.Hour, time.Hour)
	eventually(t, "initial resolution", func() bool { return dns.count() == 1 })

	for i := 0; i < 50; i++ {
		clientIP(t, r, "66.6.6.6:5000", "1.2.3.4")
	}
	time.Sleep(50 * time.Millisecond)
	if got := dns.count(); got != 1 {
		t.Fatalf("lookups = %d, want 1: triggers within minTriggeredGap must not reach DNS", got)
	}
}
