import type { ReactNode } from 'react'
import { Search } from 'lucide-react'
import type { AlertLevel, AlertType } from '../types/api'
import { alertLevelLabel, alertMetricLabel, hostStatusLabel } from '../labels'
import { LEVELS, METRICS, SINCE_LABELS, SINCE_WINDOWS, type DashboardFilters, type SinceKey } from './dashboardFilters'

const cap = (s: string) => s.charAt(0).toLocaleUpperCase('tr') + s.slice(1)

const toggle = <T,>(list: T[], item: T): T[] => (list.includes(item) ? list.filter((x) => x !== item) : [...list, item])

function Choice<T extends string>({
  label,
  value,
  options,
  onChange,
}: {
  label: string
  value: T | ''
  options: { value: T | ''; label: string }[]
  onChange: (v: T | '') => void
}) {
  return (
    <div className="segmented" role="group" aria-label={label}>
      {options.map((o) => (
        <button key={o.value} type="button" aria-pressed={value === o.value} onClick={() => onChange(o.value)}>
          {o.label}
        </button>
      ))}
    </div>
  )
}

function Section({ title, hint, children }: { title: string; hint?: string; children: ReactNode }) {
  return (
    <fieldset className="filter-section">
      <legend>{title}</legend>
      {hint && <p className="form-hint filter-hint">{hint}</p>}
      {children}
    </fieldset>
  )
}

// Özet ekranı süzgeçlerinin formu. Değişiklikler anında uygulanır (ekran arkada güncellenir).
export function DashboardFilterPanel({
  filters,
  onChange,
  organizations,
}: {
  filters: DashboardFilters
  onChange: (next: DashboardFilters) => void
  organizations: { id: string; name: string }[]
}) {
  const set = (patch: Partial<DashboardFilters>) => onChange({ ...filters, ...patch })

  return (
    <div className="filter-panel">
      <Section title="Sunucu">
        <div className="form-row filter-search">
          <label htmlFor="filter-query" className="visually-hidden">
            Sunucu ara
          </label>
          <div className="input-icon">
            <Search size={15} strokeWidth={1.9} />
            <input
              id="filter-query"
              type="search"
              placeholder="Sunucu adı ya da IP ara"
              value={filters.query}
              onChange={(e) => set({ query: e.target.value })}
            />
          </div>
        </div>
        <div className="filter-field">
          <span className="filter-label">Durum</span>
          <Choice
            label="Sunucu durumu"
            value={filters.status}
            options={[
              { value: '', label: 'Tümü' },
              { value: 'online', label: cap(hostStatusLabel('online')) },
              { value: 'offline', label: cap(hostStatusLabel('offline')) },
            ]}
            onChange={(status) => set({ status })}
          />
        </div>
        <div className="filter-field">
          <span className="filter-label">Mod</span>
          <Choice
            label="Toplama modu"
            value={filters.mode}
            options={[
              { value: '', label: 'Tümü' },
              { value: 'push', label: 'push' },
              { value: 'pull', label: 'pull' },
            ]}
            onChange={(mode) => set({ mode })}
          />
        </div>
      </Section>

      {organizations.length > 1 && (
        <Section title="Organizasyon" hint="Birden fazla seçebilirsiniz; hiçbiri seçili değilse hepsi gösterilir.">
          {organizations.map((o) => (
            <label key={o.id} className="check-row">
              <input type="checkbox" checked={filters.orgs.includes(o.id)} onChange={() => set({ orgs: toggle(filters.orgs, o.id) })} />
              {o.name}
            </label>
          ))}
        </Section>
      )}

      <Section title="Agent" hint="Eski (sürüm bildirmeyen), güncel olmayan ya da desteklenmeyen sürümdeki agent'lar.">
        <label className="check-row">
          <input type="checkbox" checked={filters.agentUpdate} onChange={(e) => set({ agentUpdate: e.target.checked })} />
          Yalnızca agent'ı güncellenmesi gereken sunucular
        </label>
      </Section>

      <Section title="Alert'ler" hint="Bunlardan biri seçilince yalnızca eşleşen alert'i olan sunucular listelenir.">
        <label className="check-row">
          <input type="checkbox" checked={filters.withAlerts} onChange={(e) => set({ withAlerts: e.target.checked })} />
          Yalnızca açık alert'i olan sunucular
        </label>
        <div className="filter-field">
          <span className="filter-label">Seviye</span>
          <div className="filter-checks">
            {LEVELS.map((l: AlertLevel) => (
              <label key={l} className="check-row">
                <input type="checkbox" checked={filters.levels.includes(l)} onChange={() => set({ levels: toggle(filters.levels, l) })} />
                {cap(alertLevelLabel(l))}
              </label>
            ))}
          </div>
        </div>
        <div className="filter-field">
          <span className="filter-label">Metrik</span>
          <div className="filter-checks">
            {METRICS.map((m: AlertType) => (
              <label key={m} className="check-row">
                <input type="checkbox" checked={filters.metrics.includes(m)} onChange={() => set({ metrics: toggle(filters.metrics, m) })} />
                {alertMetricLabel(m)}
              </label>
            ))}
          </div>
        </div>
        <div className="filter-field">
          <span className="filter-label">Alert zamanı</span>
          <Choice<SinceKey>
            label="Alert'in açıldığı zaman"
            value={filters.since}
            options={[
              { value: '', label: 'Tümü' },
              ...(Object.keys(SINCE_WINDOWS) as SinceKey[]).map((k) => ({ value: k, label: cap(SINCE_LABELS[k]) })),
            ]}
            onChange={(since) => set({ since })}
          />
        </div>
      </Section>
    </div>
  )
}
