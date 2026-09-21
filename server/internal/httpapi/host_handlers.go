package httpapi

import (
	"errors"
	"log"
	"net"
	"net/http"
	"strconv"
	"strings"

	"github.com/google/uuid"

	"healthbeat-server/internal/authsvc"
	"healthbeat-server/internal/model"
	"healthbeat-server/internal/store"
)

// requireOrgAccess (organization_handlers.go'da tanımlı) organizasyon kimliğine göre
// super_admin/org_admin kapsam denetimini zaten uygular — host'lar onu yeniden kullanır,
// çünkü host.create/update/delete yalnızca bu iki role verilir (bkz.
// migrations/000013_seed_role_permissions).

type createHostRequest struct {
	OrganizationID uuid.UUID `json:"organization_id"`
	// Title, panelde görünen addır (organizasyon içinde benzersiz). Makinenin kendi hostname'i agent'tan gelir.
	Title           string  `json:"title"`
	IP              string  `json:"ip"`
	Mode            string  `json:"mode"`
	IntervalSeconds int     `json:"interval_seconds"`
	PullPort        *int    `json:"pull_port,omitempty"`
	PullEndpoint    *string `json:"pull_endpoint,omitempty"`
	// AllMountsAlert: verilmemiş ya da true = raporlanan her mount için alert; false = yalnızca
	// CustomAlertMounts'taki mount'lar. Şimdi (ilk rapordan önce) ya da sonra PUT /hosts/:id/disk-alerts
	// ile ayarlanabilir.
	AllMountsAlert    *bool    `json:"all_mounts_alert"`
	CustomAlertMounts []string `json:"custom_alert_mounts"`
	// Thresholds, host'ın özel eşikleridir (metrik türü -> {warning_level, critical_level});
	// verilmemiş, null ya da null girdi = varsayılanı kullan. Host ile aynı transaction'da
	// yazılır.
	Thresholds thresholdOverridesInput `json:"thresholds"`
	// MountThresholds mount başına disk eşikleridir (mount yolu -> {warning_level,
	// critical_level}); null girdiler yok sayılır. Aynı transaction'da yazılır.
	MountThresholds thresholdSubjectsInput `json:"mount_thresholds"`
	// ContainerThresholds, container başına docker_restart eşikleridir (container adı ->
	// {warning_level, critical_level}); null girdiler yok sayılır. Aynı transaction'da yazılır.
	ContainerThresholds thresholdSubjectsInput `json:"container_thresholds"`
}

type hostResponse struct {
	model.Host
	// SameMachineAs, aynı /etc/machine-id özetini bildiren diğer sunucular (çift kayıt uyarısı); yalnızca tek sunucu
	// yanıtında. Klonlanmış sanal makineler aynı kimliği taşıyabilir, bu yüzden yalnızca uyarıdır.
	SameMachineAs []store.HostRef `json:"same_machine_as,omitempty"`
	APIToken      *string         `json:"api_token,omitempty"`
	PullSecret    *string         `json:"pull_secret,omitempty"`
}

