import assert from 'node:assert/strict'
import { beforeEach, test } from 'node:test'

const store = new Map<string, string>()
const redirects: string[] = []
let pathname = '/'
let responder: (url: string) => { status: number; body?: unknown }
const calls: string[] = []

Object.assign(globalThis, {
  localStorage: {
    getItem: (k: string) => store.get(k) ?? null,
    setItem: (k: string, v: string) => void store.set(k, v),
    removeItem: (k: string) => void store.delete(k),
  },
  window: {
    location: {
      origin: 'http://panel.test',
      get pathname() {
        return pathname
      },
      assign: (to: string) => void redirects.push(to),
    },
  },
  fetch: async (url: string) => {
    calls.push(url)
    const { status, body } = responder(url)
    return new Response(body === undefined ? null : JSON.stringify(body), {
      status,
      headers: { 'content-type': 'application/json' },
    })
  },
})

const { apiRequest, ApiError } = await import('./client.ts')

beforeEach(() => {
  store.clear()
  redirects.length = 0
  calls.length = 0
  pathname = '/'
})

test('a cached profile without tokens is cleared and sent to the login page', async () => {
  store.set('healthbeat_user', '{"email":"a@b.c"}')
  responder = () => ({ status: 401, body: { error: 'Bearer token eksik' } })

  await assert.rejects(apiRequest('/api/v1/dashboard/summary'), (e: unknown) => e instanceof ApiError && e.status === 401)

  assert.equal(store.has('healthbeat_user'), false, 'stale profile must not survive: the login page would bounce back')
  assert.deepEqual(redirects, ['/login'])
})

test('a failed refresh ends the session including the cached profile', async () => {
  store.set('healthbeat_user', '{"email":"a@b.c"}')
  store.set('healthbeat_access_token', 'old')
  store.set('healthbeat_refresh_token', 'gone')
  responder = () => ({ status: 401, body: { error: 'nope' } })

  await assert.rejects(apiRequest('/api/v1/hosts'))

  assert.equal(store.size, 0)
  assert.deepEqual(redirects, ['/login'])
})

test('a wrong password on login keeps the server message and is not treated as an expired session', async () => {
  store.set('healthbeat_user', '{"email":"a@b.c"}')
  pathname = '/login'
  responder = () => ({ status: 401, body: { error: 'e-posta veya şifre hatalı' } })

  await assert.rejects(apiRequest('/api/v1/auth/login', { method: 'POST', body: {} }), /e-posta veya şifre hatalı/)

  assert.equal(store.has('healthbeat_user'), true)
  assert.deepEqual(redirects, [])
})

test('a signed-out visitor with no cached state gets the plain 401 error, no redirect loop', async () => {
  responder = () => ({ status: 401, body: { error: 'Bearer token eksik' } })

  await assert.rejects(apiRequest('/api/v1/hosts'), /Bearer token eksik/)

  assert.deepEqual(redirects, [])
})

test('an expired access token is refreshed and the request retried once', async () => {
  store.set('healthbeat_user', '{"email":"a@b.c"}')
  store.set('healthbeat_access_token', 'old')
  store.set('healthbeat_refresh_token', 'r1')
  responder = (url) => {
    if (url.endsWith('/auth/refresh')) return { status: 200, body: { access_token: 'new', refresh_token: 'r2' } }
    return calls.filter((c) => c.endsWith('/hosts')).length === 1 ? { status: 401 } : { status: 200, body: [1] }
  }

  assert.deepEqual(await apiRequest('/api/v1/hosts'), [1])

  assert.equal(store.get('healthbeat_access_token'), 'new')
  assert.equal(store.get('healthbeat_refresh_token'), 'r2')
  assert.equal(store.has('healthbeat_user'), true)
})

test('a server error shows its request id so the operator can find it in the log', async () => {
  store.set('healthbeat_access_token', 't')
  responder = () => ({ status: 500, body: { error: 'sunucu kaydedilemedi', code: 'internal', request_id: 'req-123' } })

  await assert.rejects(apiRequest('/api/v1/hosts'), (err: unknown) => {
    assert.ok(err instanceof ApiError)
    assert.equal(err.message, 'sunucu kaydedilemedi (hata kimliği: req-123)')
    assert.equal(err.code, 'internal')
    assert.equal(err.requestId, 'req-123')
    return true
  })
})

test('a client error keeps its message without the request id', async () => {
  store.set('healthbeat_access_token', 't')
  responder = () => ({ status: 400, body: { error: 'başlık boş olamaz', code: 'validation_failed', request_id: 'req-456' } })

  await assert.rejects(apiRequest('/api/v1/hosts'), (err: unknown) => {
    assert.ok(err instanceof ApiError)
    assert.equal(err.message, 'başlık boş olamaz')
    assert.equal(err.requestId, 'req-456')
    return true
  })
})
