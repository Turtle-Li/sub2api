import { beforeEach, describe, expect, it, vi } from 'vitest'
import { cachedStudioKeys, cacheStudioKeys, cachedStudioModels, cacheStudioModels } from '../cache'
import type { StudioKey } from '../api'

beforeEach(() => { sessionStorage.clear(); vi.useRealTimers(); vi.restoreAllMocks() })
const model = { id: 'fixture-image', provider: 'fixture', object: 'model' }
describe('display-only studio metadata cache', () => {
  it('survives remounts while isolating owners and key identities', () => {
    cacheStudioModels(1, 20, [model])
    expect(cachedStudioModels(1, 20)).toEqual([model])
    expect(cachedStudioModels(2, 20)).toBeUndefined()
    expect(cachedStudioModels(1, 21)).toBeUndefined()
    expect(cachedStudioModels(undefined, 20)).toBeUndefined()
  })
  it('stores only directory fields, never credentials or unrelated data', () => {
    cacheStudioKeys(1, [{ id: 20, name: 'studio', group_name: 'image', status: 'active', expired: false, quota_exhausted: false, key: 'SECRET_CANARY' } as StudioKey])
    expect(cachedStudioKeys(1)?.[0]?.id).toBe(20)
    expect(JSON.stringify(sessionStorage)).not.toContain('SECRET_CANARY')
  })
  it('rejects expired, malformed and oversized entries', () => {
    vi.useFakeTimers()
    cacheStudioModels(1, 20, [model])
    vi.advanceTimersByTime(10 * 60 * 1000)
    expect(cachedStudioModels(1, 20)).toBeUndefined()
    sessionStorage.setItem('image-studio:directory:v1:1:model:20', JSON.stringify({ at: Date.now(), value: [{ ...model, capabilities: { image_sizes: null } }] }))
    expect(cachedStudioModels(1, 20)).toBeUndefined()
    sessionStorage.setItem('image-studio:directory:v1:1:model:20', 'x'.repeat(128 * 1024 + 1))
    expect(cachedStudioModels(1, 20)).toBeUndefined()
    cacheStudioKeys(1, Array.from({ length: 20 }, (_, id) => ({ id: id + 1, name: '图'.repeat(3000), group_name: 'image', status: 'active', expired: false, quota_exhausted: false })))
    expect(cachedStudioKeys(1)).toBeUndefined()
  })
  it('bounds retained catalogues and tolerates unavailable storage', () => {
    sessionStorage.setItem('image-studio:directory:v1:1:model:0', 'malformed')
    for (let key = 1; key <= 40; key++) cacheStudioModels(1, key, [model])
    expect(sessionStorage.length).toBe(32)
    vi.spyOn(Storage.prototype, 'setItem').mockImplementation(() => { throw new DOMException('full', 'QuotaExceededError') })
    expect(() => cacheStudioModels(1, 50, [model])).not.toThrow()
  })
  it('invalidates catalogues when a key moves groups or loses generation access', () => {
    const key = { id: 20, group_id: 7, name: 'studio', group_name: 'image', status: 'active', expired: false, quota_exhausted: false, can_generate: true }
    cacheStudioKeys(1, [key])
    cacheStudioModels(1, 20, [model])
    cacheStudioKeys(1, [key])
    expect(cachedStudioModels(1, 20)).toEqual([model])
    cacheStudioKeys(1, [{ ...key, group_id: 6 }])
    expect(cachedStudioModels(1, 20)).toBeUndefined()
    cacheStudioModels(1, 20, [model])
    cacheStudioKeys(1, [{ ...key, group_id: 6, can_generate: false }])
    expect(cachedStudioModels(1, 20)).toBeUndefined()
    cacheStudioModels(1, 20, [model])
    cacheStudioKeys(1, [])
    expect(cachedStudioModels(1, 20)).toBeUndefined()
  })
})
