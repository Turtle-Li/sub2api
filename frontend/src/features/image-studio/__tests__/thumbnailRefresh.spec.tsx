import React, { act } from 'react'
import { createRoot } from 'react-dom/client'
import { afterEach, describe, expect, it, vi } from 'vitest'
import type { StudioTask, studioAPI } from '../api'
import TaskCard from '../upstream/components/TaskCard'
import TaskGrid from '../upstream/components/TaskGrid'

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

  it('adapts the card preview frame to the loaded image aspect ratio and renders the platform mark with short model name', async () => {
    const record = {
      id: 'job/landscape',
      job: { id: 'job', status: 'completed', provider: 'vertex', model: 'gemini-3.1-pro-image', created_at: 10 },
      item: { custom_id: 'landscape', status: 'success', image_count: 1, prompt_preview: 'mountain sunrise' },
    } as StudioTask
    const thumbnail = vi.fn().mockResolvedValue(new Blob(['cover'], { type: 'image/jpeg' }))
    const api = { thumbnail } as unknown as ReturnType<typeof studioAPI>
    vi.stubGlobal('URL', class extends URL { static createObjectURL = vi.fn(() => 'blob:landscape'); static revokeObjectURL = vi.fn() })
    const host = document.createElement('div'); document.body.append(host)
    const root = createRoot(host)
    cleanup = () => { act(() => root.unmount()); host.remove() }

    await act(async () => root.render(<TaskCard record={record} api={api} onOpen={vi.fn()} onEditOutputs={vi.fn()} />))
    expect(host.querySelector('.studio-card-model-mark')?.textContent).toBe('G')
    expect(host.querySelector('.studio-card-model-name')?.textContent).toBe('3.1 Pro')
    const preview = host.querySelector<HTMLElement>('.studio-card-preview')!
    const image = host.querySelector<HTMLImageElement>('img')!
    expect(preview.getAttribute('data-has-image')).toBe('true')
    Object.defineProperties(image, {
      naturalWidth: { configurable: true, value: 480 },
      naturalHeight: { configurable: true, value: 270 },
    })
    await act(async () => { image.dispatchEvent(new Event('load')) })
    expect(Number(preview.style.aspectRatio)).toBeCloseTo(16 / 9, 3)
  })

  it('distributes cards across waterfall columns and triggers downward loading when the bottom sentinel intersects', async () => {
    let intersect: ((entries: IntersectionObserverEntry[]) => void) | undefined
    vi.stubGlobal('IntersectionObserver', class {
      constructor(cb: (entries: IntersectionObserverEntry[]) => void) { intersect = cb }
      observe() {}
      disconnect() {}
    })
    const tasks = [1, 2, 3, 4].map(index => ({
      id: `job/${index}`,
      job: { id: 'job', status: 'completed', provider: 'openai_images', model: 'gpt-image-2.5', created_at: index },
      item: { custom_id: String(index), status: 'success', image_count: 0, prompt_preview: `item ${index}` },
    })) as StudioTask[]
    const onLoadMore = vi.fn()
    const host = document.createElement('div'); document.body.append(host)
    const root = createRoot(host)
    cleanup = () => { act(() => root.unmount()); host.remove() }

    await act(async () => root.render(
      <TaskGrid tasks={tasks} api={{} as ReturnType<typeof studioAPI>} open={vi.fn()} edit={vi.fn()} hasMore onLoadMore={onLoadMore} />
    ))
    expect(host.querySelectorAll('[data-gallery-column]')).toHaveLength(3)
    await act(async () => { intersect?.([{ isIntersecting: true } as IntersectionObserverEntry]) })
    expect(onLoadMore).toHaveBeenCalledOnce()
  })
})
