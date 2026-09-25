package httpapi

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sort"

	"github.com/google/uuid"

	"healthbeat-server/internal/model"
	"healthbeat-server/internal/rbac"
	"healthbeat-server/internal/store"
)

// thresholdLevelsInput işaretçi alanlara sahiptir; böylece yarım doldurulmuş çift sessizce 0
// seviyesine dönüşmek yerine reddedilir.
type thresholdLevelsInput struct {
	WarningLevel  *float64 `json:"warning_level"`
	CriticalLevel *float64 `json:"critical_level"`
}

// thresholdOverridesInput, model.ThresholdOverrides'ın tel biçimidir: metrik türü ->
// null (varsayılanı kullan) ya da {warning_level, critical_level} (özel).
type thresholdOverridesInput map[string]*thresholdLevelsInput

func (in thresholdOverridesInput) toOverrides() (model.ThresholdOverrides, error) {
	out := make(model.ThresholdOverrides, len(in))
	for metricType, levels := range in {
		if levels == nil {
			out[metricType] = nil
			continue
		}
		if levels.WarningLevel == nil || levels.CriticalLevel == nil {
			return nil, fmt.Errorf("%s: warning_level and critical_level are both required (or null to use the default)", metricType)
		}
		out[metricType] = &model.ThresholdLevels{WarningLevel: *levels.WarningLevel, CriticalLevel: *levels.CriticalLevel}
	}
	if err := out.Validate(); err != nil {
		return nil, err
	}
	return out, nil
}

// hostThresholdView, bir host için bir metriğin eşik durumudur: varsayılan olarak ne
// alacağı ve varsa kendi özel değerleri. Ayarlıysa özel olan kazanır.
type hostThresholdView struct {
	MetricType string                 `json:"metric_type"`
	Default    *model.ThresholdLevels `json:"default"`
	Custom     *model.ThresholdLevels `json:"custom"`
}

// hostMountThreshold, bir mount'un kendi disk eşiğidir.
type hostMountThreshold struct {
	Mount  string                `json:"mount"`
	Custom model.ThresholdLevels `json:"custom"`
}

// hostContainerThreshold, bir container'ın kendi docker_restart eşiğidir.
type hostContainerThreshold struct {
	Container string                `json:"container"`
	Custom    model.ThresholdLevels `json:"custom"`
}

type hostThresholdsResponse struct {
	Thresholds []hostThresholdView `json:"thresholds"`
	// MountThresholds yalnızca kendi eşiği olan mount'ları listeler (mount'a göre sıralı);
	// diğer her mount host'ın disk eşiğini izler.
	MountThresholds []hostMountThreshold `json:"mount_thresholds"`
	// ContainerThresholds, yalnızca kendi eşiği olan container'ları (ada göre sıralı) listeler.
	ContainerThresholds []hostContainerThreshold `json:"container_thresholds"`
}

// thresholdSubjectsInput, model.MountThresholds / model.ContainerThresholds'un JSON biçimidir:
// subject (mount yolu ya da container adı) -> null (host geneli eşiği izle) veya
// {warning_level, critical_level}.
type thresholdSubjectsInput map[string]*thresholdLevelsInput

func (in thresholdSubjectsInput) toLevels() (map[string]*model.ThresholdLevels, error) {
	out := make(map[string]*model.ThresholdLevels, len(in))
	for subject, levels := range in {
		if levels == nil {
			out[subject] = nil
			continue
		}
		if levels.WarningLevel == nil || levels.CriticalLevel == nil {
			return nil, fmt.Errorf("%s: warning_level ve critical_level birlikte verilmeli (ya da null)", subject)
		}
		out[subject] = &model.ThresholdLevels{WarningLevel: *levels.WarningLevel, CriticalLevel: *levels.CriticalLevel}
	}
	return out, nil
}

func (in thresholdSubjectsInput) toMounts() (model.MountThresholds, error) {
	levels, err := in.toLevels()
	if err != nil {
		return nil, err
	}
	out := model.MountThresholds(levels)
	if err := out.Validate(); err != nil {
		return nil, err
	}
	return out, nil
}

func (in thresholdSubjectsInput) toContainers() (model.ContainerThresholds, error) {
	levels, err := in.toLevels()
	if err != nil {
		return nil, err
	}
	out := model.ContainerThresholds(levels)
	if err := out.Validate(); err != nil {
		return nil, err
	}
	return out, nil
}

