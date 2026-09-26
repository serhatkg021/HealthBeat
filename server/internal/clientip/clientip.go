// Package clientip, bir isteğin gerçek istemci IP'sini belirler. Server doğrudan bağlantı kabul ettiğinde bu TCP
// eşidir; önünde bir reverse proxy varsa (varsayılan compose kurulumunda panelin nginx'i /api/'yi proxy'ler) eş,
// proxy'nin kendisidir ve gerçek istemci X-Forwarded-For başlığındadır.
//
// X-Forwarded-For'u herkes yazabilir; bu yüzden başlığa yalnızca eş, TRUSTED_PROXIES ile verilen güvenilir bir
// proxy olduğunda bakılır. Aksi halde server'a doğrudan bağlanan biri her istekte başka bir sahte IP yazarak
// IP başına hız sınırlarını atlatır ve denetim kaydına sahte adres yazdırırdı. Bkz. docs/DEPLOYMENT.md.
package clientip

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"
)

// Proxies, TRUSTED_PROXIES'in ayrıştırılmış hâlidir: sabit adres aralıkları ve periyodik çözülen host adları.
type Proxies struct {
	Prefixes []netip.Prefix
	// Hosts, docker'da container IP'si sabit olmadığı için ad olarak verilen proxy'lerdir (ör. "panel").
	Hosts []string
}

// Empty, hiç güvenilir proxy olmadığını söyler (X-Forwarded-For hiç okunmaz).
func (p Proxies) Empty() bool { return len(p.Prefixes) == 0 && len(p.Hosts) == 0 }

func (p Proxies) String() string {
	parts := make([]string, 0, len(p.Prefixes)+len(p.Hosts))
	for _, pr := range p.Prefixes {
		parts = append(parts, pr.String())
	}
	return strings.Join(append(parts, p.Hosts...), ", ")
}

