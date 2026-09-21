// "Sunucu ekle" sihirbazı için saf mantık: her adımın geçerli olması için ne gerektiği, ürettiği
// istek ve kaydetmeden önce gösterilen özet. React yok; Node'un çalıştırıcısıyla birim test edilir.

import type { CreateHostInput } from '../api/endpoints.ts'
import type { HostMode } from '../types/api.ts'
import { MISSING_AFTER, parseMountInput } from './diskSelection.ts'
import {
  customMounts,
  customOnly,
  defaultDrafts,
  describeContainers,
  describeDraft,
  describeMounts,
  METRICS,
  validateContainerDrafts,
  validateDrafts,
  validateMountDrafts,
  type Defaults,
  type Drafts,
  type MountDrafts,
} from './thresholds.ts'

export type StepId = 'server' | 'disks' | 'thresholds' | 'summary'

export const STEPS: { id: StepId; title: string }[] = [
  { id: 'server', title: 'Sunucu' },
  { id: 'disks', title: 'Diskler' },
  { id: 'thresholds', title: 'Eşikler' },
  { id: 'summary', title: 'Özet ve kaydet' },
]

export type DiskMode = 'all' | 'selected'

export interface WizardDraft {
  title: string
  ip: string
  mode: HostMode
  intervalSeconds: string
  pullPort: string
  pullEndpoint: string
  diskMode: DiskMode
  diskMounts: string // virgül/satır sonu ile ayrılmış yollar; yalnızca diskMode 'selected' iken kullanılır
  thresholds: Drafts
  mountThresholds: MountDrafts // kendi disk eşiği olan mount'lar
  containerThresholds: MountDrafts // container'ların kendi docker_restart eşiği
}

export function emptyWizardDraft(): WizardDraft {
  return {
    title: '',
    ip: '',
    mode: 'push',
    intervalSeconds: '30',
    pullPort: '9100',
    pullEndpoint: '/status',
    diskMode: 'all',
    diskMounts: '',
    thresholds: defaultDrafts(),
    mountThresholds: {},
    containerThresholds: {},
  }
}

const IPV4 = /^(25[0-5]|2[0-4]\d|1\d\d|[1-9]?\d)(\.(25[0-5]|2[0-4]\d|1\d\d|[1-9]?\d)){3}$/

// Bilerek gevşek (server'ın net.ParseIP'si yetkilidir): noktalı IPv4'ü ve IPv6'ya benzeyen her
// şeyi (hex grupları ve iki nokta) kabul eder, host adlarını reddeder.
export function looksLikeIP(ip: string): boolean {
  const v = ip.trim()
  if (IPV4.test(v)) return true
  return v.includes(':') && /^[0-9a-fA-F:.]+$/.test(v) && v.split(':').length >= 3
}

export function validateStep(step: StepId, d: WizardDraft): string[] {
  const errors: string[] = []
  switch (step) {
    case 'server': {
      if (d.title.trim() === '') errors.push('Sunucu adı zorunlu.')
      if (d.ip.trim() === '') errors.push('IP zorunlu.')
      else if (!looksLikeIP(d.ip)) errors.push('IP geçerli bir IPv4 ya da IPv6 adresi olmalı.')
      const interval = Number(d.intervalSeconds)
      if (!Number.isInteger(interval) || interval < 1) errors.push('Aralık en az 1 saniye olan bir tam sayı olmalı.')
      if (d.mode === 'pull') {
        const port = Number(d.pullPort)
        if (!Number.isInteger(port) || port < 1 || port > 65535) errors.push('Pull port 1 ile 65535 arasında olmalı.')
        if (d.pullEndpoint.trim() === '') errors.push('Pull endpoint zorunlu.')
      }
      break
    }
    case 'disks': {
      if (d.diskMode === 'selected') {
        const { invalid } = parseMountInput(d.diskMounts)
        if (invalid.length > 0) errors.push(`Geçersiz disk yolu: ${invalid.join(', ')} (mutlak yol olmalı, "/" ile başlamalı).`)
      }
      break
    }
    case 'thresholds':
      errors.push(...validateDrafts(d.thresholds), ...validateMountDrafts(d.mountThresholds), ...validateContainerDrafts(d.containerThresholds))
      break
    case 'summary':
      break
  }
  return errors
}

