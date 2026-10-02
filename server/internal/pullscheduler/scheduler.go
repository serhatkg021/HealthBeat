// Package pullscheduler, pull modu host'ları periyodik olarak poll eder (bkz.
// docs/MIMARI.md bölüm 2 ve 6: "Server, belirlenen aralıkta host'a
// bağlanıp durumu sorgular"); raporu push modu alımıyla aynı yoldan kaydeder (internal/ingest).
package pullscheduler

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"healthbeat-server/internal/alertengine"
	"healthbeat-server/internal/ingest"
	"healthbeat-server/internal/logging"
	"healthbeat-server/internal/model"
	"healthbeat-server/internal/secretbox"
	"healthbeat-server/internal/store"
	"healthbeat-server/internal/version"
)

// dueCheckInterval, kendi interval_seconds süresi dolmuş host'lar için ne sıklıkla tarama
// yaptığımızdır. Etkin poll ayrıntısı için bir tabandır — bundan küçük interval_seconds ile
// yapılandırılmış bir host o kadar sık poll edilmez. Bilinçli bir sadeleştirmedir: host başına
// zamanlayıcı yerine tek ticker, izleme için gereken çözünürlükten fazlasını zaten sağlar.
const dueCheckInterval = 5 * time.Second

type Scheduler struct {
	hosts  *store.Hosts
	ingest *ingest.Service

	httpClient  *http.Client
	verifiesTLS bool // poll edilen host'ların sertifikalarının doğrulanıp doğrulanmadığı (bkz. New)

	mu         sync.Mutex
	lastPolled map[uuid.UUID]time.Time
	// inFlight, önceki poll'u bitmemiş host'ları tutar. Kısa interval'li yavaş ya da erişilemeyen
	// bir host (10 sn zaman aşımı) aksi halde üst üste binen poll'lar biriktirirdi.
	inFlight map[uuid.UUID]struct{}

	wg sync.WaitGroup // bekleyen poll'lar; Run'ın kapanışta boşalmasını sağlar
}

// maxPullResponseBytes, poll edilen bir host'ın geri gönderebileceği miktarı sınırlar (meşru
// bir rapor birkaç KiB'dir); ele geçirilmiş bir host belleği tüketememeli.
const maxPullResponseBytes = 1 << 20

// New, scheduler'ı kurar. rootCAs poll edilen bir host'ın sertifikasının nasıl denetleneceğini
// belirler:
//   - nil değil: sertifika BU otoritelerden birine zincirlenmeli (ve yalnızca bunlara) ve
//     host'ın IP adresi için geçerli olmalı — host IP ile poll edilir, bu yüzden
//     sertifikasında o IP bir subject alternative name olarak bulunmalı;
//   - nil: doğrulama KAPALI. Pull host'lar bu durumda tipik olarak kendinden imzalı
//     sertifika çalıştırır ve bunun yerine paylaşılan secret ile agent'ın kaynak-IP izin
//     listesiyle doğrulanır. Doğrulamayı açmak için PULL_CA_CERT_FILE'ı ayarla.
func New(pool *pgxpool.Pool, engine *alertengine.Engine, box *secretbox.Box, rootCAs *x509.CertPool) *Scheduler {
	tlsConfig := &tls.Config{MinVersion: tls.VersionTLS12}
	if rootCAs != nil {
		tlsConfig.RootCAs = rootCAs
	} else {
		tlsConfig.InsecureSkipVerify = true //nolint:gosec // documented default, see above
	}
	hosts := store.NewHosts(pool, box)
	return &Scheduler{
		hosts:  hosts,
		ingest: ingest.New(store.NewMetrics(pool), hosts, engine),
		httpClient: &http.Client{
			Timeout:   10 * time.Second,
			Transport: &http.Transport{TLSClientConfig: tlsConfig},
		},
		verifiesTLS: rootCAs != nil,
		lastPolled:  map[uuid.UUID]time.Time{},
		inFlight:    map[uuid.UUID]struct{}{},
	}
}

func (s *Scheduler) Run(ctx context.Context) {
	if s.verifiesTLS {
		slog.InfoContext(ctx, "pull scheduler: verifying pull hosts' TLS certificates against PULL_CA_CERT_FILE")
	} else {
		slog.WarnContext(ctx, "pull scheduler: pull hosts' TLS certificates are NOT verified (PULL_CA_CERT_FILE is not set); they are authenticated by the shared secret and the agent's IP allow-list")
	}
	ticker := time.NewTicker(dueCheckInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			s.wg.Wait()
			return
		case <-ticker.C:
			s.pollDueHosts(ctx)
		}
	}
}

