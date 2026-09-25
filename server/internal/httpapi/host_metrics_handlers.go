package httpapi

import (
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/google/uuid"

	"healthbeat-server/internal/store"
)

// handleGetHostMetrics, panelin host detay sayfasını besler: "Genel" sekmesindeki anlık kartlar
// dizinin son elemanını kullanır, geçmiş grafiği tüm diziyi (bkz. docs/MIMARI.md bölüm 7:
// GET /hosts/:id/metrics?from=&to=). Yanıt her zaman ham satırlardır, ortalanmaz/kovalanmaz.
func (d *Deps) handleGetHostMetrics(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "geçersiz sunucu kimliği")
		return
	}

	host, err := d.hosts.GetByID(r.Context(), id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "sunucu bulunamadı")
			return
		}
		slog.ErrorContext(r.Context(), "get host metrics: lookup host", "err", err)
		writeError(w, http.StatusInternalServerError, "metrikler alınamadı")
		return
	}
	allowed, err := d.requireHostViewAccess(r, host)
	if err != nil {
		slog.ErrorContext(r.Context(), "get host metrics: check access", "err", err)
		writeError(w, http.StatusInternalServerError, "metrikler alınamadı")
		return
	}
	if !allowed {
		writeError(w, http.StatusForbidden, "yetkiniz yok")
		return
	}

	to := time.Now()
	from := to.Add(-24 * time.Hour)
	if v := r.URL.Query().Get("to"); v != "" {
		parsed, err := time.Parse(time.RFC3339, v)
		if err != nil {
			writeError(w, http.StatusBadRequest, "to RFC3339 biçiminde olmalı")
			return
		}
		to = parsed
	}
	if v := r.URL.Query().Get("from"); v != "" {
		parsed, err := time.Parse(time.RFC3339, v)
		if err != nil {
			writeError(w, http.StatusBadRequest, "from RFC3339 biçiminde olmalı")
			return
		}
		from = parsed
	}
	if from.After(to) {
		writeError(w, http.StatusBadRequest, "from, to'dan önce olmalı")
		return
	}

	points, err := d.metrics.ListByHostAndRange(r.Context(), id, from, to)
	if err != nil {
		slog.ErrorContext(r.Context(), "get host metrics", "err", err)
		writeError(w, http.StatusInternalServerError, "metrikler alınamadı")
		return
	}
	writeJSON(w, http.StatusOK, points)
}

// handleGetHostLatestMetric, host'ın en son ham metrik örneğini döndürür; panelin "Genel" sekmesindeki anlık kartlar
// yalnızca bunu gösterir ve tüm aralığı (GET /hosts/:id/metrics, varsayılan son 24 saat) indirmek zorunda kalmaz.
// Biçim, /metrics dizisinin bir elemanıyla aynıdır. Host henüz hiç rapor vermediyse 204.
func (d *Deps) handleGetHostLatestMetric(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "geçersiz sunucu kimliği")
		return
	}

	host, err := d.hosts.GetByID(r.Context(), id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "sunucu bulunamadı")
			return
		}
		slog.ErrorContext(r.Context(), "get host latest metric: lookup host", "err", err)
		writeError(w, http.StatusInternalServerError, "metrikler alınamadı")
		return
	}
	allowed, err := d.requireHostViewAccess(r, host)
	if err != nil {
		slog.ErrorContext(r.Context(), "get host latest metric: check access", "err", err)
		writeError(w, http.StatusInternalServerError, "metrikler alınamadı")
		return
	}
	if !allowed {
		writeError(w, http.StatusForbidden, "yetkiniz yok")
		return
	}

	point, err := d.metrics.Latest(r.Context(), id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		slog.ErrorContext(r.Context(), "get host latest metric", "err", err)
		writeError(w, http.StatusInternalServerError, "metrikler alınamadı")
		return
	}
	writeJSON(w, http.StatusOK, point)
}

// handleGetHostDocker, her container'ın son raporlanan durumunu döndürür (bkz.
// docs/MIMARI.md bölüm 7: GET /hosts/:id/docker).
func (d *Deps) handleGetHostDocker(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "geçersiz sunucu kimliği")
		return
	}

	host, err := d.hosts.GetByID(r.Context(), id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "sunucu bulunamadı")
			return
		}
		slog.ErrorContext(r.Context(), "get host docker: lookup host", "err", err)
		writeError(w, http.StatusInternalServerError, "Docker container'ları alınamadı")
		return
	}
	allowed, err := d.requireHostViewAccess(r, host)
	if err != nil {
		slog.ErrorContext(r.Context(), "get host docker: check access", "err", err)
		writeError(w, http.StatusInternalServerError, "Docker container'ları alınamadı")
		return
	}
	if !allowed {
		writeError(w, http.StatusForbidden, "yetkiniz yok")
		return
	}

	containers, err := d.metrics.LatestDockerContainers(r.Context(), id)
	if err != nil {
		slog.ErrorContext(r.Context(), "get host docker", "err", err)
		writeError(w, http.StatusInternalServerError, "Docker container'ları alınamadı")
		return
	}
	writeJSON(w, http.StatusOK, containers)
}