// Adım başına sorunlar, özet için: kişiyi hangi adıma geri göndereceğimiz.
export function validateAll(d: WizardDraft): Partial<Record<StepId, string[]>> {
  const out: Partial<Record<StepId, string[]>> = {}
  for (const s of STEPS) {
    const errors = validateStep(s.id, d)
    if (errors.length > 0) out[s.id] = errors
  }
  return out
}

export function selectedMounts(d: WizardDraft): string[] {
  return d.diskMode === 'selected' ? parseMountInput(d.diskMounts).mounts : []
}

// POST /hosts gövdesi. "Tüm diskler" disk alanlarını atlar (server'da all_mounts_alert=true); "selected"
// all_mounts_alert=false ile listeyi boş olsa bile gönderir, bu hiç disk alert'i olmaması demektir. Yalnızca özel değeri
// olan metrikler gönderilir: geri kalan her şey varsayılanı izler.
export function buildCreateRequest(organizationId: string, d: WizardDraft): CreateHostInput {
  const custom = customOnly(d.thresholds)
  return {
    organization_id: organizationId,
    title: d.title.trim(),
    ip: d.ip.trim(),
    mode: d.mode,
    interval_seconds: Number(d.intervalSeconds),
    ...(d.mode === 'pull' ? { pull_port: Number(d.pullPort), pull_endpoint: d.pullEndpoint.trim() } : {}),
    ...(d.diskMode === 'selected' ? { all_mounts_alert: false, custom_alert_mounts: selectedMounts(d) } : {}),
    ...(Object.keys(custom).length > 0 ? { thresholds: custom } : {}),
    ...(Object.keys(d.mountThresholds).length > 0 ? { mount_thresholds: customMounts(d.mountThresholds) } : {}),
    ...(Object.keys(d.containerThresholds).length > 0 ? { container_thresholds: customMounts(d.containerThresholds) } : {}),
  }
}

export interface SummarySection {
  step: StepId
  title: string
  lines: string[]
}

// Neyin kaydedileceği, onu üreten adımın başlığı altında.
export function summarize(d: WizardDraft, defaults: Defaults): SummarySection[] {
  const server = [`Ad: ${d.title.trim() || '—'}`, `IP: ${d.ip.trim() || '—'}`, `Mod: ${d.mode}`, `Aralık: ${d.intervalSeconds} sn`]
  if (d.mode === 'pull') server.push(`Pull: port ${d.pullPort}, endpoint ${d.pullEndpoint.trim() || '—'}`)

  const mounts = selectedMounts(d)
  const disks =
    d.diskMode === 'all'
      ? ['Raporlanan tüm diskler için alert üretilir.']
      : mounts.length > 0
        ? [`Yalnızca şu diskler için alert üretilir: ${mounts.join(', ')}`, `Bunlardan biri ${MISSING_AFTER} ardışık raporda görünmezse “disk kayboldu” alert’i açılır.`]
        : ['Hiç disk seçilmedi: bu sunucu için disk alert’i kapalı.']

  const thresholds = METRICS.flatMap((m) => {
    const line = `${m.label} — ${describeDraft(m.type, d.thresholds[m.type], defaults)}`
    if (m.type === 'disk') return [line, ...describeMounts(d.mountThresholds).map((l) => withSelectionNote(l, d))]
    if (m.type === 'docker_restart') return [line, ...describeContainers(d.containerThresholds)]
    return [line]
  })

  return [
    { step: 'server', title: 'Sunucu', lines: server },
    { step: 'disks', title: 'Diskler', lines: disks },
    { step: 'thresholds', title: 'Eşikler', lines: thresholds },
  ]
}

// Diskler adımının alert seçiminin dışında bıraktığı, ama kendi eşiği olan bir mount asla alert
// üretmez; kişinin sonradan fark etmesine bırakmak yerine bunu özette söyle.
function withSelectionNote(line: string, d: WizardDraft): string {
  if (d.diskMode !== 'selected') return line
  const selected = selectedMounts(d)
  const mount = line.slice('Disk '.length, line.indexOf(' — '))
  return selected.includes(mount) ? line : `${line} (uyarı: bu disk, Diskler adımındaki alert seçiminde yok; alert üretmez)`
}
