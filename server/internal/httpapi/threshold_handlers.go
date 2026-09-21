package httpapi

import (
	"errors"
	"log"
	"net/http"

	"github.com/google/uuid"

	"healthbeat-server/internal/model"
	"healthbeat-server/internal/store"
)

// requireThresholdAccess, requireOrgAccess'i bir varsayılan eşik satırı için yansıtır: organizasyon
// varsayılanı organizasyon erişimini izler ve genel bir varsayılan (organizasyonsuz) yalnızca
// super_admin içindir. (Sunucuya özel eşikler /hosts/:id/thresholds ile yönetilir.)
func (d *Deps) requireThresholdAccess(r *http.Request, t model.ThresholdConfig) (bool, error) {
	if t.OrganizationID != nil {
		return d.requireOrgAccess(r, *t.OrganizationID)
	}
	role, _ := roleFromContext(r.Context())
	return role == model.RoleSuperAdmin, nil
}

func (d *Deps) handleListThresholds(w http.ResponseWriter, r *http.Request) {
	role, _ := roleFromContext(r.Context())

	if role == model.RoleSuperAdmin {
		thresholds, err := d.thresholds.List(r.Context())
		if err != nil {
			log.Printf("list thresholds: %v", err)
			writeError(w, http.StatusInternalServerError, "eşikler listelenemedi")
			return
		}
		writeJSON(w, http.StatusOK, thresholds)
		return
	}

	// Kendi dalının eşiklerine ek olarak üst zincirin eşikleri de (salt okunur bağlam) döner: sunucuya uygulanan geçerli
	// değer üst şirketten miras alınmış olabilir ve yönetici bunu görmeden neyin uygulandığını bilemez. Düzenleme/silme
	// yine yalnızca kendi dalı içindir (requireThresholdAccess).
	userID, _ := userIDFromContext(r.Context())
	orgIDs, err := d.userOrgs.ListOrganizationIDs(r.Context(), userID)
	if err == nil {
		var contextIDs []uuid.UUID
		if contextIDs, err = d.userOrgs.ListContextIDs(r.Context(), userID); err == nil {
			orgIDs = append(orgIDs, contextIDs...)
		}
	}
	if err != nil {
		log.Printf("list thresholds: list user organizations: %v", err)
		writeError(w, http.StatusInternalServerError, "eşikler listelenemedi")
		return
	}
	thresholds, err := d.thresholds.ListForOrganizations(r.Context(), orgIDs)
	if err != nil {
		log.Printf("list thresholds: %v", err)
		writeError(w, http.StatusInternalServerError, "eşikler listelenemedi")
		return
	}
	writeJSON(w, http.StatusOK, thresholds)
}

type thresholdRequest struct {
	// OrganizationID boşsa genel varsayılan (yalnızca super_admin), doluysa o organizasyonun (ve altındaki dalın)
	// varsayılanıdır.
	OrganizationID *uuid.UUID `json:"organization_id"`
	MetricType     string     `json:"metric_type"`
	WarningLevel   float64    `json:"warning_level"`
	CriticalLevel  float64    `json:"critical_level"`
}

func (d *Deps) handleCreateThreshold(w http.ResponseWriter, r *http.Request) {
	var req thresholdRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "geçersiz istek gövdesi")
		return
	}
	if !model.ValidThresholdMetricType(req.MetricType) {
		writeError(w, http.StatusBadRequest, "metric_type cpu, ram, disk veya docker_restart olmalı")
		return
	}
	if err := model.ValidateThresholdLevels(req.MetricType, req.WarningLevel, req.CriticalLevel); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	scoped := model.ThresholdConfig{OrganizationID: req.OrganizationID}
	allowed, err := d.requireThresholdAccess(r, scoped)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "organizasyon bulunamadı")
			return
		}
		log.Printf("create threshold: check access: %v", err)
		writeError(w, http.StatusInternalServerError, "eşik oluşturulamadı")
		return
	}
	if !allowed {
		writeError(w, http.StatusForbidden, "yetkiniz yok")
		return
	}

	threshold, err := d.thresholds.Create(r.Context(), store.CreateThresholdParams{
		OrganizationID: req.OrganizationID,
		MetricType:     req.MetricType,
		WarningLevel:   req.WarningLevel,
		CriticalLevel:  req.CriticalLevel,
	})
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "organizasyon bulunamadı")
			return
		}
		if errors.Is(err, store.ErrConflict) {
			writeError(w, http.StatusConflict, err.Error())
			return
		}
		log.Printf("create threshold: %v", err)
		writeError(w, http.StatusInternalServerError, "eşik oluşturulamadı")
		return
	}

	targetID := threshold.ID.String()
	d.logAudit(r, "threshold.create", "threshold", &targetID, map[string]any{
		"metric_type":    threshold.MetricType,
		"warning_level":  threshold.WarningLevel,
		"critical_level": threshold.CriticalLevel,
	})
	writeJSON(w, http.StatusCreated, threshold)
}

