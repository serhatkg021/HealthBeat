package store

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"math"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"healthbeat-server/internal/model"
)

type Metrics struct {
	pool *pgxpool.Pool
}

func NewMetrics(pool *pgxpool.Pool) *Metrics {
	return &Metrics{pool: pool}
}

func (s *Metrics) Insert(ctx context.Context, hostID uuid.UUID, cpuPct, ramPct float64, disk []model.DiskUsage) error {
	if disk == nil {
		disk = []model.DiskUsage{}
	}
	diskJSON, err := json.Marshal(disk)
	if err != nil {
		return err
	}

	_, err = s.pool.Exec(ctx,
		`INSERT INTO metrics (host_id, cpu_usage_pct, ram_usage_pct, disk_json) VALUES ($1, $2, $3, $4)
		 ON CONFLICT (host_id, recorded_at) DO NOTHING`,
		hostID, cpuPct, ramPct, string(diskJSON),
	)
	return err
}

// Makul bir raporun asla aşamayacağı sınırlar; bunların dışındaki değerler hatalı ya da
// düşmanca bir agent'tan gelir ve tek bir container'ın veritabanının tüm raporu reddetmesine
// (ve böylece kaybetmesine) yol açmaması için kırpılır.
const (
	maxContainerCPUPct = 1_000_000.0 // NUMERIC(9,2) 9.999.999,99'a kadar tutar
	maxContainerRAMMB  = 1_000_000_000.0
)

func clamp(v, lo, hi float64) float64 {
	if v != v { // NaN
		return lo
	}
	return math.Min(math.Max(v, lo), hi)
}

// sanitizeContainer sayısal alanları şemanın kabul ettiği aralığa kırpar ve durumu şemanın
// reddedeceği bir container için false bildirir.
func sanitizeContainer(c model.DockerContainerReport) (model.DockerContainerReport, bool) {
	if !model.ValidDockerStatus(c.Status) {
		return c, false
	}
	c.CPUPct = clamp(c.CPUPct, 0, maxContainerCPUPct)
	c.RAMMB = clamp(c.RAMMB, 0, maxContainerRAMMB)
	if c.RestartCount < 0 {
		c.RestartCount = 0
	}
	if c.UptimeSeconds < 0 {
		c.UptimeSeconds = 0
	}
	return c, true
}