func (d *Deps) hostThresholds(r *http.Request, host model.Host) (hostThresholdsResponse, error) {
	defaults, err := d.thresholds.DefaultsFor(r.Context(), host.OrganizationID)
	if err != nil {
		return hostThresholdsResponse{}, err
	}
	custom, err := d.thresholds.HostOverrides(r.Context(), host.ID)
	if err != nil {
		return hostThresholdsResponse{}, err
	}
	mounts, err := d.thresholds.HostSubjectOverrides(r.Context(), host.ID, model.MetricTypeDisk)
	if err != nil {
		return hostThresholdsResponse{}, err
	}
	containers, err := d.thresholds.HostSubjectOverrides(r.Context(), host.ID, model.MetricTypeDockerRestart)
	if err != nil {
		return hostThresholdsResponse{}, err
	}
	resp := hostThresholdsResponse{
		Thresholds:          make([]hostThresholdView, 0, len(model.ThresholdMetricTypes)),
		MountThresholds:     make([]hostMountThreshold, 0, len(mounts)),
		ContainerThresholds: make([]hostContainerThreshold, 0, len(containers)),
	}
	for mount, levels := range mounts {
		resp.MountThresholds = append(resp.MountThresholds, hostMountThreshold{Mount: mount, Custom: levels})
	}
	sort.Slice(resp.MountThresholds, func(i, j int) bool { return resp.MountThresholds[i].Mount < resp.MountThresholds[j].Mount })
	for name, levels := range containers {
		resp.ContainerThresholds = append(resp.ContainerThresholds, hostContainerThreshold{Container: name, Custom: levels})
	}
	sort.Slice(resp.ContainerThresholds, func(i, j int) bool {
		return resp.ContainerThresholds[i].Container < resp.ContainerThresholds[j].Container
	})
	for _, metricType := range model.ThresholdMetricTypes {
		view := hostThresholdView{MetricType: metricType}
		if levels, ok := defaults[metricType]; ok {
			view.Default = &levels
		}
		if levels, ok := custom[metricType]; ok {
			view.Custom = &levels
		}
		resp.Thresholds = append(resp.Thresholds, view)
	}
	return resp, nil
}

// handleGetHostThresholds, GET /api/v1/hosts/:id/thresholds'u sunar.
func (d *Deps) handleGetHostThresholds(w http.ResponseWriter, r *http.Request) {
	host, ok := d.loadHostForView(w, r, "sunucu eşikleri alınamadı")
	if !ok {
		return
	}
	resp, err := d.hostThresholds(r, host)
	if err != nil {
		slog.ErrorContext(r.Context(), "get host thresholds", "err", err)
		writeError(w, http.StatusInternalServerError, "sunucu eşikleri alınamadı")
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

// handleSetHostThresholds, PUT /api/v1/hosts/:id/thresholds'u sunar. Gövde
// {"thresholds": {"cpu": {"warning_level": 70, "critical_level": 85}, "ram": null}}
// biçimindedir: bir çift o metrik için host'ın özel eşiğini ayarlar, null onu kaldırır
// (varsayılana döner) ve dışarıda bırakılan metriklere dokunulmaz. İsteğe bağlı
// "mount_thresholds" aynı şekilde mount yolu başına çalışır ({"/storage": {...}, "/": null}).
// Tüm değişiklikler birlikte uygulanır ya da hiç uygulanmaz.
func (d *Deps) handleSetHostThresholds(w http.ResponseWriter, r *http.Request) {
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
		slog.ErrorContext(r.Context(), "set host thresholds: lookup", "err", err)
		writeError(w, http.StatusInternalServerError, "sunucu eşikleri güncellenemedi")
		return
	}
	allowed, err := d.requireOrgAccess(r, host.OrganizationID)
	if err != nil {
		slog.ErrorContext(r.Context(), "set host thresholds: check org access", "err", err)
		writeError(w, http.StatusInternalServerError, "sunucu eşikleri güncellenemedi")
		return
	}
	if !allowed {
		writeError(w, http.StatusForbidden, "yetkiniz yok")
		return
	}

	var req struct {
		Thresholds          thresholdOverridesInput `json:"thresholds"`
		MountThresholds     thresholdSubjectsInput  `json:"mount_thresholds"`
		ContainerThresholds thresholdSubjectsInput  `json:"container_thresholds"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "geçersiz istek gövdesi")
		return
	}
	if req.Thresholds == nil {
		writeError(w, http.StatusBadRequest, `"thresholds" zorunlu: metrik türü -> null (varsayılan) ya da {warning_level, critical_level} nesnesi`)
		return
	}
	overrides, err := req.Thresholds.toOverrides()
	if err != nil {
		writeError(w, http.StatusBadRequest, "thresholds: "+err.Error())
		return
	}

	mounts, err := req.MountThresholds.toMounts()
	if err != nil {
		writeError(w, http.StatusBadRequest, "mount_thresholds: "+err.Error())
		return
	}

	containers, err := req.ContainerThresholds.toContainers()
	if err != nil {
		writeError(w, http.StatusBadRequest, "container_thresholds: "+err.Error())
		return
	}

	if err := d.thresholds.SetHostOverrides(r.Context(), id, overrides, mounts, containers); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "sunucu bulunamadı")
			return
		}
		slog.ErrorContext(r.Context(), "set host thresholds", "err", err)
		writeError(w, http.StatusInternalServerError, "sunucu eşikleri güncellenemedi")
		return
	}

	targetID := id.String()
	d.logAudit(r, "host.update_thresholds", "host", &targetID, map[string]any{"thresholds": overrides, "mount_thresholds": mounts, "container_thresholds": containers})

	resp, err := d.hostThresholds(r, host)
	if err != nil {
		slog.ErrorContext(r.Context(), "set host thresholds: reload", "err", err)
		writeError(w, http.StatusInternalServerError, "eşikler kaydedildi ancak geri okunamadı")
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

// canEditThresholds, çağıranın rolünün threshold.edit'e sahip olup olmadığını bildirir. Farklı
// bir izin gerektiren bir isteğin (host oluşturma) eşik de ayarladığı yerlerde kullanılır.
func (d *Deps) canEditThresholds(r *http.Request) (bool, error) {
	role, _ := roleFromContext(r.Context())
	return rbac.HasPermission(r.Context(), d.pool, role, "threshold.edit")
}
