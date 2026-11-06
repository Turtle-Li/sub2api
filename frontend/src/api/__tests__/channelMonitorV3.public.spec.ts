import { describe, expect, it, vi } from 'vitest'

const mocks = vi.hoisted(() => ({
  publicGet: vi.fn().mockResolvedValue({ data: { categories: [] } }),
  privateGet: vi.fn().mockResolvedValue({ data: {} }),
  privatePut: vi.fn().mockResolvedValue({ data: { mode: 'v3' } }),
}))
vi.mock('axios', () => ({ default: { create: () => ({
  get: mocks.publicGet,
  interceptors: { response: { use: vi.fn() } },
}) } }))
vi.mock('../client', () => ({ apiClient: { get: mocks.privateGet, put: mocks.privatePut } }))
import { getStatus, getIncidents, setMonitorMode } from '../channelMonitorV3'

describe('anonymous status transport', () => {
  it('updates monitor mode through the registered admin route', async () => {
    expect(await setMonitorMode('v3')).toBe('v3')
    expect(mocks.privatePut).toHaveBeenCalledWith('/admin/channel-monitor-v3/mode', { mode: 'v3' })
  })
  it('uses independent public client for both histories and incidents', async () => {
    await getStatus(123, 'public')
    await getIncidents(1, 20, 'public')
    expect(mocks.publicGet).toHaveBeenCalledWith('/public/channel-monitor-v3/status', expect.objectContaining({ params: { end: 123 } }))
    expect(mocks.publicGet).toHaveBeenCalledWith('/public/channel-monitor-v3/incidents', expect.objectContaining({ params: { page: 1, page_size: 20 } }))
    expect(mocks.privateGet).not.toHaveBeenCalled()
  })
})
