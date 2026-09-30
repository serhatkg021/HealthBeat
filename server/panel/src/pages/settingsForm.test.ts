import { test } from 'node:test'
import assert from 'node:assert/strict'
import { SETTINGS_SECTIONS, buildPatch, channelStatus, defaultText, fieldMessage, fromDisplay, notificationGap, toDisplay, type FieldDef } from './settingsForm.ts'
import type { SettingsValues } from '../types/api.ts'

const def = (field: string): FieldDef => {
  for (const s of SETTINGS_SECTIONS) {
    const d = s.fields.find((f) => f.field === field)
    if (d) return d
  }
  throw new Error(field)
}

const values: SettingsValues = {
  latest_agent_version: '1.0.0',
  min_supported_agent_version: '',
  metrics_retention_days: 30,
  audit_retention_days: 0,
  resolved_alert_retention_days: 0,
  access_token_ttl_seconds: 900,
  refresh_token_ttl_seconds: 604800,
  rate_limit_auth_failures_per_minute: 10,
  rate_limit_ingest_per_minute: 120,
  panel_base_url: '',
  log_level: 'info',
  log_error_body_bytes: 4096,
  log_file_max_age_days: 14,
  log_file_max_total_mb: 1024,
}

test('every setting appears in exactly one section', () => {
  const fields = SETTINGS_SECTIONS.flatMap((s) => s.fields.map((f) => f.field)).sort()
  assert.deepEqual(fields, Object.keys(values).sort())
})

test('durations are shown in minutes and hours and sent back in seconds', () => {
  assert.equal(toDisplay(def('access_token_ttl_seconds'), 900), '15')
  assert.equal(toDisplay(def('refresh_token_ttl_seconds'), 604800), '168')
  assert.equal(toDisplay(def('refresh_token_ttl_seconds'), 5400), '1.5')
  assert.deepEqual(fromDisplay(def('access_token_ttl_seconds'), '30'), { value: 1800 })
  assert.deepEqual(fromDisplay(def('refresh_token_ttl_seconds'), '1,5'), { value: 5400 })
  assert.deepEqual(fromDisplay(def('metrics_retention_days'), 'otuz'), { error: 'sayı olmalı' })
  assert.deepEqual(fromDisplay(def('metrics_retention_days'), '-1'), { error: 'sayı olmalı' })
  assert.deepEqual(fromDisplay(def('latest_agent_version'), ' 1.2.0 '), { value: '1.2.0' })
})

test('buildPatch sends only the fields that changed', () => {
  const session = SETTINGS_SECTIONS.find((s) => s.id === 'oturum')!
  const { patch, errors } = buildPatch(session, values, {
    access_token_ttl_seconds: '15', // aynı (900 sn)
    refresh_token_ttl_seconds: '24',
    rate_limit_ingest_per_minute: '0',
  })
  assert.deepEqual(patch, { refresh_token_ttl_seconds: 86400, rate_limit_ingest_per_minute: 0 })
  assert.deepEqual(errors, {})

  const bad = buildPatch(session, values, { access_token_ttl_seconds: 'abc' })
  assert.deepEqual(bad.errors, { access_token_ttl_seconds: 'sayı olmalı' })
})

test('defaultText names the unit and an empty default', () => {
  assert.equal(defaultText(def('access_token_ttl_seconds'), 900), 'varsayılan: 15 dakika')
  assert.equal(defaultText(def('min_supported_agent_version'), ''), 'varsayılan: boş')
  assert.equal(defaultText(def('log_level'), 'info'), 'varsayılan: info')
})

test('channelStatus', () => {
  assert.deepEqual(channelStatus({ enabled: false, ready: false }), { label: 'Ayar gerekli', tone: 'warning' })
  assert.deepEqual(channelStatus({ enabled: false, ready: true }), { label: 'Kapalı', tone: 'neutral' })
  assert.deepEqual(channelStatus({ enabled: true, ready: true }), { label: 'Açık', tone: 'good' })
})

test('notificationGap: nobody is notified without an open e-mail channel and an owner who takes e-mail', () => {
  const on = [{ channel: 'email' as const, enabled: true }]
  const owner = { email: 'noc@x.test', email_enabled: true }
  assert.match(notificationGap([{ channel: 'email', enabled: false }], [owner]) ?? '', /kanalı kapalı/)
  assert.match(notificationGap(on, []) ?? '', /sistem sahibi yok/)
  assert.match(notificationGap(on, [{ email: 'noc@x.test', email_enabled: false }, { email: null, email_enabled: true }]) ?? '', /sistem sahibi yok/)
  assert.equal(notificationGap(on, [owner]), null)
})

test('server range errors are shown in the unit of the form field', () => {
  assert.equal(fieldMessage(def('access_token_ttl_seconds'), '60 ile 86400 arasında olmalı'), '1 ile 1440 dakika arasında olmalı')
  assert.equal(fieldMessage(def('refresh_token_ttl_seconds'), '3600 ile 7776000 arasında olmalı'), '1 ile 2160 saat arasında olmalı')
  assert.equal(fieldMessage(def('metrics_retention_days'), 'en az 0 olmalı'), 'en az 0 olmalı')
  assert.equal(fieldMessage(def('refresh_token_ttl_seconds'), 'oturum yenileme süresi erişim süresinden uzun olmalı'), 'oturum yenileme süresi erişim süresinden uzun olmalı')
  assert.equal(fieldMessage(undefined, 'x'), 'x')
})
