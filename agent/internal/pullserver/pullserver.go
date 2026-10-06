// Package pullserver, agent'ın pull modu tarafını uygular: HealthBeat server'ının
// zamanlanmış olarak çağırdığı yerel bir HTTPS API'si (bkz. docs/MIMARI.md bölüm 2, 5,
// 6). Her istek izin verilen bir server IP'sinden gelmeli ve paylaşılan pull_secret'ı sunmalı
// — ikisi de zorunlu, isteğe bağlı sertleştirme değil.
package pullserver

import (
	"context"
	"crypto/subtle"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"healthbeat-agent/internal/config"
	"healthbeat-agent/internal/report"
	"healthbeat-agent/internal/version"
)

type Server struct {
	cfg      *config.Config
	builder  *report.Builder
	interval *intervalTracker

	allowedIPs  map[string]struct{}
	allowedNets []*net.IPNet

	httpServer *http.Server
}

func New(cfg *config.Config) (*Server, error) {
	interval := newIntervalTracker(time.Now)
	s := &Server{
		cfg:        cfg,
		builder:    report.New(cfg.DiskMounts, interval.Interval),
		interval:   interval,
		allowedIPs: map[string]struct{}{},
	}

	for _, entry := range cfg.AllowedServerIPs {
		if strings.Contains(entry, "/") {
			_, ipnet, err := net.ParseCIDR(entry)
			if err != nil {
				return nil, fmt.Errorf("invalid allowed_server_ips entry %q: %w", entry, err)
			}
			s.allowedNets = append(s.allowedNets, ipnet)
			continue
		}
		ip := net.ParseIP(entry)
		if ip == nil {
			return nil, fmt.Errorf("invalid allowed_server_ips entry %q", entry)
		}
		s.allowedIPs[ip.String()] = struct{}{}
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET "+cfg.PullEndpoint, s.handleStatus)
	s.httpServer = &http.Server{
		Addr:    cfg.ListenAddr,
		Handler: mux,
		// Bunlar olmadan bir karşı taraf bağlantıları sonsuza dek açık tutabilir (Slowloris).
		// ReadTimeout TLS el sıkışmasını da sınırlar. Yanıt yavaş kaynakları beklemez (arka planda
		// toplanırlar); WriteTimeout yalnızca açılıştaki ilk bekleyişi ve yavaş bir istemciyi kapsar.
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    16 << 10,
	}

	return s, nil
}

func (s *Server) isAllowed(remoteAddr string) bool {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		host = remoteAddr
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}
	if _, ok := s.allowedIPs[ip.String()]; ok {
		return true
	}
	for _, n := range s.allowedNets {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	if !s.isAllowed(r.RemoteAddr) {
		http.Error(w, "yetkiniz yok", http.StatusForbidden)
		return
	}

	token, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	// Burada düz eşitlik sorun değil (hash karşılaştırması değil): iki taraf da bellekte kısa
	// ömürlü aynı düz metni tutar ve ConstantTimeCompare yalnızca secret uzunluğunu/içeriğini
	// yanıt süresi üzerinden sızdırmayı önler.
	if !ok || subtle.ConstantTimeCompare([]byte(token), []byte(s.cfg.PullSecret)) != 1 {
		http.Error(w, "kimlik doğrulaması başarısız", http.StatusUnauthorized)
		return
	}

	s.interval.Observe()
	ctx := r.Context()
	s.builder.WaitReady(ctx, report.FirstReportWait) // yalnızca açılıştan hemen sonraki ilk isteklerde bekler
	payload := s.builder.Build(ctx)

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set(version.HeaderAgentVersion, version.Version)
	w.Header().Set(version.HeaderProtocol, strconv.Itoa(version.Protocol))
	if err := json.NewEncoder(w).Encode(payload); err != nil {
		log.Printf("encode status response: %v", err)
	}
}

// Start, raporun arka plan toplayıcılarını ctx bitene kadar çalıştırır; ListenAndServe'den önce çağrılır.
func (s *Server) Start(ctx context.Context) {
	s.builder.Start(ctx)
}

func (s *Server) ListenAndServe() error {
	cert, err := tls.LoadX509KeyPair(s.cfg.TLSCertFile, s.cfg.TLSKeyFile)
	if err != nil {
		return fmt.Errorf("load tls key pair: %w", err)
	}
	ln, err := net.Listen("tcp", s.cfg.ListenAddr)
	if err != nil {
		return err
	}
	log.Printf("HealthBeat agent %s (protocol %d, pull mode) listening on %s%s", version.Version, version.Protocol, s.cfg.ListenAddr, s.cfg.PullEndpoint)

	// İzin listesi bağlantı kabul edilirken uygulanır: başka bir adresten gelen bağlantı, TLS
	// el sıkışmasından ya da tek bir başlık baytı okunmadan önce kapatılır; böylece yabancılar
	// kaynak bağlayamaz. handleStatus ikinci katman olarak yeniden denetler.
	guarded := &allowListener{Listener: ln, allowed: s.isAllowed}
	tlsLn := tls.NewListener(guarded, &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12})
	return s.httpServer.Serve(tlsLn)
}

// allowListener, uzak adresi allowed()'i geçemeyen bağlantıları bırakır.
type allowListener struct {
	net.Listener
	allowed func(remoteAddr string) bool

	mu        sync.Mutex
	rejected  int
	lastLogAt time.Time
}

func (l *allowListener) Accept() (net.Conn, error) {
	for {
		conn, err := l.Listener.Accept()
		if err != nil {
			return nil, err
		}
		if l.allowed(conn.RemoteAddr().String()) {
			return conn, nil
		}
		conn.Close()
		l.noteRejected(conn.RemoteAddr().String())
	}
}

// noteRejected dakikada en fazla bir kez loglar; böylece bir tarama logu doldurmaz.
func (l *allowListener) noteRejected(remote string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.rejected++
	if time.Since(l.lastLogAt) >= time.Minute {
		log.Printf("pull server: dropped %d connection(s) from non-whitelisted addresses (latest: %s)", l.rejected, remote)
		l.rejected, l.lastLogAt = 0, time.Now()
	}
}

func (s *Server) Shutdown(ctx context.Context) error {
	return s.httpServer.Shutdown(ctx)
}
