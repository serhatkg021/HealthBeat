import { useState } from 'react'
import { Check, Copy } from 'lucide-react'

// Sunucuda çalıştırılacak bir komutu öne çıkaran tek satırlık kutu; kopyalama düğmesi verir. Komut satır sonunda bölünmez
// (dar ekranda kutunun içinde yatay kayar). `value` ile komut değil kopyalanacak bir değer (ör. host id) gösterilir:
// "$" işareti olmaz ve kutu vurgusuzdur; `name` kopyalama düğmesinin erişilebilir adında kullanılır.
export function CommandLine({ command, value, name }: { command?: string; value?: string; name?: string }) {
  const [copied, setCopied] = useState(false)
  const text = command ?? value ?? ''

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
    <div className={command === undefined ? 'command-line command-line-value' : 'command-line'}>
      <code>
        {command !== undefined && (
          <span className="command-prompt" aria-hidden="true">
            $
          </span>
        )}
        {text}
      </code>
      <button className="btn btn-sm" type="button" onClick={copy} aria-label={command !== undefined ? `Komutu kopyala: ${text}` : `${name ?? 'Değeri'} kopyala`}>
        {copied ? <Check size={14} strokeWidth={2} /> : <Copy size={14} strokeWidth={1.9} />}
        {copied ? 'Kopyalandı' : 'Kopyala'}
      </button>
    </div>
  )
}
