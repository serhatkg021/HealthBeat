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

// inspectMaxAge, durumu değişmeyen bir container'ın inspect önbelleğinin en uzun ömrüdür.
const inspectMaxAge = 5 * time.Minute

// DockerCollector yerel Docker Engine API'siyle unix soketi üzerinden konuşur. Soket yoksa
// (Docker kurulu değil ya da çalışmıyor) Sample hata yerine boş liste döndürür —
// Docker'sız sunucularda metrik toplama bozulmamalıdır (docs/MIMARI.md bölüm 8).
//
// Maliyeti düşük tutmak için iki önbellek tutar: inspect yanıtı (container'ın listedeki durumu değişmedikçe ve
// inspectMaxAge dolmadıkça yeniden sorulmaz) ve container başına son CPU örneği (bir sonraki istatistik "one-shot"
// alınıp CPU farkı agent'ta hesaplanır; daemon'un ~2 sn'lik iki örnekli ölçümü yalnızca ilk kez yapılır).
type DockerCollector struct {
	httpClient *http.Client
	available  bool
	now        func() time.Time

	mu       sync.Mutex
	oneShot  *bool // daemon one-shot istatistiği destekliyor mu (API >= 1.41); nil = henüz sorulmadı
	inspects map[string]cachedInspect
	prevCPU  map[string]containerCPU
}

type cachedInspect struct {
	state string // listedeki State + Status: container yeniden başlayınca ya da durumu değişince değişir
	at    time.Time
	resp  inspectResponse
}

// containerCPU, bir container'ın kümülatif CPU sayaçlarıdır.
type containerCPU struct {
	total, system uint64
}

func NewDockerCollector() *DockerCollector {
	return newDockerCollector(dockerSocketPath)
}

func newDockerCollector(socketPath string) *DockerCollector {
	if _, err := os.Stat(socketPath); err != nil {
		return &DockerCollector{available: false, now: time.Now}
	}
	return &DockerCollector{
		available: true,
		now:       time.Now,
		inspects:  map[string]cachedInspect{},
		prevCPU:   map[string]containerCPU{},
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
	d.mu.Lock()
	known := d.oneShot != nil
	d.mu.Unlock()
	if !known {
		d.Version(ctx) // API sürümünü de öğrenir (one-shot desteği)
	}

	// İlk kez görülen çalışan container'ın stats çağrısı ~2 sn sürer (daemon iki kez örnek alır);
	// sıralı toplama doğrusal büyürdü.
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
	d.forgetGone(summaries)

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
		Version    string `json:"Version"`
		APIVersion string `json:"ApiVersion"`
	}
	if json.Unmarshal(body, &v) != nil {
		return ""
	}
	oneShot := apiAtLeast(v.APIVersion, 1, 41)
	d.mu.Lock()
	d.oneShot = &oneShot
	d.mu.Unlock()
	return strings.TrimSpace(v.Version)
}

// apiAtLeast, "1.43" biçimindeki Docker API sürümünün en az major.minor olup olmadığıdır; okunamayan sürüm hayır sayılır.
func apiAtLeast(v string, major, minor int) bool {
	var ma, mi int
	if _, err := fmt.Sscanf(strings.TrimSpace(v), "%d.%d", &ma, &mi); err != nil {
		return false
	}
	return ma > major || (ma == major && mi >= minor)
}

