package httpapi

import (
	"errors"
	"log/slog"
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
// çünkü host.create/update/delete yalnızca bu iki role verilir (bkz. migrations/000001_baseline,
// role_permissions tohumu).

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

// Validate, istek içinde kalan kuralları denetler; eşik girdileri handler'da çevrilir (çevrilmiş değerleri gerekir).
func (req *createHostRequest) Validate() error {
	req.Title = strings.TrimSpace(req.Title)
	if req.OrganizationID == uuid.Nil || req.Title == "" {
		return errors.New("organization_id ve title zorunlu")
	}
	if err := validateIP(req.IP); err != nil {
		return err
	}
	if !model.ValidHostMode(req.Mode) {
		return errors.New("mode push veya pull olmalı")
	}
	if err := validateInterval(req.IntervalSeconds); err != nil {
		return err
	}
	if err := model.ValidateMountList(req.CustomAlertMounts); err != nil {
		return errors.New("custom_alert_mounts: " + err.Error())
	}
	return nil
}

func (d *Deps) handleCreateHost(w http.ResponseWriter, r *http.Request) error {
	fail := failWith("sunucu oluşturulamadı")
	req, err := bind[createHostRequest](r)
	if err != nil {
		return err
	}
	allMounts := req.AllMountsAlert == nil || *req.AllMountsAlert
	overrides, err := req.Thresholds.toOverrides()
	if err != nil {
		return badRequest("thresholds: " + err.Error())
	}
	mountOverrides, err := req.MountThresholds.toMounts()
	if err != nil {
		return badRequest("mount_thresholds: " + err.Error())
	}
	containerOverrides, err := req.ContainerThresholds.toContainers()
	if err != nil {
		return badRequest("container_thresholds: " + err.Error())
	}

	// requireOrgAccess oluşturma sırasında FK üzerinden organizasyonun var olduğunu da doğrular,
	// ama burada önce denetlemek ham 400 yerine temiz bir 404 verir.
	if _, err := d.organizations.GetByID(r.Context(), req.OrganizationID); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return notFound("organizasyon bulunamadı")
		}
		return fail("create host: lookup organization", err)
	}
	allowed, err := d.requireOrgAccess(r, req.OrganizationID)
	if err != nil {
		return fail("create host: check org access", err)
	}
	if !allowed {
		return forbidden()
	}
	if overrides.HasCustom() || mountOverrides.HasCustom() || containerOverrides.HasCustom() {
		mayEdit, err := d.canEditThresholds(r)
		if err != nil {
			return fail("create host: check threshold.edit", err)
		}
		if !mayEdit {
			return newError(http.StatusForbidden, "yetkiniz yok: özel eşik girmek için threshold.edit izni gerekir")
		}
	}

	params := store.CreateHostParams{
		OrganizationID:      req.OrganizationID,
		Title:               req.Title,
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
			return fail("create host: generate api token", err)
		}
		hash := authsvc.HashOpaqueSecret(plainToken)
		params.APITokenHash = &hash
	case model.HostModePull:
		// Yetki denetiminden sonra: pull ayarı hatalı ama yetkisiz bir istek 403 almaya devam eder.
		if req.PullPort == nil || req.PullEndpoint == nil || strings.TrimSpace(*req.PullEndpoint) == "" {
			return badRequest("pull modunda pull_port ve pull_endpoint zorunlu")
		}
		if *req.PullPort <= 0 || *req.PullPort > 65535 {
			return badRequest("pull_port 1 ile 65535 arasında olmalı")
		}
		plainSecret, err = authsvc.GenerateOpaqueSecret()
		if err != nil {
			return fail("create host: generate pull secret", err)
		}
		params.PullPort = req.PullPort
		params.PullEndpoint = req.PullEndpoint
		params.PullSecret = &plainSecret
	}

	host, err := d.hosts.Create(r.Context(), params)
	switch {
	case errors.Is(err, store.ErrNotFound):
		return notFound("organizasyon bulunamadı")
	case errors.Is(err, store.ErrConflict):
		return conflict(err.Error())
	case err != nil:
		return fail("create host", err)
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
	return nil
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

func (d *Deps) handleGetHost(w http.ResponseWriter, r *http.Request) error {
	host, err := d.viewableHost(r, "get host", failWith("sunucu alınamadı"))
	if err != nil {
		return err
	}

	resp := hostResponse{Host: host}
	if same, err := d.hosts.SameMachineHosts(r.Context(), host.ID); err != nil {
		slog.WarnContext(r.Context(), "get host: same machine lookup", "err", err) // uyarı tamamlayıcıdır; yokluğu yanıtı bozmamalı
	} else {
		// Yalnızca çağıranın görebildiği sunucular (başka organizasyonun makinesi sızmasın).
		for _, ref := range same {
			if ok, err := d.requireHostViewAccess(r, model.Host{ID: ref.ID, OrganizationID: ref.OrganizationID}); err == nil && ok {
				resp.SameMachineAs = append(resp.SameMachineAs, ref)
			}
		}
	}
	writeJSON(w, http.StatusOK, resp)
	return nil
}

// handleListOrganizationHosts, GET /organizations/:id/hosts'ı sunar. ?q= title/IP'de alt
// dize arar; ?limit=&offset= verilmezse (geriye dönük uyumlu) tüm sunucular döner. Toplam sayı
// X-Total-Count başlığındadır.
func (d *Deps) handleListOrganizationHosts(w http.ResponseWriter, r *http.Request) error {
	fail := failWith("sunucular listelenemedi")
	orgID, err := pathID(r, "geçersiz organizasyon kimliği")
	if err != nil {
		return err
	}
	p, err := parseListParams(r)
	if err != nil {
		return badRequest(err.Error())
	}

	role, _ := roleFromContext(r.Context())
	userID, _ := userIDFromContext(r.Context())

	var (
		hosts []model.Host
		total int
	)
	switch role {
	case model.RoleSuperAdmin:
		hosts, total, err = d.hosts.ListByOrganization(r.Context(), orgID, p)
	case model.RoleOrgAdmin:
		allowed, accessErr := d.userOrgs.IsAssigned(r.Context(), userID, orgID)
		if accessErr != nil {
			return fail("list organization hosts: check org access", accessErr)
		}
		if !allowed {
			return forbidden()
		}
		hosts, total, err = d.hosts.ListByOrganization(r.Context(), orgID, p)
	case model.RoleOperator:
		hostIDs, idsErr := d.userHosts.ListHostIDs(r.Context(), userID)
		if idsErr != nil {
			return fail("list organization hosts: list assigned hosts", idsErr)
		}
		hosts, total, err = d.hosts.ListByOrganizationFiltered(r.Context(), orgID, hostIDs, p)
	default:
		return forbidden()
	}
	if err != nil {
		return fail("list organization hosts", err)
	}
	w.Header().Set("X-Total-Count", strconv.Itoa(total))
	writeJSON(w, http.StatusOK, hosts)
	return nil
}

type updateHostRequest struct {
	Title           *string `json:"title"`
	IP              *string `json:"ip"`
	IntervalSeconds *int    `json:"interval_seconds"`
}

func (req *updateHostRequest) Validate() error {
	if req.Title != nil {
		trimmed := strings.TrimSpace(*req.Title)
		req.Title = &trimmed
	}
	if req.IP != nil {
		if err := validateIP(*req.IP); err != nil {
			return err
		}
	}
	if req.IntervalSeconds != nil {
		if err := validateInterval(*req.IntervalSeconds); err != nil {
			return err
		}
	}
	return nil
}

// viewableHost, yoldaki {id} sunucusunu yükler ve çağıranın onu görebildiğini denetler (requireHostViewAccess).
// Beklenmeyen hatalar fail ile, op öneki taşıyarak loglanır.
func (d *Deps) viewableHost(r *http.Request, op string, fail failFunc) (model.Host, error) {
	return d.hostFromPath(r, op+": lookup host", op+": check access", fail, d.requireHostViewAccess)
}

// managedHost, yoldaki {id} sunucusunu yükler ve çağıranın onun organizasyonunu yönettiğini denetler (host.update/
// delete yalnızca super_admin ve org_admin'e verilir; bkz. requireOrgAccess).
func (d *Deps) managedHost(r *http.Request, op string, fail failFunc) (model.Host, error) {
	return d.hostFromPath(r, op+": lookup", op+": check org access", fail, func(r *http.Request, host model.Host) (bool, error) {
		return d.requireOrgAccess(r, host.OrganizationID)
	})
}

func (d *Deps) hostFromPath(r *http.Request, lookupOp, accessOp string, fail failFunc,
	allowed func(*http.Request, model.Host) (bool, error)) (model.Host, error) {
	id, err := pathID(r, "geçersiz sunucu kimliği")
	if err != nil {
		return model.Host{}, err
	}
	host, err := d.hosts.GetByID(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		return model.Host{}, notFound("sunucu bulunamadı")
	}
	if err != nil {
		return model.Host{}, fail(lookupOp, err)
	}
	ok, err := allowed(r, host)
	if err != nil {
		return model.Host{}, fail(accessOp, err)
	}
	if !ok {
		return model.Host{}, forbidden()
	}
	return host, nil
}

func (d *Deps) handleUpdateHost(w http.ResponseWriter, r *http.Request) error {
	fail := failWith("sunucu güncellenemedi")
	existing, err := d.managedHost(r, "update host", fail)
	if err != nil {
		return err
	}
	req, err := bind[updateHostRequest](r)
	if err != nil {
		return err
	}

	host, err := d.hosts.Update(r.Context(), existing.ID, req.Title, req.IP, req.IntervalSeconds)
	switch {
	case errors.Is(err, store.ErrNotFound):
		return notFound("sunucu bulunamadı")
	case errors.Is(err, store.ErrConflict):
		return conflict(err.Error())
	case err != nil:
		return fail("update host", err)
	}

	targetID := host.ID.String()
	d.logAudit(r, "host.update", "host", &targetID, map[string]any{"title": host.Title})
	writeJSON(w, http.StatusOK, host)
	return nil
}

func (d *Deps) handleDeleteHost(w http.ResponseWriter, r *http.Request) error {
	fail := failWith("sunucu silinemedi")
	existing, err := d.managedHost(r, "delete host", fail)
	if err != nil {
		return err
	}
	id := existing.ID

	err = d.hosts.Delete(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		return notFound("sunucu bulunamadı")
	}
	if err != nil {
		return fail("delete host", err)
	}

	targetID := id.String()
	d.logAudit(r, "host.delete", "host", &targetID, nil)
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// handleRotateHostCredentials, host için yeni bir api_token (push) ya da pull_secret
// (pull) üretir ve öncekini geçersiz kılar. Kimlik bilgisi ele geçirilmesinden kurtarma için
// gerekir — docs/MIMARI.md'nin örnek endpoint listesinde yok ama bölüm 5'in güvenlik
// gereksinimleri göz önüne alındığında makul bir ekleme.
func (d *Deps) handleRotateHostCredentials(w http.ResponseWriter, r *http.Request) error {
	fail := failWith("kimlik bilgisi yenilenemedi")
	existing, err := d.managedHost(r, "rotate host credentials", fail)
	if err != nil {
		return err
	}
	id := existing.ID

	secret, err := authsvc.GenerateOpaqueSecret()
	if err != nil {
		return fail("rotate host credentials: generate", err)
	}

	resp := hostResponse{Host: existing}
	switch existing.Mode {
	case model.HostModePush:
		hash := authsvc.HashOpaqueSecret(secret)
		if err := d.hosts.UpdateAPITokenHash(r.Context(), id, hash); err != nil {
			return fail("rotate host credentials: update token hash", err)
		}
		resp.APIToken = &secret
	case model.HostModePull:
		// Push token'ından farklı olarak hash'lenemez — server bu secret'ı her poll'da host'a
		// sunar; bu yüzden store onu at-rest şifreler (hosts.pull_secret_enc).
		if err := d.hosts.UpdatePullSecret(r.Context(), id, secret); err != nil {
			return fail("rotate host credentials: update pull secret", err)
		}
		resp.PullSecret = &secret
	}

	targetID := id.String()
	d.logAudit(r, "host.rotate_credentials", "host", &targetID, nil)
	writeJSON(w, http.StatusOK, resp)
	return nil
}
