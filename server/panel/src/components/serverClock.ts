// Server saatinin saf hesabı (React yok; birim test edilir).

export interface ServerClock {
  // Kurulumun saat dilimi ve o anki UTC ofseti ("Europe/Istanbul", "UTC+3").
  timezone: string
  offset: string
  // Kurulumun saatinde şu an ("2026-10-10T21:11") ve gösterim için saat:dakika ("21:11").
  nowLocal: string
  time: string
}

interface MetaClock {
  timezone: string
  utc_offset: string
  utc_offset_seconds: number
  server_time: string
  fetchedAt: number
}

// serverClock, /meta'nın verdiği server saatini, yanıtın alınmasından bu yana geçen süre kadar ilerletir ve kurulumun
// ofsetiyle yerel saate çevirir.
export function serverClock(meta: MetaClock, browserNow: number): ServerClock {
  const elapsed = Math.max(browserNow - meta.fetchedAt, 0)
  const local = new Date(Date.parse(meta.server_time) + elapsed + meta.utc_offset_seconds * 1000)
  const nowLocal = local.toISOString().slice(0, 16)
  return { timezone: meta.timezone, offset: meta.utc_offset, nowLocal, time: nowLocal.slice(11) }
}