func (d *Deps) handleGetThreshold(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "geçersiz eşik kimliği")
		return
	}

	threshold, err := d.thresholds.GetByID(r.Context(), id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "eşik bulunamadı")
			return
		}
		log.Printf("get threshold: %v", err)
		writeError(w, http.StatusInternalServerError, "eşik alınamadı")
		return
	}

	allowed, err := d.requireThresholdAccess(r, threshold)
	if err != nil {
		log.Printf("get threshold: check access: %v", err)
		writeError(w, http.StatusInternalServerError, "eşik alınamadı")
		return
	}
	if !allowed {
		writeError(w, http.StatusForbidden, "yetkiniz yok")
		return
	}

	writeJSON(w, http.StatusOK, threshold)
}

type updateThresholdRequest struct {
	WarningLevel  *float64 `json:"warning_level"`
	CriticalLevel *float64 `json:"critical_level"`
}

func (d *Deps) handleUpdateThreshold(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "geçersiz eşik kimliği")
		return
	}

	existing, err := d.thresholds.GetByID(r.Context(), id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "eşik bulunamadı")
			return
		}
		log.Printf("update threshold: lookup: %v", err)
		writeError(w, http.StatusInternalServerError, "eşik güncellenemedi")
		return
	}
	allowed, err := d.requireThresholdAccess(r, existing)
	if err != nil {
		log.Printf("update threshold: check access: %v", err)
		writeError(w, http.StatusInternalServerError, "eşik güncellenemedi")
		return
	}
	if !allowed {
		writeError(w, http.StatusForbidden, "yetkiniz yok")
		return
	}

	var req updateThresholdRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "geçersiz istek gövdesi")
		return
	}
	// Seviyeleri GÜNCELLEMEDEN SONRAKİ hâlleriyle doğrula, ama yalnızca istek onları değiştiriyorsa:
	// ilgisiz bir düzenleme eski verilerde başarısız olmamalı.
	if req.WarningLevel != nil || req.CriticalLevel != nil {
		warning, critical := existing.WarningLevel, existing.CriticalLevel
		if req.WarningLevel != nil {
			warning = *req.WarningLevel
		}
		if req.CriticalLevel != nil {
			critical = *req.CriticalLevel
		}
		if err := model.ValidateThresholdLevels(existing.MetricType, warning, critical); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
	}

	threshold, err := d.thresholds.Update(r.Context(), id, req.WarningLevel, req.CriticalLevel)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "eşik bulunamadı")
			return
		}
		if errors.Is(err, store.ErrConflict) {
			writeError(w, http.StatusConflict, err.Error())
			return
		}
		log.Printf("update threshold: %v", err)
		writeError(w, http.StatusInternalServerError, "eşik güncellenemedi")
		return
	}

	targetID := threshold.ID.String()
	d.logAudit(r, "threshold.update", "threshold", &targetID, map[string]any{
		"warning_level":  threshold.WarningLevel,
		"critical_level": threshold.CriticalLevel,
	})
	writeJSON(w, http.StatusOK, threshold)
}

func (d *Deps) handleDeleteThreshold(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "geçersiz eşik kimliği")
		return
	}

	existing, err := d.thresholds.GetByID(r.Context(), id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "eşik bulunamadı")
			return
		}
		log.Printf("delete threshold: lookup: %v", err)
		writeError(w, http.StatusInternalServerError, "eşik silinemedi")
		return
	}
	allowed, err := d.requireThresholdAccess(r, existing)
	if err != nil {
		log.Printf("delete threshold: check access: %v", err)
		writeError(w, http.StatusInternalServerError, "eşik silinemedi")
		return
	}
	if !allowed {
		writeError(w, http.StatusForbidden, "yetkiniz yok")
		return
	}

	if err := d.thresholds.Delete(r.Context(), id); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "eşik bulunamadı")
			return
		}
		log.Printf("delete threshold: %v", err)
		writeError(w, http.StatusInternalServerError, "eşik silinemedi")
		return
	}

	targetID := id.String()
	d.logAudit(r, "threshold.delete", "threshold", &targetID, nil)
	w.WriteHeader(http.StatusNoContent)
}
