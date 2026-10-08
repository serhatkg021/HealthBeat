import { DURATION_UNITS, type DurationDraft } from './duration'

// Süre koşulunun girişi: sayı ve birim (boş = hemen). Eşik tabloları, sunucu eşikleri ve durum kuralları paylaşır.
export function DurationField({
  value,
  onChange,
  label,
  placeholder = 'hemen',
  disabled = false,
}: {
  value: DurationDraft
  onChange: (next: DurationDraft) => void
  // Ekran okuyucu için alanın adı ("Disk gecikmesi süresi").
  label: string
  placeholder?: string
  disabled?: boolean
}) {
  return (
    <span className="duration-field">
      <input
        aria-label={label}
        className="level-input duration-input"
        inputMode="decimal"
        value={value.value}
        placeholder={placeholder}
        disabled={disabled}
        onChange={(e) => onChange({ ...value, value: e.target.value })}
      />
      <select aria-label={`${label} birimi`} value={value.unit} disabled={disabled} onChange={(e) => onChange({ ...value, unit: e.target.value as DurationDraft['unit'] })}>
        {DURATION_UNITS.map((u) => (
          <option key={u.unit} value={u.unit}>
            {u.label}
          </option>
        ))}
      </select>
    </span>
  )
}
