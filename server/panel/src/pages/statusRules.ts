// Durum kuralları: eşiği olmayan alert'lerin (servis çalışmıyor, RAID bozuk …) seviyesi ve süresi. Kapsamlar eşiklerle
// aynıdır: genel → organizasyon zinciri (en yakın kazanır) → sunucu. Hiçbir kapsamda tanımlı olmayan kural kapalıdır; bir
// kapsamdaki "kapalı" (off), üst kapsamda açık bir kuralı o kapsamda kapatır. React yok: Node'un çalıştırıcısıyla test edilir.

import type { HostStatusRuleView, StatusRule, StatusRuleChanges, StatusRuleConfig, StatusRuleLevel, StatusRuleSetting } from '../types/api.ts'
import { durationDraft, durationSeconds, emptyDuration, formatDuration, parseDuration, sameDuration, type DurationDraft } from './duration.ts'
import { orgChain, type ParentMap } from './thresholds.ts'

export interface RuleInfo {
  rule: StatusRule
  label: string
  // Alert'in ne zaman açıldığı.
  hint: string
  // Süre verilebilir mi ve süre ne anlama gelir (anlık olaylarda süre yoktur).
  duration?: string
}

// Server'ın model.StatusRules sırası.
export const STATUS_RULES: RuleInfo[] = [
  {
    rule: 'service_failed',
    label: 'Servis çalışmıyor',
    hint: 'İzlenen bir systemd servisi durdu ya da hata verdi. Yalnızca izlenen servisler değerlendirilir; seçim sunucu sayfasındaki Servisler sekmesindedir.',
    duration: 'Servis bu kadar süre çalışmazsa açılır.',
  },
  {
    rule: 'container_unhealthy',
    label: 'Container sağlıksız',
    hint: 'Docker healthcheck’i “unhealthy” diyor (healthcheck tanımlı container’larda).',
    duration: 'Container bu kadar süre sağlıksız kalırsa açılır.',
  },
  {
    rule: 'container_oom',
    label: 'Container bellek yetmezliği',
    hint: 'Container bellek sınırını aştığı için çekirdek tarafından öldürüldü. Anlık bir olaydır.',
  },
  {
    rule: 'oom_kill',
    label: 'Bellek yetmezliği (OOM)',
    hint: 'Çekirdek bellek yetmediği için bir süreç öldürdü.',
    duration: 'Süre, alert’in ne kadar sessizlikten sonra kapanacağıdır: bu kadar süre yeni olay olmazsa kapanır (boşsa sonraki raporda).',
  },
  {
    rule: 'fs_readonly',
    label: 'Dosya sistemi salt okunur',
    hint: 'Yazılabilir bir mount salt okunur oldu (çoğunlukla disk ya da dosya sistemi hatası). Anlık bir olaydır.',
  },
  {
    rule: 'raid_degraded',
    label: 'RAID bozuk',
    hint: 'Yazılım RAID dizisi bozuk (degraded, inactive ya da failed).',
    duration: 'Dizi bu kadar süre bozuk kalırsa açılır.',
  },
  {
    rule: 'raid_rebuilding',
    label: 'RAID yeniden kuruluyor',
    hint: 'Dizi yeniden kuruluyor ya da eşitleniyor. “RAID bozuk” ile aynı alert’tir: dizi bir durumdan diğerine geçince seviyesi değişir.',
    duration: 'Bu kadar süre sürerse açılır.',
  },
  {
    rule: 'time_unsynced',
    label: 'Saat senkron değil',
    hint: 'Sunucu saati NTP ile eşitlenmiyor.',
    duration: 'Bu kadar süre senkron olmazsa açılır.',
  },
  {
    rule: 'time_source',
    label: 'Saat kaynağı sorunlu',
    hint: 'Saat hâlâ senkron ama NTP sunucusuna ulaşılamıyor ya da yanıtı geçersiz sayılıyor.',
    duration: 'Sorun bu kadar süre sürerse açılır.',
  },
  {
    rule: 'reboot_required',
    label: 'Yeniden başlatma gerekli',
    hint: 'Kurulan güncellemeler sunucunun yeniden başlatılmasını bekliyor.',
  },
  {
    rule: 'security_updates',
    label: 'Güvenlik güncellemesi bekliyor',
    hint: 'En az bir güvenlik güncellemesi kurulmayı bekliyor.',
    duration: 'Güncelleme bu kadar süre beklerse açılır.',
  },
]

export const ruleInfo = (rule: StatusRule): RuleInfo => STATUS_RULES.find((r) => r.rule === rule)!

