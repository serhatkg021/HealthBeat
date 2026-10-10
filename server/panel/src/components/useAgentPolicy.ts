import { useEffect, useState } from 'react'
import { metaApi } from '../api/endpoints'
import { noPolicy, type AgentPolicy } from '../pages/agentStatus'
import type { Meta } from '../types/api'

// Server'ın /meta yanıtı (sürüm politikası, server sürümü) oturum boyunca değişmez (server yapılandırması); her
// sayfa yüklenişinde yeniden istemek yerine tek istek paylaşılır. Başarısızlık kritik değildir: meta olmadan panel
// yalnızca "sürüm bilgisi yok / bilinmiyor" gibi politikasız durumları gösterir.
// fetchedAt, yanıtın alındığı tarayıcı saatidir: server saati bundan sonra tarayıcıda ilerletilir (useServerClock).
export type LoadedMeta = Meta & { fetchedAt: number }

let shared: Promise<LoadedMeta | null> | null = null
const listeners = new Set<() => void>()

// refreshMeta, /meta'yı yeniden okutur (ör. saat dilimi ayarı değişince): açık bütün bileşenler yeni değeri alır.
export function refreshMeta() {
  shared = null
  for (const l of listeners) l()
}

function loadMeta(): Promise<LoadedMeta | null> {
  shared ??= metaApi
    .get()
    .then((m) => ({ ...m, fetchedAt: Date.now() }))
    .catch(() => {
      shared = null // bir sonraki çağrıda yeniden dene
      return null
    })
  return shared
}

export function useMeta(): LoadedMeta | null {
  const [meta, setMeta] = useState<LoadedMeta | null>(null)
  const [version, setVersion] = useState(0)
  useEffect(() => {
    const bump = () => setVersion((v) => v + 1)
    listeners.add(bump)
    return () => {
      listeners.delete(bump)
    }
  }, [])
  useEffect(() => {
    let alive = true
    void loadMeta().then((m) => alive && setMeta(m))
    return () => {
      alive = false
    }
  }, [version])
  return meta
}

export function useAgentPolicy(): AgentPolicy {
  const meta = useMeta()
  return meta ? { latest: meta.latest_agent_version, min: meta.min_agent_version } : noPolicy
}

// Server'ın sürümü (kenar çubuğundaki panel/server sürüm satırı için); erişilemezse undefined.
export function useServerVersion(): string | undefined {
  return useMeta()?.server_version
}
