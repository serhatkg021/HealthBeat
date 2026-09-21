package collector

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

const dockerSocketPath = "/var/run/docker.sock"

const maxConcurrentContainerSamples = 16

type DockerContainer struct {
	Name          string
	Image         string
	Status        string
	CPUPct        float64
	RAMMB         float64
	RestartCount  int
	UptimeSeconds int64
}

// DockerCollector yerel Docker Engine API'siyle unix soketi üzerinden konuşur. Soket yoksa
// (Docker kurulu değil ya da çalışmıyor) Sample hata yerine boş liste döndürür —
// Docker'sız sunucularda metrik toplama bozulmamalıdır (docs/MIMARI.md bölüm 8).
type DockerCollector struct {
	httpClient *http.Client
	available  bool
}

func NewDockerCollector() *DockerCollector {
	return newDockerCollector(dockerSocketPath)
}

func newDockerCollector(socketPath string) *DockerCollector {
	if _, err := os.Stat(socketPath); err != nil {
		return &DockerCollector{available: false}
	}
	return &DockerCollector{
		available: true,
		httpClient: &http.Client{
			Timeout: 5 * time.Second,
			Transport: &http.Transport{
				DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
					var d net.Dialer
					return d.DialContext(ctx, "unix", socketPath)
				},
			},
		},
	}
}

func (d *DockerCollector) Sample(ctx context.Context) ([]DockerContainer, error) {
	if !d.available {
		return nil, nil
	}

	summaries, err := d.listContainers(ctx)
	if err != nil {
		return nil, err
	}

	// Çalışan her container'ın stats çağrısı ~2 sn sürer (daemon iki kez örnek alır); sıralı
	// toplama doğrusal büyür ve poll zaman aşımlarını aşar.
	sampled := make([]*DockerContainer, len(summaries))
	sem := make(chan struct{}, maxConcurrentContainerSamples)
	var wg sync.WaitGroup
	for i, s := range summaries {
		wg.Add(1)
		sem <- struct{}{}
		go func(i int, s containerSummary) {
			defer wg.Done()
			defer func() { <-sem }()
			sampled[i] = d.sampleContainer(ctx, s)
		}(i, s)
	}
	wg.Wait()

	result := make([]DockerContainer, 0, len(summaries))
	for _, c := range sampled {
		if c != nil {
			result = append(result, *c)
		}
	}
	return result, nil
}

// Version, Docker daemon'ının sürümünü döndürür ("" = Docker yok/erişilemiyor/yanıt bozuk).
// Envanter için (bkz. HostInfoCollector); hata durumunda sessizce boş döner.
func (d *DockerCollector) Version(ctx context.Context) string {
	if !d.available {
		return ""
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	body, err := d.get(ctx, "/version")
	if err != nil {
		return ""
	}
	var v struct {
		Version string `json:"Version"`
	}
	if json.Unmarshal(body, &v) != nil {
		return ""
	}
	return strings.TrimSpace(v.Version)
}

// sampleContainer, container liste ile inspect arasında kaybolduysa nil döndürür.
func (d *DockerCollector) sampleContainer(ctx context.Context, s containerSummary) *DockerContainer {
	detail, err := d.inspect(ctx, s.ID)
	if err != nil {
		return nil
	}
	c := &DockerContainer{
		Name:         strings.TrimPrefix(firstOrEmpty(s.Names), "/"),
		Image:        s.Image,
		Status:       detail.State.Status,
		RestartCount: detail.RestartCount,
	}
	if detail.State.Status == "running" {
		if startedAt, err := time.Parse(time.RFC3339Nano, detail.State.StartedAt); err == nil {
			c.UptimeSeconds = int64(time.Since(startedAt).Seconds())
		}
		if stats, err := d.stats(ctx, s.ID); err == nil {
			c.CPUPct = computeContainerCPUPercent(stats)
			c.RAMMB = float64(stats.MemoryStats.Usage) / (1024 * 1024)
		}
	}
	return c
}

func firstOrEmpty(names []string) string {
	if len(names) == 0 {
		return ""
	}
	return names[0]
}

type containerSummary struct {
	ID    string   `json:"Id"`
	Names []string `json:"Names"`
	Image string   `json:"Image"`
}

func (d *DockerCollector) listContainers(ctx context.Context) ([]containerSummary, error) {
	body, err := d.get(ctx, "/containers/json?all=true")
	if err != nil {
		return nil, err
	}
	var summaries []containerSummary
	if err := json.Unmarshal(body, &summaries); err != nil {
		return nil, fmt.Errorf("decode container list: %w", err)
	}
	return summaries, nil
}

type inspectResponse struct {
	RestartCount int `json:"RestartCount"`
	State        struct {
		Status    string `json:"Status"`
		StartedAt string `json:"StartedAt"`
	} `json:"State"`
}

func (d *DockerCollector) inspect(ctx context.Context, id string) (inspectResponse, error) {
	body, err := d.get(ctx, "/containers/"+id+"/json")
	if err != nil {
		return inspectResponse{}, err
	}
	var resp inspectResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return inspectResponse{}, fmt.Errorf("decode container inspect: %w", err)
	}
	return resp, nil
}

type statsResponse struct {
	CPUStats struct {
		CPUUsage struct {
			TotalUsage uint64 `json:"total_usage"`
		} `json:"cpu_usage"`
		SystemCPUUsage uint64 `json:"system_cpu_usage"`
		OnlineCPUs     uint32 `json:"online_cpus"`
	} `json:"cpu_stats"`
	PreCPUStats struct {
		CPUUsage struct {
			TotalUsage uint64 `json:"total_usage"`
		} `json:"cpu_usage"`
		SystemCPUUsage uint64 `json:"system_cpu_usage"`
	} `json:"precpu_stats"`
	MemoryStats struct {
		Usage uint64 `json:"usage"`
	} `json:"memory_stats"`
}

// stream=false, daemon'un ~1 sn arayla iki iç örnek alıp ikisini birden tek bir anlık görüntü
// olarak döndürmesini sağlar — bizim iki kez örneklememize gerek yok.
func (d *DockerCollector) stats(ctx context.Context, id string) (statsResponse, error) {
	body, err := d.get(ctx, "/containers/"+id+"/stats?stream=false")
	if err != nil {
		return statsResponse{}, err
	}
	var resp statsResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return statsResponse{}, fmt.Errorf("decode container stats: %w", err)
	}
	return resp, nil
}

func computeContainerCPUPercent(stats statsResponse) float64 {
	cpuDelta := float64(stats.CPUStats.CPUUsage.TotalUsage) - float64(stats.PreCPUStats.CPUUsage.TotalUsage)
	systemDelta := float64(stats.CPUStats.SystemCPUUsage) - float64(stats.PreCPUStats.SystemCPUUsage)
	if cpuDelta <= 0 || systemDelta <= 0 {
		return 0
	}
	onlineCPUs := stats.CPUStats.OnlineCPUs
	if onlineCPUs == 0 {
		onlineCPUs = 1
	}
	return (cpuDelta / systemDelta) * float64(onlineCPUs) * 100
}

func (d *DockerCollector) get(ctx context.Context, path string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://unix"+path, nil)
	if err != nil {
		return nil, err
	}
	resp, err := d.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("docker api %s: status %d: %s", path, resp.StatusCode, string(body))
	}
	return body, nil
}