// Seçim: '' = bu kapsamda tanımlı değil (üst kapsamı izle; genel kapsamda kapalı demektir).
export type RuleChoice = '' | StatusRuleLevel

export interface RuleDraft {
  level: RuleChoice
  duration: DurationDraft
}

export type RuleDrafts = Record<StatusRule, RuleDraft>

const LEVEL_TEXT: Record<StatusRuleLevel, string> = { off: 'Kapalı', info: 'Bilgi', warning: 'Uyarı', critical: 'Kritik' }

export const ruleDraft = (s?: StatusRuleSetting | null): RuleDraft =>
  s ? { level: s.level, duration: durationDraft(s.duration_seconds) } : { level: '', duration: emptyDuration() }

export function draftsOf(get: (rule: StatusRule) => StatusRuleSetting | null | undefined): RuleDrafts {
  return Object.fromEntries(STATUS_RULES.map((r) => [r.rule, ruleDraft(get(r.rule))])) as RuleDrafts
}

// Bir kapsamın kendi satırları (organizationId undefined = genel).
export function scopeDrafts(list: StatusRuleConfig[], organizationId?: string): RuleDrafts {
  return draftsOf((rule) => list.find((r) => r.rule === rule && (r.organization_id ?? undefined) === organizationId))
}

export const hostDrafts = (views: HostStatusRuleView[]): RuleDrafts => draftsOf((rule) => views.find((v) => v.rule === rule)?.custom)

// Süre yalnızca süre alan kurallarda ve kural açıkken anlamlıdır.
const usesDuration = (rule: StatusRule, level: RuleChoice): boolean => ruleInfo(rule).duration !== undefined && level !== '' && level !== 'off'

export function validateRuleDraft(rule: StatusRule, d: RuleDraft): string | null {
  if (!usesDuration(rule, d.level)) return null
  const p = parseDuration(d.duration)
  return 'error' in p ? p.error : null
}

export function validateRuleDrafts(drafts: RuleDrafts): string[] {
  return STATUS_RULES.flatMap((r) => {
    const err = validateRuleDraft(r.rule, drafts[r.rule])
    return err ? [`${r.label}: ${err}`] : []
  })
}

// Taslağın server ayarı; '' = null (bu kapsamdaki ayarı kaldır).
export function settingOf(rule: StatusRule, d: RuleDraft): StatusRuleSetting | null {
  if (d.level === '') return null
  const seconds = usesDuration(rule, d.level) ? durationSeconds(d.duration) : undefined
  return seconds !== undefined ? { level: d.level, duration_seconds: seconds } : { level: d.level }
}

export function ruleChanged(rule: StatusRule, a: RuleDraft, b: RuleDraft): boolean {
  if (a.level !== b.level) return true
  return usesDuration(rule, a.level) && !sameDuration(a.duration, b.duration)
}

// Yalnızca değişen kurallar gönderilir; dokunulmayanlar başka bir yerden değiştirilmişse ezilmez.
export function ruleChanges(saved: RuleDrafts, draft: RuleDrafts): StatusRuleChanges {
  const out: StatusRuleChanges = {}
  for (const r of STATUS_RULES) if (ruleChanged(r.rule, saved[r.rule], draft[r.rule])) out[r.rule] = settingOf(r.rule, draft[r.rule])
  return out
}

// "Uyarı · 10 dk boyunca", "Kapalı".
export function settingText(rule: StatusRule, s: StatusRuleSetting): string {
  const text = LEVEL_TEXT[s.level]
  return s.level !== 'off' && s.duration_seconds && ruleInfo(rule).duration ? `${text} · ${formatDuration(s.duration_seconds)} boyunca` : text
}

export interface InheritedRule {
  setting: StatusRuleSetting
  // Değerin geldiği organizasyon; undefined = genel.
  fromOrganizationId?: string
}

// Bir organizasyonun kendi satırı yokken devraldığı: üst zincirde en yakın, yoksa genel (server'ın StatusRuleDefaultsFor'u).
export function inheritedForOrg(list: StatusRuleConfig[], orgId: string, parents: ParentMap, rule: StatusRule): InheritedRule | undefined {
  const pick = (r: StatusRuleConfig): StatusRuleSetting => (r.duration_seconds ? { level: r.level, duration_seconds: r.duration_seconds } : { level: r.level })
  for (const id of orgChain(orgId, parents).slice(1)) {
    const r = list.find((x) => x.rule === rule && x.organization_id === id)
    if (r) return { setting: pick(r), fromOrganizationId: id }
  }
  const global = list.find((x) => x.rule === rule && !x.organization_id)
  return global ? { setting: pick(global) } : undefined
}
