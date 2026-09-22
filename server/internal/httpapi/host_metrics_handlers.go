package httpapi

import (
	"errors"
	"log"
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

	points, err := d.metrics.ListByHostAndRange(r.Context(), id, from, to)
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
