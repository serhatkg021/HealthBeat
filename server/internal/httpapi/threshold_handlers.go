package httpapi

import (
	"errors"
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

func (d *Deps) handleListThresholds(w http.ResponseWriter, r *http.Request) error {
	fail := failWith("eşikler listelenemedi")
	role, _ := roleFromContext(r.Context())

	if role == model.RoleSuperAdmin {
		thresholds, err := d.thresholds.List(r.Context())
		if err != nil {
			return fail("list thresholds", err)
		}
		writeJSON(w, http.StatusOK, thresholds)
		return nil
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
		return fail("list thresholds: list user organizations", err)
	}
	thresholds, err := d.thresholds.ListForOrganizations(r.Context(), orgIDs)
	if err != nil {
		return fail("list thresholds", err)
	}
	writeJSON(w, http.StatusOK, thresholds)
	return nil
}

type thresholdRequest struct {
	// OrganizationID boşsa genel varsayılan (yalnızca super_admin), doluysa o organizasyonun (ve altındaki dalın)
	// varsayılanıdır.
	OrganizationID *uuid.UUID `json:"organization_id"`
	MetricType     string     `json:"metric_type"`
	WarningLevel   float64    `json:"warning_level"`
	CriticalLevel  float64    `json:"critical_level"`
}

func (req *thresholdRequest) Validate() error {
	if !model.ValidThresholdMetricType(req.MetricType) {
		return errors.New("metric_type cpu, ram, disk veya docker_restart olmalı")
	}
	return model.ValidateThresholdLevels(req.MetricType, req.WarningLevel, req.CriticalLevel)
}

func (d *Deps) handleCreateThreshold(w http.ResponseWriter, r *http.Request) error {
	fail := failWith("eşik oluşturulamadı")
	req, err := bind[thresholdRequest](r)
	if err != nil {
		return err
	}
	allowed, err := d.requireThresholdAccess(r, model.ThresholdConfig{OrganizationID: req.OrganizationID})
	switch {
	case errors.Is(err, store.ErrNotFound):
		return notFound("organizasyon bulunamadı")
	case err != nil:
		return fail("create threshold: check access", err)
	case !allowed:
		return forbidden()
	}

	threshold, err := d.thresholds.Create(r.Context(), store.CreateThresholdParams{
		OrganizationID: req.OrganizationID,
		MetricType:     req.MetricType,
		WarningLevel:   req.WarningLevel,
		CriticalLevel:  req.CriticalLevel,
	})
	switch {
	case errors.Is(err, store.ErrNotFound):
		return notFound("organizasyon bulunamadı")
	case errors.Is(err, store.ErrConflict):
		return conflict(err.Error())
	case err != nil:
		return fail("create threshold", err)
	}

	targetID := threshold.ID.String()
	d.logAudit(r, "threshold.create", "threshold", &targetID, map[string]any{
		"metric_type":    threshold.MetricType,
		"warning_level":  threshold.WarningLevel,
		"critical_level": threshold.CriticalLevel,
	})
	writeJSON(w, http.StatusCreated, threshold)
	return nil
}

func (d *Deps) handleGetThreshold(w http.ResponseWriter, r *http.Request) error {
	fail := failWith("eşik alınamadı")
	id, err := pathID(r, "geçersiz eşik kimliği")
	if err != nil {
		return err
	}

	threshold, err := d.thresholds.GetByID(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		return notFound("eşik bulunamadı")
	}
	if err != nil {
		return fail("get threshold", err)
	}

	allowed, err := d.requireThresholdAccess(r, threshold)
	if err != nil {
		return fail("get threshold: check access", err)
	}
	if !allowed {
		return forbidden()
	}

	writeJSON(w, http.StatusOK, threshold)
	return nil
}

// managedThreshold, id'li varsayılan eşiği yükler ve çağıranın onu düzenleyebildiğini denetler (bkz.
// requireThresholdAccess). Beklenmeyen hatalar fail ile op öneki taşıyarak loglanır.
func (d *Deps) managedThreshold(r *http.Request, id uuid.UUID, op string, fail failFunc) (model.ThresholdConfig, error) {
	threshold, err := d.thresholds.GetByID(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		return model.ThresholdConfig{}, notFound("eşik bulunamadı")
	}
	if err != nil {
		return model.ThresholdConfig{}, fail(op+": lookup", err)
	}
	allowed, err := d.requireThresholdAccess(r, threshold)
	if err != nil {
		return model.ThresholdConfig{}, fail(op+": check access", err)
	}
	if !allowed {
		return model.ThresholdConfig{}, forbidden()
	}
	return threshold, nil
}

type updateThresholdRequest struct {
	WarningLevel  *float64 `json:"warning_level"`
	CriticalLevel *float64 `json:"critical_level"`
}

func (d *Deps) handleUpdateThreshold(w http.ResponseWriter, r *http.Request) error {
	fail := failWith("eşik güncellenemedi")
	id, err := pathID(r, "geçersiz eşik kimliği")
	if err != nil {
		return err
	}
	existing, err := d.managedThreshold(r, id, "update threshold", fail)
	if err != nil {
		return err
	}

	req, err := bind[updateThresholdRequest](r)
	if err != nil {
		return err
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
			return badRequest(err.Error())
		}
	}

	threshold, err := d.thresholds.Update(r.Context(), id, req.WarningLevel, req.CriticalLevel)
	switch {
	case errors.Is(err, store.ErrNotFound):
		return notFound("eşik bulunamadı")
	case errors.Is(err, store.ErrConflict):
		return conflict(err.Error())
	case err != nil:
		return fail("update threshold", err)
	}

	targetID := threshold.ID.String()
	d.logAudit(r, "threshold.update", "threshold", &targetID, map[string]any{
		"warning_level":  threshold.WarningLevel,
		"critical_level": threshold.CriticalLevel,
	})
	writeJSON(w, http.StatusOK, threshold)
	return nil
}

func (d *Deps) handleDeleteThreshold(w http.ResponseWriter, r *http.Request) error {
	fail := failWith("eşik silinemedi")
	id, err := pathID(r, "geçersiz eşik kimliği")
	if err != nil {
		return err
	}
	if _, err := d.managedThreshold(r, id, "delete threshold", fail); err != nil {
		return err
	}

	err = d.thresholds.Delete(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		return notFound("eşik bulunamadı")
	}
	if err != nil {
		return fail("delete threshold", err)
	}

	targetID := id.String()
	d.logAudit(r, "threshold.delete", "threshold", &targetID, nil)
	w.WriteHeader(http.StatusNoContent)
	return nil
}
