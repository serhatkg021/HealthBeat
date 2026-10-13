// MonoValue, makine değerlerini (telefon, kernel, hostname…) eşit aralıklı yazı tipiyle gösterir. Değer yoksa boş işareti
// "—" normal yazı tipindedir: eşit aralıklı yazı tipi uzun tireyi dar çizer ve panelin geri kalanından farklı görünür.
export function MonoValue({ value }: { value?: string | null }) {
  return value && value !== '—' ? <span className="mono">{value}</span> : <>—</>
}
