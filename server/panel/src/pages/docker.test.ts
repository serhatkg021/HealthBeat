import assert from 'node:assert/strict'
import { test } from 'node:test'
import { dockerSummary, formatMB } from './docker.ts'
import type { DockerContainerReport } from '../types/api.ts'

const c = (name: string, status: string, cpu: number, ram: number, restarts = 0): DockerContainerReport => ({
  name, image: 'img', status, cpu_pct: cpu, ram_mb: ram, restart_count: restarts, uptime_seconds: 0,
})

test('dockerSummary counts states; resource totals only include running containers', () => {
  const s = dockerSummary([c('a', 'running', 10, 100), c('b', 'running', 5.5, 50, 2), c('c', 'exited', 99, 999, 1)])
  assert.deepEqual(s, { total: 3, running: 2, notRunning: 1, restarted: 2, cpuPct: 15.5, ramMB: 150 })
})

test('dockerSummary of nothing is all zeros', () => {
  assert.deepEqual(dockerSummary([]), { total: 0, running: 0, notRunning: 0, restarted: 0, cpuPct: 0, ramMB: 0 })
})

test('formatMB switches to GB at 1024 MB', () => {
  assert.equal(formatMB(98), '98 MB')
  assert.equal(formatMB(1023.6), '1024 MB')
  assert.equal(formatMB(1024), '1 GB')
  assert.equal(formatMB(1536), '1.5 GB')
})
