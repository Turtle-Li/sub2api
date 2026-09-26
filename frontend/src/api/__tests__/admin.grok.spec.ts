import { beforeEach, describe, expect, it, vi } from 'vitest'

const { post } = vi.hoisted(() => ({
  post: vi.fn(),
}))

vi.mock('@/api/client', () => ({
  apiClient: { post },
}))

import { authorizePassword, createFromSSO, getGrokSSOImportTimeout } from '@/api/admin/grok'

describe('admin Grok SSO import API', () => {
  beforeEach(() => {
    post.mockReset()
    post.mockResolvedValue({ data: { created: [], failed: [] } })
  })

  it.each([
    [1, 180_000],
    [3, 180_000],
    [4, 270_000],
    [7, 360_000],
  ])('uses a timeout sized for %i keys', async (keyCount, expectedTimeout) => {
    expect(getGrokSSOImportTimeout(keyCount)).toBe(expectedTimeout)
  })

  it('keeps a single-token import as one request', async () => {
    await createFromSSO({ sso_tokens: ['sso-1'], name: 'Grok account' })

    expect(post).toHaveBeenCalledWith(
      '/admin/grok/sso-to-oauth',
      { sso_tokens: ['sso-1'], name: 'Grok account' },
      { timeout: 180_000 },
    )
  })

  it('imports multiple tokens with at most three single-token requests in flight', async () => {
    let active = 0
    let maxActive = 0
    post.mockImplementation(async (_url, request) => {
      active++
      maxActive = Math.max(maxActive, active)
      await new Promise((resolve) => setTimeout(resolve, 5))
      active--
      return { data: { created: [{ index: 1, name: request.name }], failed: [] } }
    })

    const result = await createFromSSO({
      sso_tokens: Array.from({ length: 7 }, (_, index) => `sso-${index + 1}`),
      name: 'Grok account',
    })

    expect(post).toHaveBeenCalledTimes(7)
    expect(maxActive).toBe(3)
    expect(post.mock.calls.map((call) => call[1])).toEqual(
      Array.from({ length: 7 }, (_, index) => ({
        sso_tokens: [`sso-${index + 1}`],
        name: `Grok account #${index + 1}`,
      }))
    )
    expect(post.mock.calls.every((call) => call[2]?.timeout === 180_000)).toBe(true)
    expect(result.created.map((item) => item.index)).toEqual([1, 2, 3, 4, 5, 6, 7])
    expect(result.failed).toEqual([])
  })

  it('keeps importing after an individual request fails and reports the original index', async () => {
    post.mockImplementation(async (_url, request) => {
      if (request.sso_tokens[0] === 'sso-2') throw new Error('gateway timeout')
      return { data: { created: [{ index: 1 }], failed: [] } }
    })

    const result = await createFromSSO({ sso_tokens: ['sso-1', 'sso-2', 'sso-3', 'sso-4'] })

    expect(result.created.map((item) => item.index)).toEqual([1, 3, 4])
    expect(result.failed).toEqual([{ index: 2, error: 'gateway timeout' }])
  })

  it('preserves password whitespace and applies the authorization timeout', async () => {
    post.mockResolvedValueOnce({ data: { access_token: 'access-token' } })

    await authorizePassword(' user@example.com ----  password with spaces  ', 7)

    expect(post).toHaveBeenCalledWith(
      '/admin/grok/oauth/password',
      {
        email: 'user@example.com',
        password: '  password with spaces  ',
        proxy_id: 7,
      },
      { timeout: 120_000 },
    )
  })
})