func (s *Scheduler) pollDueHosts(ctx context.Context) {
	pullHosts, err := s.hosts.ListPullHosts(ctx)
	if err != nil {
		slog.ErrorContext(ctx, "pull scheduler: list pull hosts", "err", err)
		return
	}

	now := time.Now()
	current := make(map[uuid.UUID]struct{}, len(pullHosts))
	for _, c := range pullHosts {
		current[c.ID] = struct{}{}

		s.mu.Lock()
		last, seen := s.lastPolled[c.ID]
		_, busy := s.inFlight[c.ID]
		due := !busy && (!seen || now.Sub(last) >= time.Duration(c.IntervalSeconds)*time.Second)
		if due {
			s.lastPolled[c.ID] = now
			s.inFlight[c.ID] = struct{}{}
		}
		s.mu.Unlock()

		if due {
			s.wg.Add(1)
			go func(c store.PullHostInfo) {
				defer s.wg.Done()
				defer func() {
					s.mu.Lock()
					delete(s.inFlight, c.ID)
					s.mu.Unlock()
				}()
				// Bir host'un poll'undaki panic yalnızca o turu düşürür; host inFlight'tan çıkar ve sonraki turda yeniden denenir.
				defer logging.Recover(ctx, "pull poll "+c.ID.String())
				s.pollOne(ctx, c)
			}(c)
		}
	}

	// Silinen ya da push moduna geçirilen host'ları unut.
	s.mu.Lock()
	for id := range s.lastPolled {
		if _, ok := current[id]; !ok {
			delete(s.lastPolled, id)
		}
	}
	s.mu.Unlock()
}

func (s *Scheduler) pollOne(ctx context.Context, c store.PullHostInfo) {
	// Her poll'un kendi korelasyon kimliği vardır (push'taki request_id gibi): bu poll sırasında alert motorunun yazdığı
	// satırlar da aynı kimliği taşır.
	info := &logging.RequestInfo{ID: "poll-" + uuid.NewString()}
	info.SetHost(c.ID.String())
	ctx = logging.WithRequestInfo(ctx, info)

	reqCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	url := fmt.Sprintf("https://%s:%d%s", c.IP, c.PullPort, c.PullEndpoint)
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, url, nil)
	if err != nil {
		slog.ErrorContext(ctx, "pull scheduler: build request", "host_id", c.ID.String(), "err", err)
		return
	}
	req.Header.Set("Authorization", "Bearer "+c.PullSecret)
	req.Header.Set("User-Agent", version.UserAgent())
	req.Header.Set(version.HeaderServerVersion, version.Version)

	resp, err := s.httpClient.Do(req)
	if err != nil {
		slog.WarnContext(ctx, "pull scheduler: request to host failed", "host_id", c.ID.String(), "err", err)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		// Bozuk ya da kötü niyetli bir agent logu şişirmesin: gövdenin yalnızca başı yazılır.
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		slog.WarnContext(ctx, "pull scheduler: host returned an error", "host_id", c.ID.String(), "status", resp.StatusCode, "body", string(body))
		return
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxPullResponseBytes))
	if err != nil {
		slog.WarnContext(ctx, "pull scheduler: read response", "host_id", c.ID.String(), "err", err)
		return
	}
	payload, unknown, err := ingest.Decode(body)
	if errors.Is(err, ingest.ErrMalformed) {
		slog.WarnContext(ctx, "pull scheduler: decode response", "host_id", c.ID.String(), "err", err)
		return
	}
	if err != nil {
		slog.WarnContext(ctx, "pull scheduler: rejecting report", "host_id", c.ID.String(), "err", err)
		return
	}

	err = s.ingest.Record(ctx, ingest.Report{
		HostID: c.ID, OrgID: c.OrganizationID, Payload: payload, Agent: agentInfo(resp.Header, unknown), Source: ingest.SourcePull,
	})
	if err != nil {
		slog.ErrorContext(ctx, "pull scheduler: store metrics", "host_id", c.ID.String(), "err", err)
	}
}

// agentInfo, pull yanıtının başlıklarından agent sürümünü okur; unknown, yanıt gövdesindeki
// server'ın tanımadığı alan adlarıdır.
func agentInfo(h http.Header, unknown []string) model.AgentInfo {
	info := model.ParseAgentInfo(h.Get(version.HeaderAgentVersion), "", h.Get(version.HeaderProtocol))
	info.UnsupportedFields = unknown
	return info
}

// PolledHost, zamanlayıcının bir pull host hakkında bellekte tuttuğudur (bkz. Snapshot).
type PolledHost struct {
	HostID       uuid.UUID `json:"host_id"`
	LastPolledAt time.Time `json:"last_polled_at"`
	// InFlight, sorgusu şu an süren host'tur (yanıt bekleniyor).
	InFlight bool `json:"in_flight"`
}

// Snapshot, zamanlayıcının salt okunur anlık görüntüsüdür (Sistem Araçları → Cache Durumu).
type Snapshot struct {
	// VerifiesTLS, sorgulanan host'ların sertifikalarının doğrulanıp doğrulanmadığıdır (PULL_CA_CERT_FILE).
	VerifiesTLS bool         `json:"verifies_tls"`
	Hosts       []PolledHost `json:"hosts"`
}

// Snapshot, bilinen pull host'ları en son sorgulanan önce döndürür.
func (s *Scheduler) Snapshot() Snapshot {
	snap := Snapshot{VerifiesTLS: s.verifiesTLS, Hosts: []PolledHost{}}
	s.mu.Lock()
	for id, at := range s.lastPolled {
		_, busy := s.inFlight[id]
		snap.Hosts = append(snap.Hosts, PolledHost{HostID: id, LastPolledAt: at, InFlight: busy})
	}
	s.mu.Unlock()
	slices.SortFunc(snap.Hosts, func(a, b PolledHost) int {
		if c := b.LastPolledAt.Compare(a.LastPolledAt); c != 0 {
			return c
		}
		return strings.Compare(a.HostID.String(), b.HostID.String())
	})
	return snap
}
