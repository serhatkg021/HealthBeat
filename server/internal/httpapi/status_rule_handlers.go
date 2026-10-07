package httpapi

import (
	"errors"
	"net/http"

	"github.com/google/uuid"

	"healthbeat-server/internal/model"
	"healthbeat-server/internal/store"
)

// Durum kuralları: eşiği olmayan alert'lerin (servis çalışmıyor, RAID bozuk …) seviyesi ve süresi. Kapsam ve yetki
// eşiklerle aynıdır: genel (yalnızca super_admin) ya da organizasyon kuralları /status-rules'ta, sunucunun kendi
// kuralları /hosts/:id/status-rules'ta. Hiç tanımlanmamış kural kapalıdır.

// handleListStatusRules, GET /api/v1/status-rules'u sunar: genel kurallar ve çağıranın görebildiği organizasyonların
// kuralları (üst zincirinkiler dahil: sunucuya uygulanan değer oradan miras gelebilir; bkz. handleListThresholds).
func (d *Deps) handleListStatusRules(w http.ResponseWriter, r *http.Request) error {
	fail := failWith("durum kuralları listelenemedi")
	scope := d.scope(r)
	var orgIDs []uuid.UUID // nil = hepsi (super_admin)
	if !scope.IsSuperAdmin() {
		managed, err := scope.ManagedOrgIDs(r.Context())
		if err != nil {
			return fail("list status rules: list user organizations", err)
		}
		contextIDs, err := scope.ContextOrgIDs(r.Context())
		if err != nil {
			return fail("list status rules: list context organizations", err)
		}
		orgIDs = append(append([]uuid.UUID{}, managed...), contextIDs...)
	}
	rules, err := d.thresholds.ListStatusRules(r.Context(), orgIDs)
	if err != nil {
		return fail("list status rules", err)
	}
	writeJSON(w, http.StatusOK, rules)
	return nil
}

type setStatusRulesRequest struct {
	// OrganizationID boşsa genel kurallar (yalnızca super_admin), doluysa o organizasyonun (ve altındaki dalın) kuralları.
	OrganizationID *uuid.UUID              `json:"organization_id"`
	Rules          model.StatusRuleChanges `json:"rules"`
}

func (req *setStatusRulesRequest) Validate() error {
	if req.Rules == nil {
		return errors.New(`"rules" zorunlu: kural -> {level, duration_seconds} ya da null (üst kapsamı izle)`)
	}
	return req.Rules.Validate()
}

// handleSetStatusRules, PUT /api/v1/status-rules'u sunar. Gövde {"organization_id": null, "rules": {"service_failed":
// {"level": "critical", "duration_seconds": 60}, "reboot_required": null}} biçimindedir: bir ayar o kapsamdaki kuralı
// yazar, null onu kaldırır (üst kapsamı izler), dışarıda bırakılan kurallara dokunulmaz. Hepsi birlikte uygulanır.
func (d *Deps) handleSetStatusRules(w http.ResponseWriter, r *http.Request) error {
	fail := failWith("durum kuralları güncellenemedi")
	req, err := bind[setStatusRulesRequest](r)
	if err != nil {
		return err
	}
	allowed, err := d.scope(r).CanManageThreshold(r.Context(), req.OrganizationID)
	switch {
	case errors.Is(err, store.ErrNotFound):
		return notFound("organizasyon bulunamadı")
	case err != nil:
		return fail("set status rules: check access", err)
	case !allowed:
		return forbidden()
	}
	if err := d.thresholds.SetStatusRules(r.Context(), req.OrganizationID, req.Rules); err != nil {
		return storeError(err, "organizasyon bulunamadı", fail, "set status rules")
	}

	var targetID *string
	if req.OrganizationID != nil {
		id := req.OrganizationID.String()
		targetID = &id
	}
	d.logAudit(r, "status_rule.update", "organization", targetID, map[string]any{"rules": req.Rules})

	orgIDs := []uuid.UUID{}
	if req.OrganizationID != nil {
		orgIDs = append(orgIDs, *req.OrganizationID)
	}
	rules, err := d.thresholds.ListStatusRules(r.Context(), orgIDs)
	if err != nil {
		return serverErr("kurallar kaydedildi ancak geri okunamadı", "set status rules: reload", err)
	}
	// Yalnızca istenen kapsamın satırları döner.
	scoped := []model.StatusRuleConfig{}
	for _, rule := range rules {
		if (rule.OrganizationID == nil) == (req.OrganizationID == nil) {
			scoped = append(scoped, rule)
		}
	}
	writeJSON(w, http.StatusOK, scoped)
	return nil
}

