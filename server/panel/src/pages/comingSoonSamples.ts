// "Yakında" kartlarının ÖRNEK verisi: henüz gelmemiş özelliklerin nasıl görüneceğini anlatır. Gerçek bir sunucudan
// gelmez ve hiçbir gerçek hesaba (sayaç, özet, alert) girmez; yalnızca ComingSoon içinde çizilir. Özellik geldiğinde
// ilgili örnek buradan silinir.

export interface SampleService {
  name: string
  state: 'running' | 'failed' | 'activating' | 'inactive'
  since: string
  restarts: string
  watched: boolean
}

export const SAMPLE_SERVICES: SampleService[] = [
  { name: 'postgresql.service', state: 'failed', since: '4 dk', restarts: '3 (son 10 dk)', watched: true },
  { name: 'redis-server.service', state: 'activating', since: '12 sn', restarts: '7 (son 10 dk)', watched: true },
  { name: 'nginx.service', state: 'running', since: '12 gün', restarts: '0', watched: true },
  { name: 'ssh.service', state: 'running', since: '12 gün', restarts: '0', watched: false },
  { name: 'certbot.service', state: 'inactive', since: '6 sa', restarts: '0', watched: false },
]

// Disk G/Ç: aynı zaman eksenini paylaşan üç seri (0–100 arası normalize edilmiş çizim noktaları).
export const SAMPLE_DISK_IO = {
  disk: 'nvme0n1',
  latency: [8, 9, 8, 9, 8, 10, 9, 8, 52, 58, 50, 61, 55, 49, 57, 60, 53, 58],
  throughput: [6, 5, 6, 6, 5, 6, 5, 6, 14, 15, 13, 14, 15, 13, 14, 15, 14, 13],
  iops: [15, 16, 14, 15, 16, 15, 16, 15, 88, 84, 90, 86, 91, 85, 89, 92, 87, 90],
  now: { latency: '2,9 ms', read: '24 MB/sn', write: '5,5 MB/sn', iops: '10 080 / 445' },
}

export const SAMPLE_NETWORK = [
  { iface: 'enp3s0', rx: '42 Mbit/sn', tx: '8,1 Mbit/sn', errors: '0 / 12' },
  { iface: 'docker0', rx: '3,2 Mbit/sn', tx: '3,0 Mbit/sn', errors: '0 / 0' },
]

export const SAMPLE_TEMPERATURES = [
  { sensor: 'CPU paketi', celsius: 64, limit: 85 },
  { sensor: 'nvme0n1', celsius: 48, limit: 70 },
]

export const SAMPLE_PROCESSES = [
  { name: 'postgres', cpu: '38,2%', ram: '4,1 GB', count: 23 },
  { name: 'java', cpu: '21,7%', ram: '2,8 GB', count: 1 },
  { name: 'node', cpu: '9,4%', ram: '640 MB', count: 4 },
]

export const SAMPLE_UPDATES = { total: 14, security: 3, checked: '6 sa önce' }
