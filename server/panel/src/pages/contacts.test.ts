import assert from 'node:assert/strict'
import { test } from 'node:test'
import type { OrganizationContact } from '../types/api.ts'
import { draftFromContact, emptyContactDraft, managerChoices, managerName, toContactInput, validateContact } from './contacts.ts'

const contact = (over: Partial<OrganizationContact>): OrganizationContact => ({ id: 'c', organization_id: 'o', name: 'Ayşe', created_at: '', updated_at: '', ...over })

test('a contact needs a name and at least a phone or an email', () => {
  assert.deepEqual(validateContact(emptyContactDraft()), ['Ad zorunlu.', 'Telefon ya da e-postadan en az biri zorunlu.'])
  assert.deepEqual(validateContact({ ...emptyContactDraft(), name: 'Ali', phone: '+90 (555) 111-22-33' }), [])
  assert.deepEqual(validateContact({ ...emptyContactDraft(), name: 'Ali', email: 'ali@x.test' }), [])
  assert.equal(validateContact({ ...emptyContactDraft(), name: 'Ali', email: 'Ali <a@x.test>' }).length, 1)
  assert.equal(validateContact({ ...emptyContactDraft(), name: 'Ali', phone: 'call me' }).length, 1)
})

test('the request body trims text and sends null for no manager', () => {
  assert.deepEqual(toContactInput({ name: ' Ali ', department: ' BT ', title: '', managerId: '', phone: '', email: ' a@x.test ' }), {
    name: 'Ali',
    department: 'BT',
    title: '',
    manager_contact_id: null,
    phone: '',
    email: 'a@x.test',
  })
  assert.equal(toContactInput({ ...emptyContactDraft(), name: 'x', managerId: 'm1' }).manager_contact_id, 'm1')
})

test('editing starts from the stored values and a contact cannot be its own manager', () => {
  const list = [contact({ id: 'a', name: 'Amir' }), contact({ id: 'b', name: 'Başak', manager_contact_id: 'a', email: 'b@x.test' })]
  assert.equal(draftFromContact(list[1]).managerId, 'a')
  assert.equal(draftFromContact(list[0]).managerId, '')
  assert.deepEqual(managerChoices(list, 'b').map((c) => c.id), ['a'])
  assert.equal(managerName(list[1], list), 'Amir')
  assert.equal(managerName(list[0], list), '')
})
