import type { ReactNode } from 'react'
import { InfoTip } from '../components/InfoTip'
import { TopicMenu } from '../components/TopicMenu'
import type { Pill, RuleRowView, SourceKind } from './hostRuleRows'
import type { TopicId } from './ruleTopics'

// Alert kuralları sayfasının konu düzeni: solda dikey konu menüsü, sağda seçili konunun kuralları. Üç kapsam (sistem,
// organizasyon, sunucu) aynı parçaları kullanır.

export interface TopicMenuItem {
  id: TopicId
  title: string
  on: number
  total: number
  // Bu konuda kaydedilmemiş değişiklik var.
  changed: boolean
  // Menüde üzerine gelince görünen açıklama.
  desc: string
}

export function RuleTopicLayout({
  menu,
  active,
  onSelect,
  children,
}: {
  menu: TopicMenuItem[]
  active: TopicId
  onSelect: (id: TopicId) => void
  children: ReactNode
}) {
  // Seçili konunun adı soldaki menüde; panelde ayrıca başlık ve açıklama yok (açıklama menüde ipucu).
  const items = menu.map((m) => ({
    id: m.id,
    title: m.title,
    hint: m.desc,
    ...(m.total > 0 ? { meta: `${m.on}/${m.total}`, metaTitle: `${m.total} kuraldan ${m.on} tanesi etkin` } : {}),
    ...(m.changed ? { dot: { label: 'kaydedilmemiş değişiklik var' } } : {}),
  }))

  return (
    <div className="topic-layout">
      <TopicMenu items={items} active={active} onSelect={onSelect} panelId="konu-paneli" />

      <section className="card topic-panel" role="tabpanel" id="konu-paneli" aria-labelledby={`konu-${active}`}>
        <div className="rule-rows-head" aria-hidden="true">
          <span>Kural</span>
          <span>Değer</span>
          <span>Süre</span>
          <span>Kaynak</span>
          <span />
        </div>
        <div className="rule-rows">{children}</div>
      </section>
    </div>
  )
}

const PILL_CLASS: Record<Pill['tone'], string> = {
  warning: 'badge badge-warning',
  critical: 'badge badge-critical',
  info: 'badge badge-info',
  off: 'badge badge-neutral badge-plain',
  plain: 'badge badge-neutral badge-plain',
}

const SOURCE_TEXT: Record<SourceKind, string> = {
  own: 'Bu sunucu',
  inherited: 'Devralındı',
  always: 'Her zaman açık',
  none: 'Tanımlı değil',
}

// Bir kural satırı: ad ve açıklama · değer rozetleri · süre · kaynak · işlem. `editor` ve `durationEditor` verilirse
// rozetlerin ve sürenin yerine onlar çizilir (düzenlenen satır); `error` değerin altında, `extra` satırın altında tam
// genişlikte durur (konuya özel değerler, seçim listesi).
export function RuleRow({
  row,
  sourceText,
  actions,
  editor,
  durationEditor,
  error,
  extra,
  changed = false,
}: {
  row: RuleRowView
  // Kaynağın kapsama özgü metni (ör. "Devralındı · E2E Org"); verilmezse genel metin.
  sourceText?: string
  actions?: ReactNode
  editor?: ReactNode
  durationEditor?: ReactNode
  error?: string | null
  extra?: ReactNode
  changed?: boolean
}) {
  const muted = !row.on && row.kind !== 'seçim' && row.kind !== 'otomatik'
  return (
    <div className={`rule-row${changed ? ' changed' : ''}${muted ? ' muted-row' : ''}`}>
      <div className="rule-name">
        <span className="rule-label">{row.label}</span>
        <span className="rule-kind">{row.kind}</span>
        {row.hint && <InfoTip label={row.label}>{row.hint}</InfoTip>}
      </div>
      <div className="rule-value" data-label="Değer">
        {editor ?? (
          <span className="rule-pills">
            {row.pills.map((p) => (
              <span key={p.text} className={PILL_CLASS[p.tone]}>
                {p.text}
              </span>
            ))}
          </span>
        )}
        {error && <div className="field-error flush">{error}</div>}
      </div>
      <div className={`rule-duration${row.duration || durationEditor ? ' tnum' : ' muted'}`} data-label="Süre">
        {durationEditor ?? row.duration ?? '—'}
      </div>
      <div className="rule-source" data-label="Kaynak">
        {row.source && <span className={`rule-src rule-src-${row.source}`}>{sourceText ?? SOURCE_TEXT[row.source]}</span>}
      </div>
      <div className="rule-actions">{actions}</div>
      {extra && <div className="rule-extra">{extra}</div>}
    </div>
  )
}

