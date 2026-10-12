package httpapi

import (
	"errors"
	"net/http"
	"net/mail"
	"strings"
	"time"

	"github.com/google/uuid"

	"healthbeat-server/internal/model"
	"healthbeat-server/internal/store"
)

// Kişiler. Organizasyon kişileri bir organizasyonun sunucu işleri için başvurulacak kişilerdir (paneli olmayan müşteri
// yetkilileri dahil): görme contact.view, düzenleme contact.edit izni ve organizasyona TAM erişim ister. Sistem kişileri
// organizasyonsuzdur ve bütün kurulumun bildirimlerini alabilir (sistem kuralları): görme settings.view, düzenleme
// settings.manage ister (varsayılan olarak yalnızca süper admin). İkisi aynı tablodadır ama uçları ayrıdır; organizasyon
// kişisi uçları sistem kişisini görmez.

const maxContactTextLen = 200

type contactRequest struct {
	Department       *string    `json:"department"`
	Title            *string    `json:"title"`
	Name             string     `json:"name"`
	ManagerContactID *uuid.UUID `json:"manager_contact_id"`
	Phone            *string    `json:"phone"`
	Email            *string    `json:"email"`

	input store.ContactInput // Validate'in ürettiği kırpılmış girdi
}

// Validate, isteği doğrular ve kırpılmış bir store.ContactInput'a çevirir (req.input).
func (req *contactRequest) Validate() error {
	name := strings.TrimSpace(req.Name)
	if name == "" {
		return errors.New("ad zorunlu")
	}
	for _, v := range []*string{req.Department, req.Title} {
		if v != nil && len(strings.TrimSpace(*v)) > maxContactTextLen {
			return errors.New("departman ve unvan en fazla 200 karakter olabilir")
		}
	}
	if len(name) > maxContactTextLen {
		return errors.New("ad çok uzun")
	}
	in := store.ContactInput{
		Department: trimPtr(req.Department), Title: trimPtr(req.Title), Name: name,
		ManagerContactID: req.ManagerContactID, Phone: trimPtr(req.Phone), Email: trimPtr(req.Email),
	}
	if in.Phone != nil {
		if err := validatePhone(*in.Phone); err != nil {
			return err
		}
	}
	if in.Email != nil && *in.Email != "" {
		addr, err := mail.ParseAddress(*in.Email)
		if err != nil || addr.Address != *in.Email || strings.ContainsAny(*in.Email, " \t\r\n<>,;") {
			return errors.New("e-posta ad@ornek.com gibi sade bir adres olmalı")
		}
	}
	empty := func(p *string) bool { return p == nil || *p == "" }
	if empty(in.Phone) && empty(in.Email) {
		return errors.New("telefon ya da e-postadan en az biri zorunlu")
	}
	req.input = in
	return nil
}

func (d *Deps) handleListContacts(w http.ResponseWriter, r *http.Request) error {
	fail := failWith("iletişim kişileri alınamadı")
	orgID, err := d.managedOrg(r, "list contacts", fail)
	if err != nil {
		return err
	}
	contacts, err := d.contacts.ListByOrganization(r.Context(), orgID)
	if err != nil {
		return fail("list contacts", err)
	}
	writeJSON(w, http.StatusOK, contacts)
	return nil
}

func (d *Deps) handleCreateContact(w http.ResponseWriter, r *http.Request) error {
	fail := failWith("iletişim kişisi oluşturulamadı")
	orgID, err := d.managedOrg(r, "create contact", fail)
	if err != nil {
		return err
	}
	req, err := bind[contactRequest](r)
	if err != nil {
		return err
	}
	c, err := d.contacts.Create(r.Context(), &orgID, req.input)
	if err != nil {
		return storeError(err, "kayıt bulunamadı", fail, "create contact")
	}
	targetID := c.ID.String()
	d.logAudit(r, "contact.create", "contact", &targetID, map[string]any{"organization_id": orgID, "name": c.Name})
	writeJSON(w, http.StatusCreated, c)
	return nil
}

