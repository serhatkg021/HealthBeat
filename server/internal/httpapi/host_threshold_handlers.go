package httpapi

import (
	"errors"
	"fmt"
	"net/http"
	"sort"

	"healthbeat-server/internal/model"
)

// thresholdLevelsInput işaretçi alanlara sahiptir; böylece yarım doldurulmuş çift sessizce 0
// seviyesine dönüşmek yerine reddedilir.
type thresholdLevelsInput struct {
	WarningLevel    *float64 `json:"warning_level"`
	CriticalLevel   *float64 `json:"critical_level"`
	DurationSeconds *int     `json:"duration_seconds"` // boş = hemen
}

func (in thresholdLevelsInput) levels() *model.ThresholdLevels {
	return &model.ThresholdLevels{WarningLevel: *in.WarningLevel, CriticalLevel: *in.CriticalLevel, DurationSeconds: in.DurationSeconds}
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
			return nil, fmt.Errorf("%s: warning_level ve critical_level birlikte verilmeli (ya da varsayılan için null)", metricType)
		}
		out[metricType] = levels.levels()
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

// hostSubjectThreshold, protokol 4 türlerinden birinin bir konusunun (disk, sensör, servis) kendi eşiğidir.
type hostSubjectThreshold struct {
	MetricType string                `json:"metric_type"`
	Subject    string                `json:"subject"`
	Custom     model.ThresholdLevels `json:"custom"`
}

type hostThresholdsResponse struct {
	Thresholds []hostThresholdView `json:"thresholds"`
	// MountThresholds yalnızca kendi eşiği olan mount'ları listeler (mount'a göre sıralı);
	// diğer her mount host'ın disk eşiğini izler.
	MountThresholds []hostMountThreshold `json:"mount_thresholds"`
	// ContainerThresholds, yalnızca kendi eşiği olan container'ları (ada göre sıralı) listeler.
	ContainerThresholds []hostContainerThreshold `json:"container_thresholds"`
	// SubjectThresholds, disk_latency (disk), temperature (sensör) ve service_restart (servis) türlerinde kendi eşiği
	// olan konulardır (tür ve konuya göre sıralı); diğerleri sunucu genelindeki eşiği izler.
	SubjectThresholds []hostSubjectThreshold `json:"subject_thresholds"`
}

// subjectThresholdsInput, model.SubjectThresholds'un JSON biçimidir: tür -> konu -> null ya da seviyeler.
type subjectThresholdsInput map[string]thresholdSubjectsInput

func (in subjectThresholdsInput) toSubjects() (model.SubjectThresholds, error) {
	out := make(model.SubjectThresholds, len(in))
	for metricType, subjects := range in {
		levels, err := subjects.toLevels()
		if err != nil {
			return nil, fmt.Errorf("%s: %w", metricType, err)
		}
		out[metricType] = levels
	}
	if err := out.Validate(); err != nil {
		return nil, err
	}
	return out, nil
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
		out[subject] = levels.levels()
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
		SubjectThresholds:   []hostSubjectThreshold{},
	}
	for _, metricType := range model.ThresholdMetricTypes {
		if _, ok := model.SubjectMetricTypes[metricType]; !ok {
			continue
		}
		subjects, err := d.thresholds.HostSubjectOverrides(r.Context(), host.ID, metricType)
		if err != nil {
			return hostThresholdsResponse{}, err
		}
		names := make([]string, 0, len(subjects))
		for name := range subjects {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			resp.SubjectThresholds = append(resp.SubjectThresholds, hostSubjectThreshold{MetricType: metricType, Subject: name, Custom: subjects[name]})
		}
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
func (d *Deps) handleGetHostThresholds(w http.ResponseWriter, r *http.Request) error {
	fail := failWith("sunucu eşikleri alınamadı")
	host, err := d.viewableHost(r, "get host thresholds", fail)
	if err != nil {
		return err
	}
	resp, err := d.hostThresholds(r, host)
	if err != nil {
		return fail("get host thresholds", err)
	}
	writeJSON(w, http.StatusOK, resp)
	return nil
}

type setHostThresholdsRequest struct {
	Thresholds          thresholdOverridesInput `json:"thresholds"`
	MountThresholds     thresholdSubjectsInput  `json:"mount_thresholds"`
	ContainerThresholds thresholdSubjectsInput  `json:"container_thresholds"`
	SubjectThresholds   subjectThresholdsInput  `json:"subject_thresholds"`
}

func (req *setHostThresholdsRequest) Validate() error {
	if req.Thresholds == nil {
		return errors.New(`"thresholds" zorunlu: metrik türü -> null (varsayılan) ya da {warning_level, critical_level} nesnesi`)
	}
	return nil
}

// handleSetHostThresholds, PUT /api/v1/hosts/:id/thresholds'u sunar. Gövde
// {"thresholds": {"cpu": {"warning_level": 70, "critical_level": 85}, "ram": null}}
// biçimindedir: bir çift o metrik için host'ın özel eşiğini ayarlar, null onu kaldırır
// (varsayılana döner) ve dışarıda bırakılan metriklere dokunulmaz. İsteğe bağlı
// "mount_thresholds" aynı şekilde mount yolu başına çalışır ({"/storage": {...}, "/": null}); "subject_thresholds"
// protokol 4 türlerinde konu başına ({"disk_latency": {"sda": {...}}, "temperature": {"coretemp/Package id 0": null}}).
// Seviyelere isteğe bağlı "duration_seconds" eklenebilir (yalnızca süre koşulu destekleyen türlerde).
// Tüm değişiklikler birlikte uygulanır ya da hiç uygulanmaz.
func (d *Deps) handleSetHostThresholds(w http.ResponseWriter, r *http.Request) error {
	fail := failWith("sunucu eşikleri güncellenemedi")
	host, err := d.managedHost(r, "set host thresholds", fail)
	if err != nil {
		return err
	}
	req, err := bind[setHostThresholdsRequest](r)
	if err != nil {
		return err
	}
	overrides, err := req.Thresholds.toOverrides()
	if err != nil {
		return badRequest("thresholds: " + err.Error())
	}
	mounts, err := req.MountThresholds.toMounts()
	if err != nil {
		return badRequest("mount_thresholds: " + err.Error())
	}
	containers, err := req.ContainerThresholds.toContainers()
	if err != nil {
		return badRequest("container_thresholds: " + err.Error())
	}
	subjects, err := req.SubjectThresholds.toSubjects()
	if err != nil {
		return badRequest("subject_thresholds: " + err.Error())
	}

	if err := d.thresholds.SetHostOverrides(r.Context(), host.ID, overrides, mounts, containers, subjects); err != nil {
		return storeError(err, "sunucu bulunamadı", fail, "set host thresholds")
	}

	targetID := host.ID.String()
	d.logAudit(r, "host.update_thresholds", "host", &targetID, map[string]any{"thresholds": overrides, "mount_thresholds": mounts,
		"container_thresholds": containers, "subject_thresholds": subjects})

	resp, err := d.hostThresholds(r, host)
	if err != nil {
		return serverErr("eşikler kaydedildi ancak geri okunamadı", "set host thresholds: reload", err)
	}
	writeJSON(w, http.StatusOK, resp)
	return nil
}

// canEditThresholds, çağıranın rolünün threshold.edit'e sahip olup olmadığını bildirir. Farklı
// bir izin gerektiren bir isteğin (host oluşturma) eşik de ayarladığı yerlerde kullanılır.
func (d *Deps) canEditThresholds(r *http.Request) (bool, error) {
	role, _ := roleFromContext(r.Context())
	return d.perms.HasPermission(r.Context(), role, "threshold.edit")
}
