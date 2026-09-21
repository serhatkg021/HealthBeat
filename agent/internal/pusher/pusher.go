// Package pusher, toplanan metrikleri HealthBeat server'ına gönderir (bkz.
// docs/MIMARI.md bölüm 7: POST /api/v1/metrics).
package pusher

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"healthbeat-agent/internal/collector"
	"healthbeat-agent/internal/version"
)

type DiskUsage struct {
	Mount   string  `json:"mount"`
	UsedPct float64 `json:"used_pct"`
	Total   int64   `json:"total"`
	Free    int64   `json:"free"`
	// InodesUsedPct protokol 3'tür; bilinmiyorsa gönderilmez. Core() bunu soyar: bilinmeyen alanı 400 ile
	// reddeden eski server'lar disk girdisindeki alanı da reddederdi (bkz. docs/COMPATIBILITY.md).
	InodesUsedPct *float64 `json:"inodes_used_pct,omitempty"`
}

// PhysicalDisk, bir fiziksel diski ve üzerindeki raporlanan mount'ları anlatır. Mount birden çok
// diske düşebilir (LVM/mdraid), bu yüzden bir mount birden çok diskin listesinde bulunabilir.
type PhysicalDisk struct {
	Name      string   `json:"name"`
	Model     string   `json:"model,omitempty"`
	SizeBytes int64    `json:"size_bytes,omitempty"`
	Kind      string   `json:"kind,omitempty"` // nvme | ssd | hdd
	Mounts    []string `json:"mounts"`
}

type DockerContainer struct {
	Name          string  `json:"name"`
	Image         string  `json:"image"`
	Status        string  `json:"status"`
	CPUPct        float64 `json:"cpu_pct"`
	RAMMB         float64 `json:"ram_mb"`
	RestartCount  int     `json:"restart_count"`
	UptimeSeconds int64   `json:"uptime_seconds"`
}

type MetricsPayload struct {
	CPUUsagePct float64     `json:"cpu_usage_pct"`
	RAMUsagePct float64     `json:"ram_usage_pct"`
	Disk        []DiskUsage `json:"disk"`
	// CPUCores/RAMTotalMB donanım toplamlarıdır; 0 = "bilinmiyor" ve gönderilmez (server son
	// bilinen değeri korur).
	CPUCores   int   `json:"cpu_cores,omitempty"`
	RAMTotalMB int64 `json:"ram_total_mb,omitempty"`
	// PhysicalDisks boşsa (keşfedilemedi: konteyner, salt sanal dosya sistemleri) gönderilmez.
	PhysicalDisks []PhysicalDisk `json:"physical_disks,omitempty"`
	// HostInfo makine envanteri ve anlık durumudur (protokol 3); toplanamadıysa gönderilmez.
	HostInfo         *collector.HostInfo `json:"host_info,omitempty"`
	DockerContainers []DockerContainer   `json:"docker_containers"`
}

// Core, yalnızca her server sürümünün kabul ettiği çekirdek alanları (CPU, RAM, disk, Docker)
// içeren bir kopya döndürür: donanım özeti gibi sonradan eklenen alanlar çıkarılır. Tam payload'ı
// 400 ile reddeden eski bir server'a karşı geri dönüş için kullanılır (bkz. Compat).
func (m MetricsPayload) Core() MetricsPayload {
	// Disk girdilerinin kopyası: protokol 3'ün eklediği inode alanı çekirdek değildir.
	disks := make([]DiskUsage, len(m.Disk))
	for i, d := range m.Disk {
		disks[i] = DiskUsage{Mount: d.Mount, UsedPct: d.UsedPct, Total: d.Total, Free: d.Free}
	}
	if m.Disk == nil {
		disks = nil
	}
	return MetricsPayload{CPUUsagePct: m.CPUUsagePct, RAMUsagePct: m.RAMUsagePct, Disk: disks, DockerContainers: m.DockerContainers}
}

// ServerInfo, server'ın ingest yanıt başlıklarında bildirdiği sürüm bilgisidir. Başlık yoksa
// (bu sözleşmeyi henüz bilmeyen eski bir server) alanlar boştur.
type ServerInfo struct {
	Version     string // X-HealthBeat-Server-Version
	Protocol    int    // X-HealthBeat-Protocol; 0 = bildirmedi
	LatestAgent string // X-HealthBeat-Latest-Agent: server'ın önerdiği agent sürümü; "" = yok
}

// StatusError, server'ın 204 dışında bir durumla yanıt verdiğini belirtir.
type StatusError struct {
	Status int
	Body   string
}

func (e *StatusError) Error() string { return fmt.Sprintf("server returned %d: %s", e.Status, e.Body) }

type Pusher struct {
	serverURL  string
	hostID     string
	apiToken   string
	httpClient *http.Client

	mu   sync.Mutex
	info ServerInfo
}

// ServerInfo, en son alınan yanıtın bildirdiği server sürümünü döndürür.
func (p *Pusher) ServerInfo() ServerInfo {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.info
}

func (p *Pusher) observe(h http.Header) {
	info := ServerInfo{Version: h.Get(version.HeaderServerVersion), LatestAgent: h.Get(version.HeaderLatestAgent)}
	if n, err := strconv.Atoi(h.Get(version.HeaderProtocol)); err == nil && n > 0 {
		info.Protocol = n
	}
	p.mu.Lock()
	p.info = info
	p.mu.Unlock()
}

// TLSOptions, server sertifikasının nasıl doğrulanacağını söyler.
type TLSOptions struct {
	// RootCAs nil değilse güvenilen TEK otorite kümesidir (özel CA); nil sistemin
	// köklerini kullanmak demektir.
	RootCAs *x509.CertPool
	// InsecureSkipVerify doğrulamayı tamamen kapatır — yalnızca kendinden imzalı bir server'a
	// karşı geliştirme için; RootCAs ile asla birlikte kullanma.
	InsecureSkipVerify bool
}

// New, server sertifikasını tlsOpts'un söylediği gibi doğrulayan bir Pusher kurar.
func New(serverURL, hostID, apiToken string, tlsOpts TLSOptions) *Pusher {
	transport := &http.Transport{
		TLSClientConfig: &tls.Config{
			MinVersion:         tls.VersionTLS12,
			RootCAs:            tlsOpts.RootCAs,
			InsecureSkipVerify: tlsOpts.InsecureSkipVerify, //nolint:gosec // explicit opt-in for development
		},
	}
	return &Pusher{
		serverURL: strings.TrimSuffix(serverURL, "/"),
		hostID:    hostID,
		apiToken:  apiToken,
		httpClient: &http.Client{
			Transport: transport,
			Timeout:   10 * time.Second,
		},
	}
}

func (p *Pusher) Push(ctx context.Context, payload MetricsPayload) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("encode payload: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.serverURL+"/api/v1/metrics", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+p.apiToken)
	req.Header.Set("X-Host-ID", p.hostID)
	// Sürüm bilgisi gövdeye değil başlığa konur: gövdede bilinmeyen bir alan, bu sözleşmeyi henüz
	// bilmeyen eski bir server'da 400 üretirdi; başlıklar ise sessizce yok sayılır.
	req.Header.Set("User-Agent", version.UserAgent())
	req.Header.Set(version.HeaderProtocol, strconv.Itoa(version.Protocol))

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("send request: %w", err)
	}
	defer resp.Body.Close()
	p.observe(resp.Header)

	if resp.StatusCode != http.StatusNoContent {
		msg, _ := io.ReadAll(resp.Body)
		return &StatusError{Status: resp.StatusCode, Body: strings.TrimSpace(string(msg))}
	}
	return nil
}
