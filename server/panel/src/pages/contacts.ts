// İletişim kişisi formu için saf mantık (React yok; Node'un çalıştırıcısıyla birim test edilir).

import type { ContactInput, OrganizationContact } from '../types/api.ts'

export interface ContactDraft {
  name: string
  department: string
  title: string
  managerId: string // '' = yok
  phone: string
  email: string
}

export const emptyContactDraft = (): ContactDraft => ({ name: '', department: '', title: '', managerId: '', phone: '', email: '' })

export const draftFromContact = (c: OrganizationContact): ContactDraft => ({
  name: c.name,
  department: c.department ?? '',
  title: c.title ?? '',
  managerId: c.manager_contact_id ?? '',
  phone: c.phone ?? '',
  email: c.email ?? '',
})

// Server'ın kurallarını yansıtır (yetkili olan server'dır): ad zorunlu, telefon ya da e-postadan en az biri şart.
export function validateContact(d: ContactDraft): string[] {
  const errors: string[] = []
  if (d.name.trim() === '') errors.push('Ad zorunlu.')
  if (d.phone.trim() === '' && d.email.trim() === '') errors.push('Telefon ya da e-postadan en az biri zorunlu.')
  if (d.email.trim() !== '' && !/^[^\s@<>,;]+@[^\s@<>,;]+$/.test(d.email.trim())) errors.push('E-posta ad@ornek.com gibi sade bir adres olmalı.')
  if (d.phone.trim() !== '' && !/^[0-9 +\-().]+$/.test(d.phone.trim())) errors.push('Telefon yalnızca rakam, boşluk ve + - ( ) . içerebilir.')
  return errors
}

// PUT/POST gövdesi: boş metin "yok" demektir (server alanı temizler).
export function toContactInput(d: ContactDraft): ContactInput {
  return {
    name: d.name.trim(),
    department: d.department.trim(),
    title: d.title.trim(),
    manager_contact_id: d.managerId === '' ? null : d.managerId,
    phone: d.phone.trim(),
    email: d.email.trim(),
  }
}

// Yönetici olarak seçilebilecekler: düzenlenen kişinin kendisi hariç (kendi yöneticisi olamaz).
export const managerChoices = (contacts: OrganizationContact[], editingId?: string): OrganizationContact[] =>
  contacts.filter((c) => c.id !== editingId)

// Kişinin yöneticisinin adı, listede göstermek için.
export function managerName(c: OrganizationContact, contacts: OrganizationContact[]): string {
  return contacts.find((x) => x.id === c.manager_contact_id)?.name ?? ''
}
