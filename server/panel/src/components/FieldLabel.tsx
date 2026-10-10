import type { ReactNode } from 'react'
import { InfoTip } from './InfoTip'

// Form alanının etiketi; açıklaması alanın altında yer kaplamak yerine yanındaki "i" düğmesindedir. children etiketin
// yanına eklenen küçük öğelerdir (ör. "değiştirildi" rozeti, "Varsayılana dön").
export function FieldLabel({ htmlFor, label, hint, children }: { htmlFor: string; label: string; hint?: ReactNode; children?: ReactNode }) {
  return (
    <div className="field-head">
      <label htmlFor={htmlFor}>{label}</label>
      {hint && <InfoTip label={label}>{hint}</InfoTip>}
      {children}
    </div>
  )
}