// hostStatusRuleView, bir sunucu için bir kuralın durumudur: kendi ayarı yoksa alacağı (organizasyon zinciri ya da
// genel; nil = kapalı), varsa kendi ayarı. TakesDuration, kurala süre verilip verilemeyeceğidir.
type hostStatusRuleView struct {
	Rule          string                   `json:"rule"`
	TakesDuration bool                     `json:"takes_duration"`
	Default       *model.StatusRuleSetting `json:"default"`
	Custom        *model.StatusRuleSetting `json:"custom"`
}

func (d *Deps) hostStatusRules(r *http.Request, host model.Host) ([]hostStatusRuleView, error) {
	defaults, err := d.thresholds.StatusRuleDefaultsFor(r.Context(), host.OrganizationID)
	if err != nil {
		return nil, err
	}
	custom, err := d.thresholds.HostStatusRules(r.Context(), host.ID)
	if err != nil {
		return nil, err
	}
	out := make([]hostStatusRuleView, 0, len(model.StatusRules))
	for _, rule := range model.StatusRules {
		view := hostStatusRuleView{Rule: rule, TakesDuration: model.RuleTakesDuration(rule)}
		if st, ok := defaults[rule]; ok {
			view.Default = &st
		}
		if st, ok := custom[rule]; ok {
			view.Custom = &st
		}
		out = append(out, view)
	}
	return out, nil
}

// handleGetHostStatusRules, GET /api/v1/hosts/:id/status-rules'u sunar: her kural için varsayılan ve sunucunun kendi
// ayarı (model.StatusRules sırasıyla).
func (d *Deps) handleGetHostStatusRules(w http.ResponseWriter, r *http.Request) error {
	fail := failWith("sunucunun durum kuralları alınamadı")
	host, err := d.viewableHost(r, "get host status rules", fail)
	if err != nil {
		return err
	}
	views, err := d.hostStatusRules(r, host)
	if err != nil {
		return fail("get host status rules", err)
	}
	writeJSON(w, http.StatusOK, views)
	return nil
}

type setHostStatusRulesRequest struct {
	Rules model.StatusRuleChanges `json:"rules"`
}

func (req *setHostStatusRulesRequest) Validate() error {
	if req.Rules == nil {
		return errors.New(`"rules" zorunlu: kural -> {level, duration_seconds} ya da null (varsayılanı izle)`)
	}
	return req.Rules.Validate()
}

// handleSetHostStatusRules, PUT /api/v1/hosts/:id/status-rules'u sunar (gövde /status-rules'taki "rules" ile aynı).
func (d *Deps) handleSetHostStatusRules(w http.ResponseWriter, r *http.Request) error {
	fail := failWith("sunucunun durum kuralları güncellenemedi")
	host, err := d.managedHost(r, "set host status rules", fail)
	if err != nil {
		return err
	}
	req, err := bind[setHostStatusRulesRequest](r)
	if err != nil {
		return err
	}
	if err := d.thresholds.SetHostStatusRules(r.Context(), host.ID, req.Rules); err != nil {
		return storeError(err, "sunucu bulunamadı", fail, "set host status rules")
	}

	targetID := host.ID.String()
	d.logAudit(r, "host.update_status_rules", "host", &targetID, map[string]any{"rules": req.Rules})

	views, err := d.hostStatusRules(r, host)
	if err != nil {
		return serverErr("kurallar kaydedildi ancak geri okunamadı", "set host status rules: reload", err)
	}
	writeJSON(w, http.StatusOK, views)
	return nil
}
