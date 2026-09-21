import { useEffect, useState } from 'react'

// Geçerli zaman (ms), her `everyMs` milisaniyede bir güncellenir; "şu kadar süredir" gibi canlı süreler için.
export function useNow(everyMs: number): number {
  const [now, setNow] = useState(() => Date.now())
  useEffect(() => {
    const t = setInterval(() => setNow(Date.now()), everyMs)
    return () => clearInterval(t)
  }, [everyMs])
  return now
}
