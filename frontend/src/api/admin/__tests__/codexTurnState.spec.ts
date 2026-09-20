import { beforeEach, describe, expect, it, vi } from 'vitest'

const { request } = vi.hoisted(() => ({ request: vi.fn() }))

vi.mock('@/api/client', () => ({ apiClient: { request } }))

import { requestCodexPanel } from '../codexTurnState'

describe('Codex turn-state panel API bridge', () => {
  beforeEach(() => {
    request.mockReset()
    request.mockResolvedValue({ data: { ok: true } })
  })

  it.each([
    ['GET', 'api/state?window=30m', undefined],
    ['GET', 'api/settings', undefined],
    ['POST', 'api/settings', {refresh_advance_minutes:5}],
    ['POST', 'api/probe', { account_id: 17, model: 'gpt-6-astra', force: true }],
    ['DELETE', 'api/accounts/17/gpt-6-astra', undefined],
    ['POST', 'api/proxy-sources/0123456789abcdef0123456789abcdef/enabled', { enabled: false }],
  ])('forwards allowed %s panel requests through the admin prefix', async (method, path, body) => {
    await expect(requestCodexPanel(method, path, body)).resolves.toEqual({ ok: true })

    const route = path.replace(/^\//, '')
    expect(request).toHaveBeenCalledWith({
      method,
      url: `/admin/codex-turn-state/${route}`,
      data: body,
      timeout: 20000,
    })
  })

  it.each([
    ['GET', 'api/credentials'],
    ['GET', 'api/accounts/0/gpt-6-astra'],
    ['PUT', 'api/probe'],
    ['POST', 'https://untrusted.example/api/state'],
    ['GET', 'api/../state'],
    ['GET', 'api\\state'],
  ])('rejects unsupported panel request %s %s before calling the API client', async (method, path) => {
    await expect(requestCodexPanel(method, path)).rejects.toThrow(/Unsupported panel (path|request)/)
    expect(request).not.toHaveBeenCalled()
  })
})
