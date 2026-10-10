import { StatusBadge } from './StatusBadge'

// Sunucu listelerinde bakımdaki sunucunun rozeti; üzerine gelince bakımın bitişi (kurulumun saatinde). Bakımda değilse
// hiçbir şey çizmez.
export function MaintenanceBadge({ untilLocal }: { untilLocal?: string }) {
  if (!untilLocal) return null
  const until = `${untilLocal.slice(0, 10).replaceAll('-', '.')} ${untilLocal.slice(11, 16)}`
  return (
    <StatusBadge tone="neutral" title={`Bakımda; bitiş ${until}. Bu sürede bildirim gönderilmez.`}>
      Bakımda
    </StatusBadge>
  )
}
