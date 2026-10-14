import assert from 'node:assert/strict'
import { test } from 'node:test'
import type { DNSConfig } from '../types/api.ts'
import { dnsOverTLSLabel, dnssecLabel, domainItems, resolverLabel, serverGroups } from './dns.ts'

test('resolverLabel says who answers the applications', () => {
  assert.equal(resolverLabel({ resolver: 'resolved', mode: 'stub' }), 'systemd-resolved (stub, 127.0.0.53)')
  assert.equal(resolverLabel({ resolver: 'resolved' }), 'systemd-resolved')
  assert.equal(resolverLabel({ resolver: 'resolv.conf' }), '/etc/resolv.conf (doğrudan)')
})

test('DNSSEC and DNS-over-TLS settings read in Turkish', () => {
  assert.equal(dnssecLabel('no'), 'kapalı')
  assert.equal(dnssecLabel('allow-downgrade'), 'destekleniyorsa')
  assert.equal(dnsOverTLSLabel('opportunistic'), 'fırsat buldukça')
  assert.equal(dnsOverTLSLabel('yes'), 'açık')
  assert.equal(dnssecLabel(undefined), '—')
})

test('serverGroups groups by interface, global servers first', () => {
  const d: DNSConfig = {
    resolver: 'resolved',
    servers: [
      { address: '192.168.1.1', interface: 'enp4s0' },
      { address: '10.0.0.2' },
      { address: 'fd00::1', interface: 'wg0' },
      { address: '192.168.1.2', interface: 'enp4s0' },
    ],
  }
  assert.deepEqual(serverGroups(d), [
    { iface: undefined, addresses: ['10.0.0.2'] },
    { iface: 'enp4s0', addresses: ['192.168.1.1', '192.168.1.2'] },
    { iface: 'wg0', addresses: ['fd00::1'] },
  ])
  assert.deepEqual(serverGroups({ resolver: 'resolv.conf' }), [])
})

test('domainItems lists search domains before routing-only domains', () => {
  assert.deepEqual(domainItems({ resolver: 'resolved', search_domains: ['sirket.local'], routing_domains: ['lab.local'] }), [
    { name: 'sirket.local', routingOnly: false },
    { name: 'lab.local', routingOnly: true },
  ])
})
