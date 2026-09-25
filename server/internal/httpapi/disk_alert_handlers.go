package httpapi

import (
	"errors"
	"log/slog"
	"net/http"
	"sort"

	"github.com/google/uuid"

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
func (d *Deps) handleGetDiskAlerts(w http.ResponseWriter, r *http.Request) {
	host, ok := d.loadHostForView(w, r, "disk alert ayarları alınamadı")
	if !ok {
		return
	}
	settings, err := d.diskAlertSettings(r, host)
	if err != nil {
		slog.ErrorContext(r.Context(), "get disk alerts", "err", err)
		writeError(w, http.StatusInternalServerError, "disk alert ayarları alınamadı")
		return
	}
	writeJSON(w, http.StatusOK, settings)
}

// handleSetDiskAlerts, PUT /api/v1/hosts/:id/disk-alerts'i sunar. Gövde "all_mounts_alert" (true: raporlanan her
// mount; false: yalnızca custom_alert_mounts) içermeli; anahtarı atlamak hatadır, çünkü "gönderilmedi" ile "tüm
// mount'lar" aksi halde ayırt edilemez ve tek bir yazım hatası neyin alert vereceğini değiştirirdi.
func (d *Deps) handleSetDiskAlerts(w http.ResponseWriter, r *http.Request) {
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
		slog.ErrorContext(r.Context(), "set disk alerts: lookup", "err", err)
		writeError(w, http.StatusInternalServerError, "disk alert ayarları güncellenemedi")
		return
	}
	allowed, err := d.requireOrgAccess(r, host.OrganizationID)
	if err != nil {
		slog.ErrorContext(r.Context(), "set disk alerts: check org access", "err", err)
		writeError(w, http.StatusInternalServerError, "disk alert ayarları güncellenemedi")
		return
	}
	if !allowed {
		writeError(w, http.StatusForbidden, "yetkiniz yok")
		return
	}

	var req struct {
		AllMountsAlert    *bool    `json:"all_mounts_alert"`
		CustomAlertMounts []string `json:"custom_alert_mounts"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "geçersiz istek gövdesi")
		return
	}
	if req.AllMountsAlert == nil {
		writeError(w, http.StatusBadRequest, `"all_mounts_alert" zorunlu: raporlanan tüm mount'lar için true, yalnızca seçilenler için false`)
		return
	}
	mounts := req.CustomAlertMounts
	if mounts == nil {
		mounts = []string{}
	}
	if err := model.ValidateMountList(mounts); err != nil {
		writeError(w, http.StatusBadRequest, "custom_alert_mounts: "+err.Error())
		return
	}
	mounts = append([]string(nil), mounts...)
	sort.Strings(mounts) // saklanan (ve GET'in döndürdüğü) sırayla aynı

	if err := d.hosts.SetDiskAlertMounts(r.Context(), id, *req.AllMountsAlert, mounts); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "sunucu bulunamadı")
			return
		}
		slog.ErrorContext(r.Context(), "set disk alerts", "err", err)
		writeError(w, http.StatusInternalServerError, "disk alert ayarları güncellenemedi")
		return
	}

	targetID := id.String()
	d.logAudit(r, "host.update_disk_alerts", "host", &targetID, map[string]any{"all_mounts_alert": *req.AllMountsAlert, "custom_alert_mounts": mounts})

	host.AllMountsAlert, host.CustomAlertMounts = *req.AllMountsAlert, mounts
	settings, err := d.diskAlertSettings(r, host)
	if err != nil {
		slog.ErrorContext(r.Context(), "set disk alerts: reload", "err", err)
		writeError(w, http.StatusInternalServerError, "ayarlar kaydedildi ancak geri okunamadı")
		return
	}
	writeJSON(w, http.StatusOK, settings)
}

// loadHostForView, yolda adı geçen host'ı yükler ve çağıranın onu görmeye yetkili olduğunu
// denetler; değilse hata yanıtını kendisi yazar.
func (d *Deps) loadHostForView(w http.ResponseWriter, r *http.Request, failure string) (model.Host, bool) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "geçersiz sunucu kimliği")
		return model.Host{}, false
	}
	host, err := d.hosts.GetByID(r.Context(), id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "sunucu bulunamadı")
			return model.Host{}, false
		}
		slog.ErrorContext(r.Context(), "lookup host", "failure", failure, "err", err)
		writeError(w, http.StatusInternalServerError, failure)
		return model.Host{}, false
	}
	allowed, err := d.requireHostViewAccess(r, host)
	if err != nil {
		slog.ErrorContext(r.Context(), "check host access", "failure", failure, "err", err)
		writeError(w, http.StatusInternalServerError, failure)
		return model.Host{}, false
	}
	if !allowed {
		writeError(w, http.StatusForbidden, "yetkiniz yok")
		return model.Host{}, false
	}
	return host, true
}
