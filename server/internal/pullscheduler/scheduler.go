// Package pullscheduler, pull modu host'ları periyodik olarak poll eder (bkz.
// docs/MIMARI.md bölüm 2 ve 6: "Server, belirlenen aralıkta host'a
// bağlanıp durumu sorgular"); push modu alımının kullandığı depolama yolunun (store.Metrics,
// store.Hosts.MarkOnline) tıpatıp aynısını kullanır.
package pullscheduler

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"healthbeat-server/internal/alertengine"
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
	hosts   *store.Hosts
	metrics *store.Metrics
	engine  *alertengine.Engine

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
	return &Scheduler{
		hosts:   store.NewHosts(pool, box),
		metrics: store.NewMetrics(pool),
		engine:  engine,
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
		log.Printf("pull scheduler: verifying pull hosts' TLS certificates against PULL_CA_CERT_FILE")
	} else {
		log.Printf("pull scheduler: pull hosts' TLS certificates are NOT verified (PULL_CA_CERT_FILE is not set); they are authenticated by the shared secret and the agent's IP allow-list")
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
		log.Printf("pull scheduler: list pull hosts: %v", err)
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
	reqCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	url := fmt.Sprintf("https://%s:%d%s", c.IP, c.PullPort, c.PullEndpoint)
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, url, nil)
	if err != nil {
		log.Printf("pull scheduler: build request for host %s: %v", c.ID, err)
		return
	}
	req.Header.Set("Authorization", "Bearer "+c.PullSecret)
	req.Header.Set("User-Agent", version.UserAgent())
	req.Header.Set(version.HeaderServerVersion, version.Version)

	resp, err := s.httpClient.Do(req)
	if err != nil {
		log.Printf("pull scheduler: request to host %s failed: %v", c.ID, err)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		log.Printf("pull scheduler: host %s returned %d: %s", c.ID, resp.StatusCode, string(body))
		return
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxPullResponseBytes))
	if err != nil {
		log.Printf("pull scheduler: read response from host %s: %v", c.ID, err)
		return
	}
	payload, unknown, err := model.ParseMetricsIngest(body)
	if err != nil {
		log.Printf("pull scheduler: decode response from host %s: %v", c.ID, err)
		return
	}
	if err := payload.Validate(); err != nil {
		log.Printf("pull scheduler: rejecting report from host %s: %v", c.ID, err)
		return
	}

	if err := s.metrics.Insert(ctx, c.ID, payload.CPUUsagePct, payload.RAMUsagePct, payload.Disk); err != nil {
		log.Printf("pull scheduler: store metrics for host %s: %v", c.ID, err)
		return
	}
	if err := s.metrics.ReplaceDockerContainers(ctx, c.ID, payload.DockerContainers); err != nil {
		log.Printf("pull scheduler: store docker containers for host %s: %v", c.ID, err)
	}
	if err := s.hosts.MarkOnline(ctx, c.ID, payload.Hardware(), agentInfo(resp.Header, unknown)); err != nil {
		log.Printf("pull scheduler: mark host %s online: %v", c.ID, err)
	}

	s.engine.ResolveOffline(ctx, c.ID, c.OrganizationID)
	s.engine.EvaluateDocker(ctx, c.ID, c.OrganizationID, payload.DockerContainers)
	s.engine.EvaluateMetrics(ctx, c.ID, c.OrganizationID, payload.CPUUsagePct, payload.RAMUsagePct, payload.Disk)
}

// agentInfo, pull yanıtının başlıklarından agent sürümünü okur; unknown, yanıt gövdesindeki
// server'ın tanımadığı alan adlarıdır.
func agentInfo(h http.Header, unknown []string) model.AgentInfo {
	info := model.ParseAgentInfo(h.Get(version.HeaderAgentVersion), "", h.Get(version.HeaderProtocol))
	info.UnsupportedFields = unknown
	return info
}
