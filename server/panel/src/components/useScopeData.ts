import { useEffect, useState } from 'react'
import { dashboardApi, organizationsApi } from '../api/endpoints'
import type { Organization, OverviewHost } from '../types/api'

// Kapsam seçicilerinin verisi: organizasyonlar (görebilene) ve sunucular. Sunucular Özet'in uç noktasından gelir; liste
// kullanıcının yetkisine göre zaten süzülüdür (operatöre yalnızca atanmış olanlar).
export function useScopeData(canSeeOrgs: boolean) {
  const [orgs, setOrgs] = useState<Organization[]>([])
  const [hosts, setHosts] = useState<OverviewHost[]>([])
  const [orgNames, setOrgNames] = useState<Map<string, string>>(new Map())

  useEffect(() => {
    if (canSeeOrgs) organizationsApi.list().then(setOrgs).catch(() => undefined)
    dashboardApi
      .overview()
      .then((o) => {
        setHosts([...o.hosts].sort((a, b) => a.title.localeCompare(b.title)))
        setOrgNames(new Map(o.organizations.map((x) => [x.id, x.name])))
      })
      .catch(() => undefined)
  }, [canSeeOrgs])

  return { orgs, hosts, orgNames }
}
