import React, { act } from 'react'
import { createRoot } from 'react-dom/client'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { Studio } from '../Studio'
import { listKeys, studioAPI, studioWalletRoute } from '../api'
import { cacheStudioKeys, cacheStudioModels } from '../cache'

vi.mock('../api', async original => ({ ...await original<typeof import('../api')>(), listKeys: vi.fn(), studioAPI: vi.fn() }))
vi.stubGlobal('IS_REACT_ACT_ENVIRONMENT', true)
let cleanup = () => {}
beforeEach(() => { sessionStorage.clear(); vi.clearAllMocks() })
afterEach(() => cleanup())
it('opens a priced wallet catalogue for a logged-in user with no keys', async () => {
  vi.mocked(listKeys).mockImplementation(async (_signal, walletMode) => {
    walletMode?.('on')
    return []
  })
  vi.mocked(studioAPI).mockReturnValue({
    models: vi.fn().mockResolvedValue([{ id: 'wan2.7-image-pro', provider: 'wan_images', object: 'model', prices: { '1K': { min: .6, max: .6 } } }]),
    tasks: vi.fn().mockResolvedValue({ tasks: [], nextCursor: null, itemContinuations: [] }),
    image: vi.fn(), thumbnail: vi.fn(), archivePreview: vi.fn(), submit: vi.fn(), refreshPending: vi.fn(), items: vi.fn(), cancel: vi.fn(),
  })
  const host = document.createElement('div'); document.body.append(host)
  const root = createRoot(host)
  cleanup = () => { act(() => root.unmount()); host.remove() }
  await act(async () => root.render(<Studio userId={1} />))
  expect(vi.mocked(studioAPI).mock.calls.map(([route]) => route)).toEqual([studioWalletRoute])
  expect(host.textContent).toContain('wan2.7-image-pro')
  expect(host.textContent).toContain('0.6 额度 / 张')
  expect(host.querySelector('a[href="/keys"]')).toBeNull()
})
it('renders cached choices before key/model requests resolve and removes them on owner change', async () => {
  cacheStudioKeys(1, [{ id: 20, name: 'studio', group_name: 'fixture', status: 'active', expired: false, quota_exhausted: false, can_generate: true, has_history: false }])
  cacheStudioModels(1, 20, [{ id: 'cached-image', provider: 'fixture', object: 'model' }])
  vi.mocked(listKeys).mockImplementation(() => new Promise(() => {}))
  vi.mocked(studioAPI).mockReturnValue({ models: vi.fn(() => new Promise(() => {})), tasks: vi.fn(), image: vi.fn(), thumbnail: vi.fn(), submit: vi.fn(), refreshPending: vi.fn(), items: vi.fn(), cancel: vi.fn() })
  const host = document.createElement('div'); document.body.append(host)
  const root = createRoot(host)
  cleanup = () => { act(() => root.unmount()); host.remove() }
  await act(async () => root.render(<Studio userId={1} />))
  expect(host.textContent).toContain('cached-image')
  const previous = vi.mocked(studioAPI).mock.calls[0]?.[1]
  await act(async () => root.render(<Studio userId={2} />))
  expect(previous?.aborted).toBe(true)
  expect(host.textContent).not.toContain('cached-image')
  expect(host.textContent).toContain('正在读取可用模型')
})