// Konuya özel değerlerin şeridi (salt okunur): "Mount’a özel: /boot 90 / 95 %".
export function SubjectChips({
  label,
  items,
  empty = 'yok',
  action,
}: {
  label: string
  items: { name: string; value: string }[]
  empty?: string
  action?: ReactNode
}) {
  return (
    <div className="rule-strip">
      <span>{label}</span>
      {items.length === 0 && <span className="muted">{empty}</span>}
      {items.map((i) => (
        <span key={i.name} className="rule-chip">
          <code>{i.name}</code> {i.value}
        </span>
      ))}
      {action && <span className="rule-strip-action">{action}</span>}
    </div>
  )
}

// Sayfanın altına yapışık tek kaydet çubuğu: bütün konulardaki değişiklikler birlikte kaydedilir ya da atılır.
export function RuleSaveBar({
  count,
  topics,
  saving,
  error,
  blocked,
  onSave,
  onDiscard,
}: {
  count: number
  // Değişiklik olan konuların adları.
  topics: string[]
  saving: boolean
  error: string | null
  // Kaydetmeyi engelleyen sorun (ör. geçersiz değer).
  blocked?: string
  onSave: () => void
  onDiscard: () => void
}) {
  if (count === 0 && !error) return null
  return (
    <div className="rule-savebar" role="status">
      <div className="rule-savebar-text">
        <strong>{count} değişiklik kaydedilmedi</strong>
        {topics.length > 0 && <span>{topics.join(', ')}</span>}
        {blocked && <span className="rule-savebar-error">{blocked}</span>}
        {error && <span className="rule-savebar-error">{error}</span>}
      </div>
      <div className="row">
        <button type="button" className="btn" onClick={onDiscard} disabled={saving || count === 0}>
          Vazgeç
        </button>
        <button type="button" className="btn btn-primary" onClick={onSave} disabled={saving || count === 0 || !!blocked}>
          {saving ? 'Kaydediliyor…' : 'Kaydet'}
        </button>
      </div>
    </div>
  )
}

// Eşik ve durum satırlarının düğmeleri: Geri al · Düzenle · Devral ya da Özelleştir. "Geri al" satır değiştiyse ya da
// düzenleme alanı açıksa görünür: değişikliği kaydedilmiş hâline döndürür ve alanı kapatır (değişiklik yoksa yalnızca
// kapatır). Sistem kapsamında devralınacak bir şey olmadığından düğmeler "Tanımla" ve "Kaldır" adını alır.
export function RuleButtons({
  own,
  editing,
  changed,
  disabled,
  system = false,
  onRevert,
  onEdit,
  onInherit,
  onCustomize,
}: {
  own: boolean
  editing: boolean
  changed: boolean
  disabled: boolean
  system?: boolean
  onRevert: () => void
  onEdit: () => void
  onInherit: () => void
  onCustomize: () => void
}) {
  return (
    <span className="row row-tight rule-buttons">
      {(changed || editing) && (
        <button type="button" className="btn btn-sm btn-ghost" disabled={disabled} onClick={onRevert}>
          Geri al
        </button>
      )}
      {own && !editing && (
        <button type="button" className="btn btn-sm" disabled={disabled} onClick={onEdit}>
          Düzenle
        </button>
      )}
      {own ? (
        <button type="button" className="btn btn-sm" disabled={disabled} onClick={onInherit}>
          {system ? 'Kaldır' : 'Devral'}
        </button>
      ) : (
        <button type="button" className="btn btn-sm" disabled={disabled} onClick={onCustomize}>
          {system ? 'Tanımla' : 'Özelleştir'}
        </button>
      )}
    </span>
  )
}