func (d *Deps) handleUpdateContact(w http.ResponseWriter, r *http.Request) error {
	fail := failWith("iletişim kişisi güncellenemedi")
	existing, err := d.managedContact(r, "update contact", fail)
	if err != nil {
		return err
	}
	req, err := bind[contactRequest](r)
	if err != nil {
		return err
	}
	c, err := d.contacts.Update(r.Context(), existing.ID, req.input)
	if err != nil {
		return storeError(err, "kayıt bulunamadı", fail, "update contact")
	}
	targetID := c.ID.String()
	d.logAudit(r, "contact.update", "contact", &targetID, map[string]any{"organization_id": c.OrganizationID, "name": c.Name})
	writeJSON(w, http.StatusOK, c)
	return nil
}

func (d *Deps) handleDeleteContact(w http.ResponseWriter, r *http.Request) error {
	fail := failWith("iletişim kişisi silinemedi")
	existing, err := d.managedContact(r, "delete contact", fail)
	if err != nil {
		return err
	}
	err = d.contacts.Delete(r.Context(), existing.ID)
	if errors.Is(err, store.ErrNotFound) {
		return notFound("iletişim kişisi bulunamadı")
	}
	if err != nil {
		return fail("delete contact", err)
	}
	targetID := existing.ID.String()
	d.logAudit(r, "contact.delete", "contact", &targetID, map[string]any{"organization_id": existing.OrganizationID, "name": existing.Name})
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// managedOrg, yoldaki organizasyonu doğrular: geçerli kimlik, var olan organizasyon ve TAM erişim.
func (d *Deps) managedOrg(r *http.Request, op string, fail failFunc) (uuid.UUID, error) {
	orgID, err := pathID(r, "geçersiz organizasyon kimliği")
	if err != nil {
		return uuid.Nil, err
	}
	_, err = d.organizations.GetByID(r.Context(), orgID)
	if errors.Is(err, store.ErrNotFound) {
		return uuid.Nil, notFound("organizasyon bulunamadı")
	}
	if err != nil {
		return uuid.Nil, fail(op+": lookup organization", err)
	}
	allowed, err := d.scope(r).CanManageOrg(r.Context(), orgID)
	if err != nil {
		return uuid.Nil, fail(op+": check organization access", err)
	}
	if !allowed {
		return uuid.Nil, forbidden()
	}
	return orgID, nil
}

// managedContact, yoldaki organizasyon kişisini yükler ve organizasyonuna erişimi denetler. Sistem kişisi burada yoktur
// (404): onu yalnızca sistem kişisi uçları değiştirir.
func (d *Deps) managedContact(r *http.Request, op string, fail failFunc) (model.Contact, error) {
	id, err := pathID(r, "geçersiz kimlik")
	if err != nil {
		return model.Contact{}, err
	}
	c, err := d.contacts.GetByID(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) || (err == nil && c.OrganizationID == nil) {
		return model.Contact{}, notFound("iletişim kişisi bulunamadı")
	}
	if err != nil {
		return model.Contact{}, fail(op+": lookup contact", err)
	}
	allowed, err := d.scope(r).CanManageOrg(r.Context(), *c.OrganizationID)
	if err != nil {
		return model.Contact{}, fail(op+": check organization access", err)
	}
	if !allowed {
		return model.Contact{}, forbidden()
	}
	return c, nil
}

// ---------------------------------------------------------------- sistem kişileri

// systemContactResponse, bir sistem kişisi ve alıcı olduğu bildirim kuralı sayısıdır (silinince kurallar da silinir).
type systemContactResponse struct {
	ID         uuid.UUID `json:"id"`
	Name       string    `json:"name"`
	Department *string   `json:"department,omitempty"`
	Title      *string   `json:"title,omitempty"`
	Phone      *string   `json:"phone,omitempty"`
	Email      *string   `json:"email,omitempty"`
	Scope      string    `json:"scope"`
	RuleCount  int       `json:"rule_count"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

func systemContactView(c model.Contact, ruleCount int) systemContactResponse {
	return systemContactResponse{ID: c.ID, Name: c.Name, Department: c.Department, Title: c.Title, Phone: c.Phone, Email: c.Email,
		Scope: c.Scope(), RuleCount: ruleCount, CreatedAt: c.CreatedAt, UpdatedAt: c.UpdatedAt}
}

func contactAudit(c model.Contact) map[string]any {
	return map[string]any{"scope": c.Scope(), "organization_id": c.OrganizationID, "name": c.Name, "email": c.Email, "phone": c.Phone}
}

// bindSystemContact, sistem kişisi isteğini doğrular: kişi kuralları geçerlidir, yönetici olmaz (yönetici bağı
// organizasyon içindir).
func bindSystemContact(r *http.Request) (store.ContactInput, error) {
	req, err := bind[contactRequest](r)
	if err != nil {
		return store.ContactInput{}, err
	}
	if req.input.ManagerContactID != nil {
		return store.ContactInput{}, fieldError400("manager_contact_id", "sistem kişisinin yöneticisi olmaz")
	}
	return req.input, nil
}

func (d *Deps) handleListSystemContacts(w http.ResponseWriter, r *http.Request) error {
	fail := failWith("sistem kişileri alınamadı")
	list, err := d.contacts.ListSystem(r.Context())
	if err != nil {
		return fail("list system contacts", err)
	}
	counts, err := d.contacts.SystemRuleCounts(r.Context())
	if err != nil {
		return fail("list system contacts: rule counts", err)
	}
	out := make([]systemContactResponse, 0, len(list))
	for _, c := range list {
		out = append(out, systemContactView(c, counts[c.ID]))
	}
	writeJSON(w, http.StatusOK, out)
	return nil
}

func (d *Deps) handleCreateSystemContact(w http.ResponseWriter, r *http.Request) error {
	fail := failWith("sistem kişisi eklenemedi")
	in, err := bindSystemContact(r)
	if err != nil {
		return err
	}
	c, err := d.contacts.Create(r.Context(), nil, in)
	if err != nil {
		return storeError(err, "kayıt bulunamadı", fail, "create system contact")
	}
	targetID := c.ID.String()
	d.logAudit(r, "contact.create", "contact", &targetID, contactAudit(c))
	writeJSON(w, http.StatusCreated, systemContactView(c, 0))
	return nil
}

// systemContactFromPath, yoldaki sistem kişisini yükler; organizasyon kişisi burada yoktur (404).
func (d *Deps) systemContactFromPath(r *http.Request, op string, fail failFunc) (model.Contact, error) {
	id, err := pathID(r, "geçersiz kimlik")
	if err != nil {
		return model.Contact{}, err
	}
	c, err := d.contacts.GetByID(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) || (err == nil && c.OrganizationID != nil) {
		return model.Contact{}, notFound("sistem kişisi bulunamadı")
	}
	if err != nil {
		return model.Contact{}, fail(op+": lookup contact", err)
	}
	return c, nil
}

func (d *Deps) handleUpdateSystemContact(w http.ResponseWriter, r *http.Request) error {
	fail := failWith("sistem kişisi güncellenemedi")
	existing, err := d.systemContactFromPath(r, "update system contact", fail)
	if err != nil {
		return err
	}
	in, err := bindSystemContact(r)
	if err != nil {
		return err
	}
	c, err := d.contacts.Update(r.Context(), existing.ID, in)
	if err != nil {
		return storeError(err, "sistem kişisi bulunamadı", fail, "update system contact")
	}
	counts, err := d.contacts.SystemRuleCounts(r.Context())
	if err != nil {
		return fail("update system contact: rule counts", err)
	}
	targetID := c.ID.String()
	d.logAudit(r, "contact.update", "contact", &targetID, map[string]any{"old": contactAudit(existing), "new": contactAudit(c)})
	writeJSON(w, http.StatusOK, systemContactView(c, counts[c.ID]))
	return nil
}

func (d *Deps) handleDeleteSystemContact(w http.ResponseWriter, r *http.Request) error {
	fail := failWith("sistem kişisi silinemedi")
	existing, err := d.systemContactFromPath(r, "delete system contact", fail)
	if err != nil {
		return err
	}
	if err := d.contacts.Delete(r.Context(), existing.ID); err != nil {
		return storeError(err, "sistem kişisi bulunamadı", fail, "delete system contact")
	}
	targetID := existing.ID.String()
	d.logAudit(r, "contact.delete", "contact", &targetID, contactAudit(existing))
	w.WriteHeader(http.StatusNoContent)
	return nil
}