func (d *Deps) handleCreateHost(w http.ResponseWriter, r *http.Request) {
	var req createHostRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "geçersiz istek gövdesi")
		return
	}

	title := strings.TrimSpace(req.Title)
	if req.OrganizationID == uuid.Nil || title == "" {
		writeError(w, http.StatusBadRequest, "organization_id ve title zorunlu")
		return
	}
	if net.ParseIP(req.IP) == nil {
		writeError(w, http.StatusBadRequest, "ip geçerli bir IP adresi olmalı")
		return
	}
	if !model.ValidHostMode(req.Mode) {
		writeError(w, http.StatusBadRequest, "mode push veya pull olmalı")
		return
	}
	if req.IntervalSeconds <= 0 {
		writeError(w, http.StatusBadRequest, "interval_seconds pozitif olmalı")
		return
	}
	if err := model.ValidateMountList(req.CustomAlertMounts); err != nil {
		writeError(w, http.StatusBadRequest, "custom_alert_mounts: "+err.Error())
		return
	}
	allMounts := req.AllMountsAlert == nil || *req.AllMountsAlert
	overrides, err := req.Thresholds.toOverrides()
	if err != nil {
		writeError(w, http.StatusBadRequest, "thresholds: "+err.Error())
		return
	}
	mountOverrides, err := req.MountThresholds.toMounts()
	if err != nil {
		writeError(w, http.StatusBadRequest, "mount_thresholds: "+err.Error())
		return
	}
	containerOverrides, err := req.ContainerThresholds.toContainers()
	if err != nil {
		writeError(w, http.StatusBadRequest, "container_thresholds: "+err.Error())
		return
	}

	// requireOrgAccess oluşturma sırasında FK üzerinden organizasyonun var olduğunu da doğrular,
	// ama burada önce denetlemek ham 400 yerine temiz bir 404 verir.
	if _, err := d.organizations.GetByID(r.Context(), req.OrganizationID); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "organizasyon bulunamadı")
			return
		}
		log.Printf("create host: lookup organization: %v", err)
		writeError(w, http.StatusInternalServerError, "sunucu oluşturulamadı")
		return
	}
	allowed, err := d.requireOrgAccess(r, req.OrganizationID)
	if err != nil {
		log.Printf("create host: check org access: %v", err)
		writeError(w, http.StatusInternalServerError, "sunucu oluşturulamadı")
		return
	}
	if !allowed {
		writeError(w, http.StatusForbidden, "yetkiniz yok")
		return
	}
	if overrides.HasCustom() || mountOverrides.HasCustom() || containerOverrides.HasCustom() {
		mayEdit, err := d.canEditThresholds(r)
		if err != nil {
			log.Printf("create host: check threshold.edit: %v", err)
			writeError(w, http.StatusInternalServerError, "sunucu oluşturulamadı")
			return
		}
		if !mayEdit {
			writeError(w, http.StatusForbidden, "yetkiniz yok: özel eşik girmek için threshold.edit izni gerekir")
			return
		}
	}

	params := store.CreateHostParams{
		OrganizationID:      req.OrganizationID,
		Title:               title,
		IP:                  req.IP,
		Mode:                req.Mode,
		IntervalSeconds:     req.IntervalSeconds,
		AllMountsAlert:      allMounts,
		CustomAlertMounts:   req.CustomAlertMounts,
		Thresholds:          overrides,
		MountThresholds:     mountOverrides,
		ContainerThresholds: containerOverrides,
	}

	var plainToken, plainSecret string
	switch req.Mode {
	case model.HostModePush:
		plainToken, err = authsvc.GenerateOpaqueSecret()
		if err != nil {
			log.Printf("create host: generate api token: %v", err)
			writeError(w, http.StatusInternalServerError, "sunucu oluşturulamadı")
			return
		}
		hash := authsvc.HashOpaqueSecret(plainToken)
		params.APITokenHash = &hash
	case model.HostModePull:
		if req.PullPort == nil || req.PullEndpoint == nil || strings.TrimSpace(*req.PullEndpoint) == "" {
			writeError(w, http.StatusBadRequest, "pull modunda pull_port ve pull_endpoint zorunlu")
			return
		}
		if *req.PullPort <= 0 || *req.PullPort > 65535 {
			writeError(w, http.StatusBadRequest, "pull_port 1 ile 65535 arasında olmalı")
			return
		}
		plainSecret, err = authsvc.GenerateOpaqueSecret()
		if err != nil {
			log.Printf("create host: generate pull secret: %v", err)
			writeError(w, http.StatusInternalServerError, "sunucu oluşturulamadı")
			return
		}
		params.PullPort = req.PullPort
		params.PullEndpoint = req.PullEndpoint
		params.PullSecret = &plainSecret
	}

	host, err := d.hosts.Create(r.Context(), params)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "organizasyon bulunamadı")
			return
		}
		if errors.Is(err, store.ErrConflict) {
			writeError(w, http.StatusConflict, err.Error())
			return
		}
		log.Printf("create host: %v", err)
		writeError(w, http.StatusInternalServerError, "sunucu oluşturulamadı")
		return
	}

	targetID := host.ID.String()
	d.logAudit(r, "host.create", "host", &targetID, map[string]any{
		"organization_id":      host.OrganizationID,
		"title":                host.Title,
		"mode":                 host.Mode,
		"thresholds":           overrides,
		"mount_thresholds":     mountOverrides,
		"container_thresholds": containerOverrides,
	})

	resp := hostResponse{Host: host}
	if plainToken != "" {
		resp.APIToken = &plainToken
	}
	if plainSecret != "" {
		resp.PullSecret = &plainSecret
	}
	writeJSON(w, http.StatusCreated, resp)
}

// requireHostViewAccess, host.view'ın rol başına kapsamını uygular: super_admin her şeyi
// görür, org_admin yalnızca atandığı organizasyonlardaki host'ları, operator yalnızca
// user_hosts ile kendisine doğrudan atanmış host'ları.
func (d *Deps) requireHostViewAccess(r *http.Request, host model.Host) (bool, error) {
	role, _ := roleFromContext(r.Context())
	userID, _ := userIDFromContext(r.Context())

	switch role {
	case model.RoleSuperAdmin:
		return true, nil
	case model.RoleOrgAdmin:
		return d.userOrgs.IsAssigned(r.Context(), userID, host.OrganizationID)
	case model.RoleOperator:
		return d.userHosts.IsAssigned(r.Context(), userID, host.ID)
	default:
		return false, nil
	}
}

