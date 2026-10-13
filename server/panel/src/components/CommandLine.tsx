import { useState } from 'react'
import { Check, Copy } from 'lucide-react'

// Sunucuda çalıştırılacak bir komutu öne çıkaran tek satırlık kutu; kopyalama düğmesi verir. Komut satır sonunda bölünmez
// (dar ekranda kutunun içinde yatay kayar).
export function CommandLine({ command }: { command: string }) {
  const [copied, setCopied] = useState(false)

  async function copy() {
    try {
      await navigator.clipboard.writeText(command)
      setCopied(true)
      setTimeout(() => setCopied(false), 2000)
    } catch {
      // Pano erişimi yoksa (ör. güvensiz bağlam) kullanıcı komutu elle seçip kopyalar.
    }
  }

  return (
    <div className="command-line">
      <code>
        <span className="command-prompt" aria-hidden="true">
          $
        </span>
        {command}
      </code>
      <button className="btn btn-sm" type="button" onClick={copy} aria-label={`Komutu kopyala: ${command}`}>
        {copied ? <Check size={14} strokeWidth={2} /> : <Copy size={14} strokeWidth={1.9} />}
        {copied ? 'Kopyalandı' : 'Kopyala'}
      </button>
    </div>
  )
}