// forgetGone, listede artık olmayan container'ların önbelleklerini atar.
func (d *DockerCollector) forgetGone(summaries []containerSummary) {
	present := make(map[string]struct{}, len(summaries))
	for _, s := range summaries {
		present[s.ID] = struct{}{}
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	for id := range d.inspects {
		if _, ok := present[id]; !ok {
			delete(d.inspects, id)
		}
	}
	for id := range d.prevCPU {
		if _, ok := present[id]; !ok {
			delete(d.prevCPU, id)
		}
	}
}

// sampleContainer, container liste ile inspect arasında kaybolduysa nil döndürür.
func (d *DockerCollector) sampleContainer(ctx context.Context, s containerSummary) *DockerContainer {
	detail, err := d.cachedInspect(ctx, s)
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
		if cpu, ramMB, ok := d.sampleUsage(ctx, s.ID); ok {
			c.CPUPct, c.RAMMB = cpu, ramMB
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
	// State ("running") ve Status ("Up 2 hours (healthy)") yalnızca inspect önbelleğini geçersiz kılmak için: container
	// yeniden başlayınca, durduğunda ya da sağlık durumu değişince Status metni değişir.
	State  string `json:"State"`
	Status string `json:"Status"`
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

// cachedInspect, container'ın listedeki durumu değişmediyse ve önbellek inspectMaxAge'den genç ise önbellekteki
// inspect yanıtını, değilse yenisini döndürür.
func (d *DockerCollector) cachedInspect(ctx context.Context, s containerSummary) (inspectResponse, error) {
	state := s.State + "|" + s.Status
	d.mu.Lock()
	c, ok := d.inspects[s.ID]
	d.mu.Unlock()
	if ok && c.state == state && d.now().Sub(c.at) < inspectMaxAge {
		return c.resp, nil
	}
	resp, err := d.inspect(ctx, s.ID)
	if err != nil {
		return inspectResponse{}, err
	}
	d.mu.Lock()
	d.inspects[s.ID] = cachedInspect{state: state, at: d.now(), resp: resp}
	d.mu.Unlock()
	return resp, nil
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

// sampleUsage, çalışan bir container'ın CPU yüzdesini ve RAM'ini döndürür. Container'ın önceki bir CPU örneği varsa ve
// daemon destekliyorsa istatistik "one-shot" alınır (milisaniyeler) ve CPU farkı önceki örnekle hesaplanır; yoksa (ilk
// kez görülen container, eski daemon, sayaç geri gitti) daemon'un iki örnekli ölçümü kullanılır (~2 sn).
func (d *DockerCollector) sampleUsage(ctx context.Context, id string) (cpuPct, ramMB float64, ok bool) {
	d.mu.Lock()
	prev, hasPrev := d.prevCPU[id]
	oneShot := d.oneShot != nil && *d.oneShot
	d.mu.Unlock()

	if hasPrev && oneShot {
		if st, err := d.stats(ctx, id, true); err == nil {
			cur := containerCPU{total: st.CPUStats.CPUUsage.TotalUsage, system: st.CPUStats.SystemCPUUsage}
			if cur.total >= prev.total && cur.system > prev.system {
				d.rememberCPU(id, cur)
				st.PreCPUStats.CPUUsage.TotalUsage, st.PreCPUStats.SystemCPUUsage = prev.total, prev.system
				return computeContainerCPUPercent(st), float64(st.MemoryStats.Usage) / (1024 * 1024), true
			}
		}
	}

	st, err := d.stats(ctx, id, false)
	if err != nil {
		return 0, 0, false
	}
	d.rememberCPU(id, containerCPU{total: st.CPUStats.CPUUsage.TotalUsage, system: st.CPUStats.SystemCPUUsage})
	return computeContainerCPUPercent(st), float64(st.MemoryStats.Usage) / (1024 * 1024), true
}

func (d *DockerCollector) rememberCPU(id string, s containerCPU) {
	d.mu.Lock()
	d.prevCPU[id] = s
	d.mu.Unlock()
}

// stream=false, daemon'un ~1 sn arayla iki iç örnek alıp ikisini birden tek bir anlık görüntü olarak döndürmesini
// sağlar. one-shot=true (API >= 1.41) ise beklemeden tek örnek döner (precpu_stats boş gelir; farkı çağıran hesaplar).
func (d *DockerCollector) stats(ctx context.Context, id string, oneShot bool) (statsResponse, error) {
	path := "/containers/" + id + "/stats?stream=false"
	if oneShot {
		path += "&one-shot=true"
	}
	body, err := d.get(ctx, path)
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
