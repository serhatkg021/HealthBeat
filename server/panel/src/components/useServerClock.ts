import { serverClock, type ServerClock } from './serverClock'
import { useMeta } from './useAgentPolicy'
import { useNow } from './useNow'

// Server saati (kurulumun saat diliminde): /meta bir kez okunur, tarayıcıda ilerletilir; bilgisayarın saat dilimi ya
// da saati yanlış olsa da server'ın saatini gösterir. meta yoksa null.
export function useServerClock(): ServerClock | null {
  const meta = useMeta()
  const now = useNow(15_000)
  return meta ? serverClock(meta, now) : null
}
