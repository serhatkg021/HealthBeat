package httpapi

import (
	"errors"
	"net/http"
	"net/mail"
	"strings"

	"github.com/google/uuid"

	"healthbeat-server/internal/model"
	"healthbeat-server/internal/store"
)

// İletişim kişileri: bir organizasyonun sunucu işleri için başvurulacak kişiler (paneli olmayan müşteri yetkilileri dahil).
// Görme contact.view, düzenleme contact.edit izni ve organizasyona TAM erişim ister.

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
	c, err := d.contacts.Create(r.Context(), orgID, req.input)
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

// managedContact, yoldaki iletişim kişisini yükler ve organizasyonuna erişimi denetler.
func (d *Deps) managedContact(r *http.Request, op string, fail failFunc) (model.OrganizationContact, error) {
	id, err := pathID(r, "geçersiz kimlik")
	if err != nil {
		return model.OrganizationContact{}, err
	}
	c, err := d.contacts.GetByID(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		return model.OrganizationContact{}, notFound("iletişim kişisi bulunamadı")
	}
	if err != nil {
		return model.OrganizationContact{}, fail(op+": lookup contact", err)
	}
	allowed, err := d.scope(r).CanManageOrg(r.Context(), c.OrganizationID)
	if err != nil {
		return model.OrganizationContact{}, fail(op+": check organization access", err)
	}
	if !allowed {
		return model.OrganizationContact{}, forbidden()
	}
	return c, nil
}
