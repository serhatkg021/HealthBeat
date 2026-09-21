package httpapi

import (
	"errors"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"time"

	"github.com/google/uuid"

	"healthbeat-server/internal/store"
)

const (
	// Bir grafik piksellerinden fazla nokta gösteremez; yanıtı sınırlamak, tek bir isteğin
	// server'a belleğinde ne kadar tutturabileceğini de sınırlar.
	defaultMetricsMaxPoints = 1000
	maxMetricsMaxPoints     = 5000
)

// handleGetHostMetrics, panelin host detay geçmiş grafiklerini besler (bkz.
// docs/MIMARI.md bölüm 7: GET /hosts/:id/metrics?from=&to=).
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
		log.Printf("get host metrics: lookup host: %v", err)
		writeError(w, http.StatusInternalServerError, "metrikler alınamadı")
		return
	}
	allowed, err := d.requireHostViewAccess(r, host)
	if err != nil {
		log.Printf("get host metrics: check access: %v", err)
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

	maxPoints := defaultMetricsMaxPoints
	if v := r.URL.Query().Get("max_points"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > maxMetricsMaxPoints {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("max_points 1 ile %d arasında olmalı", maxMetricsMaxPoints))
			return
		}
		maxPoints = n
	}

	points, err := d.metrics.ListByHostAndRange(r.Context(), id, from, to, maxPoints)
	if err != nil {
		log.Printf("get host metrics: %v", err)
		writeError(w, http.StatusInternalServerError, "metrikler alınamadı")
		return
	}
	writeJSON(w, http.StatusOK, points)
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
		log.Printf("get host docker: lookup host: %v", err)
		writeError(w, http.StatusInternalServerError, "Docker container'ları alınamadı")
		return
	}
	allowed, err := d.requireHostViewAccess(r, host)
	if err != nil {
		log.Printf("get host docker: check access: %v", err)
		writeError(w, http.StatusInternalServerError, "Docker container'ları alınamadı")
		return
	}
	if !allowed {
		writeError(w, http.StatusForbidden, "yetkiniz yok")
		return
	}

	containers, err := d.metrics.LatestDockerContainers(r.Context(), id)
	if err != nil {
		log.Printf("get host docker: %v", err)
		writeError(w, http.StatusInternalServerError, "Docker container'ları alınamadı")
		return
	}
	writeJSON(w, http.StatusOK, containers)
}
