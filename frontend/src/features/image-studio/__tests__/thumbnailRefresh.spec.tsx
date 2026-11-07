import React, { act } from 'react'
import { createRoot } from 'react-dom/client'
import { afterEach, describe, expect, it, vi } from 'vitest'
import type { StudioTask, studioAPI } from '../api'
import TaskCard from '../upstream/components/TaskCard'

vi.mock('../upstream/hooks/useNearViewport', () => ({ useNearViewport: () => true }))
vi.stubGlobal('IS_REACT_ACT_ENVIRONMENT', true)
let cleanup = () => {}
afterEach(() => { cleanup(); vi.restoreAllMocks(); vi.unstubAllGlobals() })

describe('gallery cover refresh', () => {
  it('retries a failed cover when refreshed metadata keeps the same task identity', async () => {
    const record = {
      id: 'job/one',
      job: { id: 'job', status: 'completed', model: 'fixture' },
      item: { custom_id: 'one', status: 'success', image_count: 1 },
    } as StudioTask
    const thumbnail = vi.fn().mockRejectedValueOnce(new Error('暂时无法加载缩略图')).mockResolvedValue(new Blob(['cover'], { type: 'image/jpeg' }))
    const api = { thumbnail } as unknown as ReturnType<typeof studioAPI>
    const createURL = vi.fn(() => 'blob:refreshed-cover'), revokeURL = vi.fn()
    vi.stubGlobal('URL', class extends URL { static createObjectURL = createURL; static revokeObjectURL = revokeURL })
    const host = document.createElement('div'); document.body.append(host)
    const root = createRoot(host)
    cleanup = () => { act(() => root.unmount()); host.remove() }
    const render = (task: StudioTask) => root.render(<TaskCard record={task} api={api} onOpen={vi.fn()} onEditOutputs={vi.fn()} />)

    await act(async () => render(record))
    expect(host.textContent).toContain('暂时无法加载缩略图')
    await act(async () => render(record))
    expect(thumbnail).toHaveBeenCalledTimes(1)

    await act(async () => render({ ...record, item: { ...record.item } }))
    expect(thumbnail).toHaveBeenCalledTimes(2)
    expect(host.textContent).not.toContain('暂时无法加载缩略图')
    expect(host.querySelector('img')?.getAttribute('src')).toBe('blob:refreshed-cover')
    cleanup(); cleanup = () => {}
    expect(revokeURL).toHaveBeenCalledWith('blob:refreshed-cover')
  })
})