// ReplaceDockerContainers, bir host için saklanan container listesini son rapora eşitler:
// raporlanan container'lar upsert edilir (host+ad başına bir satır; böylece agent'lar ne
// kadar sık rapor verirse versin tablo filo kadar küçük kalır) ve artık raporlanmayanlar
// kaldırılır. Boş bir rapor bu yüzden listeyi temizler — tüm container'ları durdurmuş ya da
// hiç Docker'ı olmayan bir sunucu hiç göstermez.
func (s *Metrics) ReplaceDockerContainers(ctx context.Context, hostID uuid.UUID, containers []model.DockerContainerReport) error {
	// Kararlı bir sıra, aynı host için eşzamanlı raporlar arasında kilit sırası kilitlenmelerini
	// önler; yinelenen adlardan sonuncusunu tutmak "en yeni kazanır"ı yansıtır.
	byName := make(map[string]model.DockerContainerReport, len(containers))
	for _, c := range containers {
		c, ok := sanitizeContainer(c)
		if !ok {
			log.Printf("docker report for host %s: skipping container %q with unknown status %q", hostID, c.Name, c.Status)
			continue
		}
		byName[c.Name] = c
	}
	names := make([]string, 0, len(byName))
	for n := range byName {
		names = append(names, n)
	}
	sort.Strings(names)

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	for _, name := range names {
		c := byName[name]
		if _, err := tx.Exec(ctx,
			`INSERT INTO docker_containers (host_id, name, image, status, cpu_pct, ram_mb, restart_count, uptime_seconds, reported_at)
			 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, now())
			 ON CONFLICT (host_id, name) DO UPDATE SET
			     image = EXCLUDED.image, status = EXCLUDED.status, cpu_pct = EXCLUDED.cpu_pct,
			     ram_mb = EXCLUDED.ram_mb, restart_count = EXCLUDED.restart_count,
			     uptime_seconds = EXCLUDED.uptime_seconds, reported_at = EXCLUDED.reported_at`,
			hostID, c.Name, c.Image, c.Status, c.CPUPct, c.RAMMB, c.RestartCount, c.UptimeSeconds,
		); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(ctx,
		`DELETE FROM docker_containers WHERE host_id = $1 AND name <> ALL($2::text[])`,
		hostID, names,
	); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// ListByHostAndRange, host detay sayfasının hem "Genel" sekmesindeki anlık kartlarını (son
// nokta) hem geçmiş grafiklerini besler (bkz. docs/MIMARI.md bölüm 7: GET /hosts/:id/metrics?from=&to=).
// Her zaman HAM satırları döndürür — hiçbir kovalama/ortalama yapmaz.
func (s *Metrics) ListByHostAndRange(ctx context.Context, hostID uuid.UUID, from, to time.Time) ([]model.MetricPoint, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT recorded_at, cpu_usage_pct, ram_usage_pct, disk_json
		 FROM metrics
		 WHERE host_id = $1 AND recorded_at BETWEEN $2 AND $3
		 ORDER BY recorded_at`,
		hostID, from, to,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	points := []model.MetricPoint{}
	for rows.Next() {
		var p model.MetricPoint
		var diskJSON []byte
		if err := rows.Scan(&p.Timestamp, &p.CPUUsagePct, &p.RAMUsagePct, &diskJSON); err != nil {
			return nil, err
		}
		if len(diskJSON) > 0 {
			if err := json.Unmarshal(diskJSON, &p.Disk); err != nil {
				return nil, err
			}
		}
		points = append(points, p)
	}
	return points, rows.Err()
}

// PurgeOlderThan, cutoff'tan eski metrik örneklerini en eskiden başlayarak, tek bir ifade
// kilitleri uzun süre tutmasın ya da WAL'ı şişirmesin diye gruplar halinde siler. Kaç satır
// sildiğini döndürür ve ctx biterse erken durur.
func (s *Metrics) PurgeOlderThan(ctx context.Context, cutoff time.Time, batchSize int) (int64, error) {
	if batchSize < 1 {
		batchSize = 1
	}
	var total int64
	for {
		tag, err := s.pool.Exec(ctx,
			`DELETE FROM metrics WHERE (host_id, recorded_at) IN (
			     SELECT host_id, recorded_at FROM metrics WHERE recorded_at < $1 ORDER BY recorded_at LIMIT $2)`,
			cutoff, batchSize,
		)
		if err != nil {
			return total, err
		}
		total += tag.RowsAffected()
		if tag.RowsAffected() < int64(batchSize) {
			return total, nil
		}
		if err := ctx.Err(); err != nil {
			return total, err
		}
	}
}

// LatestDockerContainers, her container'ın en son raporlanan durumunu döndürür (bkz.
// docs/MIMARI.md bölüm 7: GET /hosts/:id/docker).
func (s *Metrics) LatestDockerContainers(ctx context.Context, hostID uuid.UUID) ([]model.DockerContainerReport, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT name, image, status, cpu_pct, ram_mb, restart_count, uptime_seconds
		 FROM docker_containers
		 WHERE host_id = $1
		 ORDER BY name`,
		hostID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	containers := []model.DockerContainerReport{}
	for rows.Next() {
		var c model.DockerContainerReport
		if err := rows.Scan(&c.Name, &c.Image, &c.Status, &c.CPUPct, &c.RAMMB, &c.RestartCount, &c.UptimeSeconds); err != nil {
			return nil, err
		}
		containers = append(containers, c)
	}
	return containers, rows.Err()
}

// Latest, host'ın en son ham metrik örneğini döndürür; hiç örnek yoksa ErrNotFound. Panelin "Genel" sekmesindeki anlık
// kartları besler (GET /hosts/:id/metrics/latest): tek satırdır, (host_id, recorded_at) birincil anahtarından okunur.
func (s *Metrics) Latest(ctx context.Context, hostID uuid.UUID) (model.MetricPoint, error) {
	var p model.MetricPoint
	var diskJSON []byte
	err := s.pool.QueryRow(ctx,
		`SELECT recorded_at, cpu_usage_pct, ram_usage_pct, disk_json FROM metrics
		 WHERE host_id = $1 ORDER BY recorded_at DESC LIMIT 1`, hostID).
		Scan(&p.Timestamp, &p.CPUUsagePct, &p.RAMUsagePct, &diskJSON)
	if err != nil {
		if isNoRows(err) {
			return model.MetricPoint{}, ErrNotFound
		}
		return model.MetricPoint{}, err
	}
	if len(diskJSON) > 0 {
		if err := json.Unmarshal(diskJSON, &p.Disk); err != nil {
			return model.MetricPoint{}, err
		}
	}
	return p, nil
}

// LatestDisks, host'ın en son raporundaki mount'ları döndürür — panelin hangi mount'ların
// alert üretebileceğini seçerken sunduğu şey. Hiç rapor yoksa boş liste.
func (s *Metrics) LatestDisks(ctx context.Context, hostID uuid.UUID) ([]model.DiskUsage, error) {
	p, err := s.Latest(ctx, hostID)
	if errors.Is(err, ErrNotFound) {
		return []model.DiskUsage{}, nil
	}
	if err != nil {
		return nil, err
	}
	if p.Disk == nil {
		return []model.DiskUsage{}, nil
	}
	return p.Disk, nil
}

// RecentReportedMounts, host'ın herhangi bir disk listeleyen son n raporunun mount
// yollarını, en yeniden başlayarak döndürür. Boş disk listeli raporlar atlanır: agent disk
// toplaması başarısız olduğunda bir tane gönderir ve bu, belirli bir mount hakkında hiçbir şey söylemez.
func (s *Metrics) RecentReportedMounts(ctx context.Context, hostID uuid.UUID, n int) ([]map[string]struct{}, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT disk_json FROM metrics
		 WHERE host_id = $1 AND jsonb_array_length(disk_json) > 0
		 ORDER BY recorded_at DESC LIMIT $2`, hostID, n)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []map[string]struct{}
	for rows.Next() {
		var raw []byte
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		var disks []model.DiskUsage
		if err := json.Unmarshal(raw, &disks); err != nil {
			return nil, err
		}
		set := make(map[string]struct{}, len(disks))
		for _, d := range disks {
			set[d.Mount] = struct{}{}
		}
		out = append(out, set)
	}
	return out, rows.Err()
}