var hostnamePattern = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?(\.[a-z0-9]([a-z0-9-]*[a-z0-9])?)*$`)

// ParseProxies, virgülle ayrılmış IP, CIDR ya da host adı listesini doğrular. Her adresi kapsayan bir aralık
// (0.0.0.0/0, ::/0) reddedilir: bu, başlığa herkesten güvenmek demektir.
func ParseProxies(raw string) (Proxies, error) {
	var p Proxies
	for _, entry := range strings.Split(raw, ",") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		if strings.Contains(entry, "/") {
			prefix, err := netip.ParsePrefix(entry)
			if err != nil {
				return Proxies{}, fmt.Errorf("%q is not a valid CIDR", entry)
			}
			if prefix.Bits() == 0 {
				return Proxies{}, fmt.Errorf("%q would trust X-Forwarded-For from every address", entry)
			}
			p.Prefixes = append(p.Prefixes, unmapPrefix(prefix.Masked()))
			continue
		}
		if addr, err := netip.ParseAddr(entry); err == nil {
			addr = addr.Unmap().WithZone("")
			p.Prefixes = append(p.Prefixes, netip.PrefixFrom(addr, addr.BitLen()))
			continue
		}
		host := strings.ToLower(entry)
		if !hostnamePattern.MatchString(host) {
			return Proxies{}, fmt.Errorf("%q is not an IP address, CIDR or host name", entry)
		}
		p.Hosts = append(p.Hosts, host)
	}
	return p, nil
}

// unmapPrefix, IPv4'e eşlenmiş bir IPv6 aralığını (::ffff:10.0.0.0/104) IPv4 karşılığına çevirir; adresler de
// karşılaştırmadan önce Unmap edildiği için aksi halde hiç eşleşmezdi.
func unmapPrefix(p netip.Prefix) netip.Prefix {
	if p.Addr().Is4In6() && p.Bits() >= 96 {
		return netip.PrefixFrom(p.Addr().Unmap(), p.Bits()-96)
	}
	return p
}

const (
	// refreshEvery, çözülmüş host adlarının ne sıklıkla yeniden çözüldüğüdür: container yeniden oluşunca IP'si değişebilir.
	refreshEvery = 30 * time.Second
	// retryEvery, bir ad çözülemediği sürece (ör. açılışta panel container'ı henüz yokken) yeniden deneme aralığıdır;
	// bu sürede proxy'den gelen istekler proxy'nin IP'siyle sayılır, bu yüzden kısa tutulur.
	retryEvery = 2 * time.Second
	// minTriggeredGap, tanınmayan bir eşten gelen X-Forwarded-For'lu isteklerin tetiklediği çözümler arasındaki en kısa
	// aralıktır: başlığı herkes gönderebildiği için tetikleme DNS'e en fazla bu sıklıkta yansır.
	minTriggeredGap = 2 * time.Second
	lookupTimeout   = 5 * time.Second
)

// Resolver, istemci IP'sini TRUSTED_PROXIES'e göre belirler. Sıfır değeri kullanılamaz; New ile kurulur.
type Resolver struct {
	proxies Proxies
	lookup  func(ctx context.Context, host string) ([]netip.Addr, error)

	// Zamanlama; testler kısaltır.
	refreshEvery, retryEvery, minTriggeredGap time.Duration

	// trigger, Run'a hemen yeniden çözmesini söyler (bkz. ClientIP); tek elemanlı, dolu ise yeni tetikleme düşer.
	trigger chan struct{}

	mu       sync.RWMutex
	resolved map[string][]netip.Addr // host adı -> son başarılı çözümün adresleri
	failing  map[string]bool         // son çözümü başarısız olanlar
}

// New, bir Resolver kurar. Host adları Run başlayana (ve ilk çözüm başarılı olana) kadar güvenilir sayılmaz: güvenli
// varsayılan, başlığı yok saymaktır.
func New(p Proxies) *Resolver {
	return &Resolver{
		proxies:         p,
		lookup:          systemLookup,
		refreshEvery:    refreshEvery,
		retryEvery:      retryEvery,
		minTriggeredGap: minTriggeredGap,
		trigger:         make(chan struct{}, 1),
		resolved:        map[string][]netip.Addr{},
		failing:         map[string]bool{},
	}
}

func systemLookup(ctx context.Context, host string) ([]netip.Addr, error) {
	return net.DefaultResolver.LookupNetIP(ctx, "ip", host)
}

// Run, ctx iptal edilene kadar host adlarını çözer: hepsi çözülmüşse refreshEvery'de bir, çözülemeyen varsa retryEvery'de
// bir ve ClientIP tetiklediğinde (en fazla minTriggeredGap'te bir) hemen. Host adı yoksa hemen döner. Kendi goroutine'inde
// çalıştırın.
func (r *Resolver) Run(ctx context.Context) {
	if len(r.proxies.Hosts) == 0 {
		return
	}
	timer := time.NewTimer(0)
	defer timer.Stop()
	var last time.Time
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		case <-r.trigger:
			if time.Since(last) < r.minTriggeredGap {
				continue // az önce çözüldü; düzenli zamanlama ya da sonraki tetikleme yeter
			}
		}
		r.Refresh(ctx)
		last = time.Now()
		if !timer.Stop() {
			select {
			case <-timer.C:
			default:
			}
		}
		timer.Reset(r.nextDelay())
	}
}

// nextDelay, çözülemeyen (hiç çözülmemiş ya da son denemesi başarısız) bir ad varsa kısa, yoksa uzun aralıktır.
func (r *Resolver) nextDelay() time.Duration {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, host := range r.proxies.Hosts {
		if _, ok := r.resolved[host]; !ok || r.failing[host] {
			return r.retryEvery
		}
	}
	return r.refreshEvery
}

// requestRefresh, Run'dan bloklamadan hemen bir çözüm ister.
func (r *Resolver) requestRefresh() {
	select {
	case r.trigger <- struct{}{}:
	default:
	}
}

// Refresh, host adlarını bir kez çözer. Başarısız bir çözüm o adın son bilinen adreslerini korur (DNS'in anlık
// aksaması güvenilir proxy'yi düşürmesin); hata ve düzelme yalnızca durum değiştiğinde loglanır.
func (r *Resolver) Refresh(ctx context.Context) {
	for _, host := range r.proxies.Hosts {
		lookupCtx, cancel := context.WithTimeout(ctx, lookupTimeout)
		addrs, err := r.lookup(lookupCtx, host)
		cancel()

		r.mu.Lock()
		switch {
		case err != nil:
			if !r.failing[host] {
				slog.WarnContext(ctx, "trusted proxy cannot be resolved (X-Forwarded-For from it is ignored until it can)", "proxy", host, "err", err)
			}
			r.failing[host] = true
		default:
			for i := range addrs {
				addrs[i] = addrs[i].Unmap().WithZone("")
			}
			slices.SortFunc(addrs, func(a, b netip.Addr) int { return a.Compare(b) })
			if r.failing[host] || !slices.Equal(r.resolved[host], addrs) {
				slog.InfoContext(ctx, "trusted proxy resolved", "proxy", host, "addrs", fmt.Sprint(addrs))
			}
			r.resolved[host] = addrs
			r.failing[host] = false
		}
		r.mu.Unlock()
	}
}

func (r *Resolver) trusted(addr netip.Addr) bool {
	for _, p := range r.proxies.Prefixes {
		if p.Contains(addr) {
			return true
		}
	}
	if len(r.proxies.Hosts) == 0 {
		return false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, addrs := range r.resolved {
		if slices.Contains(addrs, addr) {
			return true
		}
	}
	return false
}

// ClientIP, isteğin istemci IP'sini döndürür. Eş güvenilir bir proxy değilse eşin kendisidir. Güvenilirse
// X-Forwarded-For sağdan sola okunur ve güvenilir proxy olmayan ilk adres döner (sağdaki girdileri bizim
// proxy'lerimiz yazar; soldakileri istemci uydurabilir). Başlık yoksa, bozuksa ya da tamamı güvenilir
// proxy'lerden oluşuyorsa eş döner.
func (r *Resolver) ClientIP(req *http.Request) string {
	peer, ok := parseAddr(req.RemoteAddr)
	if !ok {
		return hostOnly(req.RemoteAddr)
	}
	if r == nil {
		return peer.String()
	}
	if !r.trusted(peer) {
		// Proxy'ler X-Forwarded-For'u her zaman gönderir: başlıklı bir istek tanınmayan bir eşten geldiyse bu, IP'si
		// değişmiş (yeniden oluşturulmuş) bir proxy container'ı olabilir. Adı hemen yeniden çözdür; bu istek ve
		// çözüm bitene kadar gelenler eşin IP'siyle sayılır (güvenli taraf).
		if len(r.proxies.Hosts) > 0 && req.Header.Get("X-Forwarded-For") != "" {
			r.requestRefresh()
		}
		return peer.String()
	}
	var hops []string
	for _, line := range req.Header.Values("X-Forwarded-For") {
		hops = append(hops, strings.Split(line, ",")...)
	}
	for i := len(hops) - 1; i >= 0; i-- {
		addr, ok := parseAddr(strings.TrimSpace(hops[i]))
		if !ok {
			break // bozuk bir girdinin solundakilere güvenilemez
		}
		if !r.trusted(addr) {
			return addr.String()
		}
	}
	return peer.String()
}

// parseAddr, "ip", "ip:port" ya da "[ipv6]:port" biçimindeki bir adresi olağan biçime getirir.
func parseAddr(s string) (netip.Addr, bool) {
	if addr, err := netip.ParseAddr(s); err == nil {
		return addr.Unmap().WithZone(""), true
	}
	if ap, err := netip.ParseAddrPort(s); err == nil {
		return ap.Addr().Unmap().WithZone(""), true
	}
	return netip.Addr{}, false
}

// hostOnly, ayrıştırılamayan bir RemoteAddr için eski davranıştır: varsa portu atar.
func hostOnly(remoteAddr string) string {
	if host, _, err := net.SplitHostPort(remoteAddr); err == nil {
		return host
	}
	return remoteAddr
}