func (d *Deps) handleGetHost(w http.ResponseWriter, r *http.Request) {
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
		log.Printf("get host: %v", err)
		writeError(w, http.StatusInternalServerError, "sunucu alınamadı")
		return
	}

	allowed, err := d.requireHostViewAccess(r, host)
	if err != nil {
		log.Printf("get host: check access: %v", err)
		writeError(w, http.StatusInternalServerError, "sunucu alınamadı")
		return
	}
	if !allowed {
		writeError(w, http.StatusForbidden, "yetkiniz yok")
		return
	}

	resp := hostResponse{Host: host}
	if same, err := d.hosts.SameMachineHosts(r.Context(), host.ID); err != nil {
		log.Printf("get host: same machine lookup: %v", err) // uyarı tamamlayıcıdır; yokluğu yanıtı bozmamalı
	} else {
		// Yalnızca çağıranın görebildiği sunucular (başka organizasyonun makinesi sızmasın).
		for _, ref := range same {
			if ok, err := d.requireHostViewAccess(r, model.Host{ID: ref.ID, OrganizationID: ref.OrganizationID}); err == nil && ok {
				resp.SameMachineAs = append(resp.SameMachineAs, ref)
			}
		}
	}
	writeJSON(w, http.StatusOK, resp)
}

// handleListOrganizationHosts, GET /organizations/:id/hosts'ı sunar. ?q= title/IP'de alt
// dize arar; ?limit=&offset= verilmezse (geriye dönük uyumlu) tüm sunucular döner. Toplam sayı
// X-Total-Count başlığındadır.
func (d *Deps) handleListOrganizationHosts(w http.ResponseWriter, r *http.Request) {
	orgID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "geçersiz organizasyon kimliği")
		return
	}
	p, err := parseListParams(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	role, _ := roleFromContext(r.Context())
	userID, _ := userIDFromContext(r.Context())

	switch role {
	case model.RoleSuperAdmin:
		// aşağıdaki filtresiz listeye düşer
	case model.RoleOrgAdmin:
		allowed, err := d.userOrgs.IsAssigned(r.Context(), userID, orgID)
		if err != nil {
			log.Printf("list organization hosts: check org access: %v", err)
			writeError(w, http.StatusInternalServerError, "sunucular listelenemedi")
			return
		}
		if !allowed {
			writeError(w, http.StatusForbidden, "yetkiniz yok")
			return
		}
	case model.RoleOperator:
		hostIDs, err := d.userHosts.ListHostIDs(r.Context(), userID)
		if err != nil {
			log.Printf("list organization hosts: list assigned hosts: %v", err)
			writeError(w, http.StatusInternalServerError, "sunucular listelenemedi")
			return
		}
		hosts, total, err := d.hosts.ListByOrganizationFiltered(r.Context(), orgID, hostIDs, p)
		if err != nil {
			log.Printf("list organization hosts: %v", err)
			writeError(w, http.StatusInternalServerError, "sunucular listelenemedi")
			return
		}
		w.Header().Set("X-Total-Count", strconv.Itoa(total))
		writeJSON(w, http.StatusOK, hosts)
		return
	default:
		writeError(w, http.StatusForbidden, "yetkiniz yok")
		return
	}

	hosts, total, err := d.hosts.ListByOrganization(r.Context(), orgID, p)
	if err != nil {
		log.Printf("list organization hosts: %v", err)
		writeError(w, http.StatusInternalServerError, "sunucular listelenemedi")
		return
	}
	w.Header().Set("X-Total-Count", strconv.Itoa(total))
	writeJSON(w, http.StatusOK, hosts)
}

type updateHostRequest struct {
	Title           *string `json:"title"`
	IP              *string `json:"ip"`
	IntervalSeconds *int    `json:"interval_seconds"`
}

