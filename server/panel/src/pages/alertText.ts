// Alert satırında gösterilen ölçüm metni (React yok; Node'un çalıştırıcısıyla birim test edilir).

import type { Alert } from '../types/api.ts'

const num = (n: number): string => (Number.isInteger(n) ? String(n) : n.toFixed(1)).replace('.', ',')

// "%97,5 (eşik %95)" ya da "7 restart (eşik 5)"; olay alert'lerinde (sunucu çevrimdışı, disk kayboldu) ölçüm yoktur: ''.
export function alertReading(a: Pick<Alert, 'alert_type' | 'value' | 'threshold'>): string {
  if (a.value === undefined || a.threshold === undefined) return ''
  if (a.alert_type === 'docker_restart') return `${num(a.value)} restart (eşik ${num(a.threshold)})`
  return `%${num(a.value)} (eşik %${num(a.threshold)})`
}
