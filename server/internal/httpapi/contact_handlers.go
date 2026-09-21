package httpapi

import (
	"errors"
	"log"
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
}

// toInput, isteği doğrular ve kırpılmış bir store.ContactInput'a çevirir.
func (req contactRequest) toInput() (store.ContactInput, string) {
	name := strings.TrimSpace(req.Name)
	if name == "" {
		return store.ContactInput{}, "ad zorunlu"
	}
	for _, v := range []*string{req.Department, req.Title} {
		if v != nil && len(strings.TrimSpace(*v)) > maxContactTextLen {
			return store.ContactInput{}, "departman ve unvan en fazla 200 karakter olabilir"
		}
	}
	if len(name) > maxContactTextLen {
		return store.ContactInput{}, "ad çok uzun"
	}
	in := store.ContactInput{
		Department: trimPtr(req.Department), Title: trimPtr(req.Title), Name: name,
		ManagerContactID: req.ManagerContactID, Phone: trimPtr(req.Phone), Email: trimPtr(req.Email),
	}
	if in.Phone != nil {
		if err := validatePhone(*in.Phone); err != nil {
			return store.ContactInput{}, err.Error()
		}
	}
	if in.Email != nil && *in.Email != "" {
		addr, err := mail.ParseAddress(*in.Email)
		if err != nil || addr.Address != *in.Email || strings.ContainsAny(*in.Email, " \t\r\n<>,;") {
			return store.ContactInput{}, "e-posta ad@ornek.com gibi sade bir adres olmalı"
		}
	}
	empty := func(p *string) bool { return p == nil || *p == "" }
	if empty(in.Phone) && empty(in.Email) {
		return store.ContactInput{}, "telefon ya da e-postadan en az biri zorunlu"
	}
	return in, ""
}

func (d *Deps) handleListContacts(w http.ResponseWriter, r *http.Request) {
	orgID, ok := d.orgFromPath(w, r, "iletişim kişileri alınamadı")
	if !ok {
		return
	}
	contacts, err := d.contacts.ListByOrganization(r.Context(), orgID)
	if err != nil {
		log.Printf("list contacts: %v", err)
		writeError(w, http.StatusInternalServerError, "iletişim kişileri alınamadı")
		return
	}
	writeJSON(w, http.StatusOK, contacts)
}

func (d *Deps) handleCreateContact(w http.ResponseWriter, r *http.Request) {
	orgID, ok := d.orgFromPath(w, r, "iletişim kişisi oluşturulamadı")
	if !ok {
		return
	}
	var req contactRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "geçersiz istek gövdesi")
		return
	}
	in, msg := req.toInput()
	if msg != "" {
		writeError(w, http.StatusBadRequest, msg)
		return
	}
	c, err := d.contacts.Create(r.Context(), orgID, in)
	if err != nil {
		d.writeContactError(w, err, "iletişim kişisi oluşturulamadı")
		return
	}
	targetID := c.ID.String()
	d.logAudit(r, "contact.create", "contact", &targetID, map[string]any{"organization_id": orgID, "name": c.Name})
	writeJSON(w, http.StatusCreated, c)
}

func (d *Deps) handleUpdateContact(w http.ResponseWriter, r *http.Request) {
	existing, ok := d.contactFromPath(w, r, "iletişim kişisi güncellenemedi")
	if !ok {
		return
	}
	var req contactRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "geçersiz istek gövdesi")
		return
	}
	in, msg := req.toInput()
	if msg != "" {
		writeError(w, http.StatusBadRequest, msg)
		return
	}
	c, err := d.contacts.Update(r.Context(), existing.ID, in)
	if err != nil {
		d.writeContactError(w, err, "iletişim kişisi güncellenemedi")
		return
	}
	targetID := c.ID.String()
	d.logAudit(r, "contact.update", "contact", &targetID, map[string]any{"organization_id": c.OrganizationID, "name": c.Name})
	writeJSON(w, http.StatusOK, c)
}

func (d *Deps) handleDeleteContact(w http.ResponseWriter, r *http.Request) {
	existing, ok := d.contactFromPath(w, r, "iletişim kişisi silinemedi")
	if !ok {
		return
	}
	if err := d.contacts.Delete(r.Context(), existing.ID); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "iletişim kişisi bulunamadı")
			return
		}
		log.Printf("delete contact: %v", err)
		writeError(w, http.StatusInternalServerError, "iletişim kişisi silinemedi")
		return
	}
	targetID := existing.ID.String()
	d.logAudit(r, "contact.delete", "contact", &targetID, map[string]any{"organization_id": existing.OrganizationID, "name": existing.Name})
	w.WriteHeader(http.StatusNoContent)
}

func (d *Deps) writeContactError(w http.ResponseWriter, err error, failure string) {
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, "kayıt bulunamadı")
	case errors.Is(err, store.ErrConflict):
		writeError(w, http.StatusBadRequest, err.Error())
	default:
		log.Printf("%s: %v", failure, err)
		writeError(w, http.StatusInternalServerError, failure)
	}
}

// orgFromPath, yoldaki organizasyonu doğrular: geçerli kimlik, var olan organizasyon ve TAM erişim. Değilse yanıtı kendisi yazar.
func (d *Deps) orgFromPath(w http.ResponseWriter, r *http.Request, failure string) (uuid.UUID, bool) {
	orgID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "geçersiz organizasyon kimliği")
		return uuid.Nil, false
	}
	if _, err := d.organizations.GetByID(r.Context(), orgID); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "organizasyon bulunamadı")
			return uuid.Nil, false
		}
		log.Printf("%s: %v", failure, err)
		writeError(w, http.StatusInternalServerError, failure)
		return uuid.Nil, false
	}
	allowed, err := d.requireOrgAccess(r, orgID)
	if err != nil {
		log.Printf("%s: check org access: %v", failure, err)
		writeError(w, http.StatusInternalServerError, failure)
		return uuid.Nil, false
	}
	if !allowed {
		writeError(w, http.StatusForbidden, "yetkiniz yok")
		return uuid.Nil, false
	}
	return orgID, true
}

// contactFromPath, yoldaki iletişim kişisini yükler ve organizasyonuna erişimi denetler.
func (d *Deps) contactFromPath(w http.ResponseWriter, r *http.Request, failure string) (model.OrganizationContact, bool) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "geçersiz kimlik")
		return model.OrganizationContact{}, false
	}
	c, err := d.contacts.GetByID(r.Context(), id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "iletişim kişisi bulunamadı")
			return model.OrganizationContact{}, false
		}
		log.Printf("%s: %v", failure, err)
		writeError(w, http.StatusInternalServerError, failure)
		return model.OrganizationContact{}, false
	}
	allowed, err := d.requireOrgAccess(r, c.OrganizationID)
	if err != nil {
		log.Printf("%s: check org access: %v", failure, err)
		writeError(w, http.StatusInternalServerError, failure)
		return model.OrganizationContact{}, false
	}
	if !allowed {
		writeError(w, http.StatusForbidden, "yetkiniz yok")
		return model.OrganizationContact{}, false
	}
	return c, true
}
