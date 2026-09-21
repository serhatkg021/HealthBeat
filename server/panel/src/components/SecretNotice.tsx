import { useState } from 'react'
import { Check, Copy, KeyRound } from 'lucide-react'

// Bir kimlik bilgisini yalnızca bir kez gösteren uyarı kutusu; kopyalama düğmesi verir.
export function SecretNotice({
  title,
  text,
  onClose,
}: {
  title: string
  text: string
  onClose: () => void
}) {
  const [copied, setCopied] = useState(false)

  async function copy() {
    try {
      await navigator.clipboard.writeText(text)
      setCopied(true)
      setTimeout(() => setCopied(false), 2000)
    } catch {
      // Pano erişimi yoksa (ör. güvensiz bağlam) kullanıcı metni elle seçip kopyalar.
    }
  }

  return (
    <div className="notice" role="status">
      <div className="notice-title">
        <KeyRound size={16} strokeWidth={1.9} />
        {title}
      </div>
      <pre className="secret-box">{text}</pre>
      <div style={{ display: 'flex', gap: 8, flexWrap: 'wrap' }}>
        <button className="btn btn-sm" type="button" onClick={copy}>
          {copied ? <Check size={14} strokeWidth={2} /> : <Copy size={14} strokeWidth={1.9} />}
          {copied ? 'Kopyalandı' : 'Kopyala'}
        </button>
        <button className="btn btn-sm btn-ghost" type="button" onClick={onClose}>
          Kapat
        </button>
      </div>
    </div>
  )
}
