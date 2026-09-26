package httpapi

import (
	"errors"
	"net/http"
	"time"

	"healthbeat-server/internal/store"
)

// handleGetHostMetrics, panelin host detay sayfasını besler: "Genel" sekmesindeki anlık kartlar
// dizinin son elemanını kullanır, geçmiş grafiği tüm diziyi (bkz. docs/MIMARI.md bölüm 7:
// GET /hosts/:id/metrics?from=&to=). Yanıt her zaman ham satırlardır, ortalanmaz/kovalanmaz.
func (d *Deps) handleGetHostMetrics(w http.ResponseWriter, r *http.Request) error {
	fail := failWith("metrikler alınamadı")
	host, err := d.viewableHost(r, "get host metrics", fail)
	if err != nil {
		return err
	}

	to := time.Now()
	from := to.Add(-24 * time.Hour)
	if v := r.URL.Query().Get("to"); v != "" {
		parsed, err := time.Parse(time.RFC3339, v)
		if err != nil {
			return badRequest("to RFC3339 biçiminde olmalı")
		}
		to = parsed
	}
	if v := r.URL.Query().Get("from"); v != "" {
		parsed, err := time.Parse(time.RFC3339, v)
		if err != nil {
			return badRequest("from RFC3339 biçiminde olmalı")
		}
		from = parsed
	}
	if from.After(to) {
		return badRequest("from, to'dan önce olmalı")
	}

	points, err := d.metrics.ListByHostAndRange(r.Context(), host.ID, from, to)
	if err != nil {
		return fail("get host metrics", err)
	}
	writeJSON(w, http.StatusOK, points)
	return nil
}

// handleGetHostLatestMetric, host'ın en son ham metrik örneğini döndürür; panelin "Genel" sekmesindeki anlık kartlar
// yalnızca bunu gösterir ve tüm aralığı (GET /hosts/:id/metrics, varsayılan son 24 saat) indirmek zorunda kalmaz.
// Biçim, /metrics dizisinin bir elemanıyla aynıdır. Host henüz hiç rapor vermediyse 204.
func (d *Deps) handleGetHostLatestMetric(w http.ResponseWriter, r *http.Request) error {
	fail := failWith("metrikler alınamadı")
	host, err := d.viewableHost(r, "get host latest metric", fail)
	if err != nil {
		return err
	}

	point, err := d.metrics.Latest(r.Context(), host.ID)
	if errors.Is(err, store.ErrNotFound) {
		w.WriteHeader(http.StatusNoContent)
		return nil
	}
	if err != nil {
		return fail("get host latest metric", err)
	}
	writeJSON(w, http.StatusOK, point)
	return nil
}

// handleGetHostDocker, her container'ın son raporlanan durumunu döndürür (bkz.
// docs/MIMARI.md bölüm 7: GET /hosts/:id/docker).
func (d *Deps) handleGetHostDocker(w http.ResponseWriter, r *http.Request) error {
	fail := failWith("Docker container'ları alınamadı")
	host, err := d.viewableHost(r, "get host docker", fail)
	if err != nil {
		return err
	}

	containers, err := d.metrics.LatestDockerContainers(r.Context(), host.ID)
	if err != nil {
		return fail("get host docker", err)
	}
	writeJSON(w, http.StatusOK, containers)
	return nil
}
