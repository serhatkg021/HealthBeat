import { useEffect, useState } from 'react'
import { metaApi } from '../api/endpoints'
import { noPolicy, type AgentPolicy } from '../pages/agentStatus'
import type { Meta } from '../types/api'

// Server'ın /meta yanıtı (sürüm politikası, server sürümü) oturum boyunca değişmez (server yapılandırması); her
// sayfa yüklenişinde yeniden istemek yerine tek istek paylaşılır. Başarısızlık kritik değildir: meta olmadan panel
// yalnızca "sürüm bilgisi yok / bilinmiyor" gibi politikasız durumları gösterir.
let shared: Promise<Meta | null> | null = null

function loadMeta(): Promise<Meta | null> {
  shared ??= metaApi.get().catch(() => {
    shared = null // bir sonraki çağrıda yeniden dene
    return null
  })
  return shared
}

function useMeta(): Meta | null {
  const [meta, setMeta] = useState<Meta | null>(null)
  useEffect(() => {
    let alive = true
    void loadMeta().then((m) => alive && setMeta(m))
    return () => {
      alive = false
    }
  }, [])
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
