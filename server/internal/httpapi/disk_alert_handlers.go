package httpapi

import (
	"errors"
	"net/http"
	"sort"

	"healthbeat-server/internal/model"
	"healthbeat-server/internal/store"
)

// diskAlertSettings, panelin birinin bir host'ın hangi mount'larının alert üretebileceğini
// seçebilmesi için ihtiyaç duyduğu şeydir: mevcut seçim ve agent'ın son raporladığı mount'lar.
type diskAlertSettings struct {
	// AllMountsAlert true ise raporlanan her mount alert üretebilir; false ise yalnızca CustomAlertMounts.
	AllMountsAlert    bool     `json:"all_mounts_alert"`
	CustomAlertMounts []string `json:"custom_alert_mounts"`
	// Reported, host'ın son raporundaki mount'lardır (kullanımlarıyla).
	Reported []model.DiskUsage `json:"reported"`
}

func (d *Deps) diskAlertSettings(r *http.Request, host model.Host) (diskAlertSettings, error) {
	reported, err := d.metrics.LatestDisks(r.Context(), host.ID)
	if err != nil {
		return diskAlertSettings{}, err
	}
	return diskAlertSettings{AllMountsAlert: host.AllMountsAlert, CustomAlertMounts: host.CustomAlertMounts, Reported: reported}, nil
}

// handleGetDiskAlerts, GET /api/v1/hosts/:id/disk-alerts'i sunar.
func (d *Deps) handleGetDiskAlerts(w http.ResponseWriter, r *http.Request) error {
	fail := failWith("disk alert ayarları alınamadı")
	host, err := d.viewableHost(r, "get disk alerts", fail)
	if err != nil {
		return err
	}
	settings, err := d.diskAlertSettings(r, host)
	if err != nil {
		return fail("get disk alerts", err)
	}
	writeJSON(w, http.StatusOK, settings)
	return nil
}

type setDiskAlertsRequest struct {
	AllMountsAlert    *bool    `json:"all_mounts_alert"`
	CustomAlertMounts []string `json:"custom_alert_mounts"`
}

// Validate, custom_alert_mounts'u saklanan (ve GET'in döndürdüğü) biçime getirir: nil yerine boş, sıralı.
func (req *setDiskAlertsRequest) Validate() error {
	if req.AllMountsAlert == nil {
		return errors.New(`"all_mounts_alert" zorunlu: raporlanan tüm mount'lar için true, yalnızca seçilenler için false`)
	}
	mounts := req.CustomAlertMounts
	if mounts == nil {
		mounts = []string{}
	}
	if err := model.ValidateMountList(mounts); err != nil {
		return errors.New("custom_alert_mounts: " + err.Error())
	}
	mounts = append([]string(nil), mounts...)
	sort.Strings(mounts)
	req.CustomAlertMounts = mounts
	return nil
}

// handleSetDiskAlerts, PUT /api/v1/hosts/:id/disk-alerts'i sunar. Gövde "all_mounts_alert" (true: raporlanan her
// mount; false: yalnızca custom_alert_mounts) içermeli; anahtarı atlamak hatadır, çünkü "gönderilmedi" ile "tüm
// mount'lar" aksi halde ayırt edilemez ve tek bir yazım hatası neyin alert vereceğini değiştirirdi.
func (d *Deps) handleSetDiskAlerts(w http.ResponseWriter, r *http.Request) error {
	fail := failWith("disk alert ayarları güncellenemedi")
	host, err := d.managedHost(r, "set disk alerts", fail)
	if err != nil {
		return err
	}
	req, err := bind[setDiskAlertsRequest](r)
	if err != nil {
		return err
	}
	allMounts, mounts := *req.AllMountsAlert, req.CustomAlertMounts

	err = d.hosts.SetDiskAlertMounts(r.Context(), host.ID, allMounts, mounts)
	if errors.Is(err, store.ErrNotFound) {
		return notFound("sunucu bulunamadı")
	}
	if err != nil {
		return fail("set disk alerts", err)
	}

	targetID := host.ID.String()
	d.logAudit(r, "host.update_disk_alerts", "host", &targetID, map[string]any{"all_mounts_alert": allMounts, "custom_alert_mounts": mounts})

	host.AllMountsAlert, host.CustomAlertMounts = allMounts, mounts
	settings, err := d.diskAlertSettings(r, host)
	if err != nil {
		return serverErr("ayarlar kaydedildi ancak geri okunamadı", "set disk alerts: reload", err)
	}
	writeJSON(w, http.StatusOK, settings)
	return nil
}