func (d *Deps) handleUpdateHost(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "geçersiz sunucu kimliği")
		return
	}

	existing, err := d.hosts.GetByID(r.Context(), id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "sunucu bulunamadı")
			return
		}
		log.Printf("update host: lookup: %v", err)
		writeError(w, http.StatusInternalServerError, "sunucu güncellenemedi")
		return
	}
	allowed, err := d.requireOrgAccess(r, existing.OrganizationID)
	if err != nil {
		log.Printf("update host: check org access: %v", err)
		writeError(w, http.StatusInternalServerError, "sunucu güncellenemedi")
		return
	}
	if !allowed {
		writeError(w, http.StatusForbidden, "yetkiniz yok")
		return
	}

	var req updateHostRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "geçersiz istek gövdesi")
		return
	}
	if req.Title != nil {
		trimmed := strings.TrimSpace(*req.Title)
		req.Title = &trimmed
	}
	if req.IP != nil && net.ParseIP(*req.IP) == nil {
		writeError(w, http.StatusBadRequest, "ip geçerli bir IP adresi olmalı")
		return
	}
	if req.IntervalSeconds != nil && *req.IntervalSeconds <= 0 {
		writeError(w, http.StatusBadRequest, "interval_seconds pozitif olmalı")
		return
	}

	host, err := d.hosts.Update(r.Context(), id, req.Title, req.IP, req.IntervalSeconds)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "sunucu bulunamadı")
			return
		}
		if errors.Is(err, store.ErrConflict) {
			writeError(w, http.StatusConflict, err.Error())
			return
		}
		log.Printf("update host: %v", err)
		writeError(w, http.StatusInternalServerError, "sunucu güncellenemedi")
		return
	}

	targetID := host.ID.String()
	d.logAudit(r, "host.update", "host", &targetID, map[string]any{"title": host.Title})
	writeJSON(w, http.StatusOK, host)
}

func (d *Deps) handleDeleteHost(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "geçersiz sunucu kimliği")
		return
	}

	existing, err := d.hosts.GetByID(r.Context(), id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "sunucu bulunamadı")
			return
		}
		log.Printf("delete host: lookup: %v", err)
		writeError(w, http.StatusInternalServerError, "sunucu silinemedi")
		return
	}
	allowed, err := d.requireOrgAccess(r, existing.OrganizationID)
	if err != nil {
		log.Printf("delete host: check org access: %v", err)
		writeError(w, http.StatusInternalServerError, "sunucu silinemedi")
		return
	}
	if !allowed {
		writeError(w, http.StatusForbidden, "yetkiniz yok")
		return
	}

	if err := d.hosts.Delete(r.Context(), id); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "sunucu bulunamadı")
			return
		}
		log.Printf("delete host: %v", err)
		writeError(w, http.StatusInternalServerError, "sunucu silinemedi")
		return
	}

	targetID := id.String()
	d.logAudit(r, "host.delete", "host", &targetID, nil)
	w.WriteHeader(http.StatusNoContent)
}

// handleRotateHostCredentials, host için yeni bir api_token (push) ya da pull_secret
// (pull) üretir ve öncekini geçersiz kılar. Kimlik bilgisi ele geçirilmesinden kurtarma için
// gerekir — docs/MIMARI.md'nin örnek endpoint listesinde yok ama bölüm 5'in güvenlik
// gereksinimleri göz önüne alındığında makul bir ekleme.
func (d *Deps) handleRotateHostCredentials(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "geçersiz sunucu kimliği")
		return
	}

	existing, err := d.hosts.GetByID(r.Context(), id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "sunucu bulunamadı")
			return
		}
		log.Printf("rotate host credentials: lookup: %v", err)
		writeError(w, http.StatusInternalServerError, "kimlik bilgisi yenilenemedi")
		return
	}
	allowed, err := d.requireOrgAccess(r, existing.OrganizationID)
	if err != nil {
		log.Printf("rotate host credentials: check org access: %v", err)
		writeError(w, http.StatusInternalServerError, "kimlik bilgisi yenilenemedi")
		return
	}
	if !allowed {
		writeError(w, http.StatusForbidden, "yetkiniz yok")
		return
	}

	secret, err := authsvc.GenerateOpaqueSecret()
	if err != nil {
		log.Printf("rotate host credentials: generate: %v", err)
		writeError(w, http.StatusInternalServerError, "kimlik bilgisi yenilenemedi")
		return
	}

	resp := hostResponse{Host: existing}
	switch existing.Mode {
	case model.HostModePush:
		hash := authsvc.HashOpaqueSecret(secret)
		if err := d.hosts.UpdateAPITokenHash(r.Context(), id, hash); err != nil {
			log.Printf("rotate host credentials: update token hash: %v", err)
			writeError(w, http.StatusInternalServerError, "kimlik bilgisi yenilenemedi")
			return
		}
		resp.APIToken = &secret
	case model.HostModePull:
		// Hash'lenmeden düz metin olarak saklanır — server bu secret'ı her poll'da host'a
		// sunar (bkz. migrations/000016).
		if err := d.hosts.UpdatePullSecret(r.Context(), id, secret); err != nil {
			log.Printf("rotate host credentials: update pull secret: %v", err)
			writeError(w, http.StatusInternalServerError, "kimlik bilgisi yenilenemedi")
			return
		}
		resp.PullSecret = &secret
	}

	targetID := id.String()
	d.logAudit(r, "host.rotate_credentials", "host", &targetID, nil)
	writeJSON(w, http.StatusOK, resp)
}
