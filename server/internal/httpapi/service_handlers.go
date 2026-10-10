package httpapi

import (
	"errors"
	"net/http"
	"sort"

	"healthbeat-server/internal/model"
	"healthbeat-server/internal/store"
)

// hostServices, bir sunucunun servis listesi ve izlenen servis seçimidir (GET/PUT yanıtı). Watched seçimin tamamıdır:
// şu an raporlanmayan izlenen servisler Services'te yoktur, panel onları "raporlanmıyor" diye gösterir.
type hostServices struct {
	Services []model.HostService `json:"services"`
	Watched  []string            `json:"watched"`
}

func (d *Deps) hostServices(r *http.Request, host model.Host) (hostServices, error) {
	services, err := d.hosts.Services(r.Context(), host.ID)
	if err != nil {
		return hostServices{}, err
	}
	watched, err := d.hosts.WatchedServices(r.Context(), host.ID)
	if err != nil {
		return hostServices{}, err
	}
	return hostServices{Services: services, Watched: watched}, nil
}

// handleGetHostServices, GET /api/v1/hosts/:id/services'i sunar: agent'ın bildirdiği systemd servisleri (protokol 4)
// ve izlenen servis seçimi. Eski agent'ta liste boştur.
func (d *Deps) handleGetHostServices(w http.ResponseWriter, r *http.Request) error {
	fail := failWith("servisler alınamadı")
	host, err := d.viewableHost(r, "get host services", fail)
	if err != nil {
		return err
	}
	resp, err := d.hostServices(r, host)
	if err != nil {
		return fail("get host services", err)
	}
	writeJSON(w, http.StatusOK, resp)
	return nil
}

type setWatchedServicesRequest struct {
	Services []string `json:"services"`
}

// Validate, seçimi saklanan (ve GET'in döndürdüğü) biçime getirir: sıralı.
func (req *setWatchedServicesRequest) Validate() error {
	if req.Services == nil {
		return errors.New(`"services" zorunlu: izlenecek servis adları (boş liste = hiçbiri)`)
	}
	if err := model.ValidateServiceList(req.Services); err != nil {
		return errors.New("services: " + err.Error())
	}
	names := append([]string(nil), req.Services...)
	sort.Strings(names)
	req.Services = names
	return nil
}

// handleSetWatchedServices, PUT /api/v1/hosts/:id/watched-services'i sunar: izlenen servis seçimini değiştirir.
// İzlenen servis çalışmazsa alert üretir; seçim yalnızca sunucu bazındadır (disk alert seçimi gibi).
func (d *Deps) handleSetWatchedServices(w http.ResponseWriter, r *http.Request) error {
	fail := failWith("izlenen servisler güncellenemedi")
	host, err := d.managedHost(r, "set watched services", fail)
	if err != nil {
		return err
	}
	req, err := bind[setWatchedServicesRequest](r)
	if err != nil {
		return err
	}

	err = d.hosts.SetWatchedServices(r.Context(), host.ID, req.Services)
	if errors.Is(err, store.ErrNotFound) {
		return notFound("sunucu bulunamadı")
	}
	if err != nil {
		return fail("set watched services", err)
	}

	targetID := host.ID.String()
	d.logAudit(r, "host.update_watched_services", "host", &targetID, map[string]any{"services": req.Services})

	resp, err := d.hostServices(r, host)
	if err != nil {
		return serverErr("seçim kaydedildi ancak geri okunamadı", "set watched services: reload", err)
	}
	writeJSON(w, http.StatusOK, resp)
	return nil
}
