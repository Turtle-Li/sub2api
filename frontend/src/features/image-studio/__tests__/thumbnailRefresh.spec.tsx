import React, { act } from 'react'
import { createRoot } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { StudioTask } from '../api'
import { createThumbnailLoader } from '../thumbnailLoader'
import TaskCard from '../upstream/components/TaskCard'
import TaskGrid from '../upstream/components/TaskGrid'

const viewport = vi.hoisted(() => ({ priority: 2 }))
vi.mock('../upstream/hooks/useNearViewport', () => ({ useThumbnailPriority: () => viewport.priority }))
beforeEach(() => { viewport.priority = 2; vi.stubGlobal('IS_REACT_ACT_ENVIRONMENT', true) })
let cleanup = () => {}
afterEach(() => { cleanup(); vi.restoreAllMocks(); vi.unstubAllGlobals(); vi.useRealTimers() })

describe('gallery cover refresh', () => {
  it('finishes an active cover after scrolling away and keeps the same image node when returning', async () => {
    const record = {
      id: 'job/scroll',
      job: { id: 'job', status: 'completed', model: 'fixture' },
      item: { custom_id: 'scroll', status: 'succeeded', image_count: 1 },
    } as StudioTask
    let finish!: (blob: Blob) => void
    let requestSignal!: AbortSignal
    const thumbnail = vi.fn((_task: StudioTask, signal: AbortSignal) => {
      requestSignal = signal
      return new Promise<Blob>(resolve => { finish = resolve })
    })
    const scope = new AbortController()
    const previews = createThumbnailLoader(thumbnail, scope.signal)
    const createURL = vi.fn(() => 'blob:scrolled-cover'), revokeURL = vi.fn()
    vi.stubGlobal('URL', class extends URL { static createObjectURL = createURL; static revokeObjectURL = revokeURL })
    const host = document.createElement('div'); document.body.append(host)
    const root = createRoot(host)
    cleanup = () => { act(() => root.unmount()); host.remove() }
    const render = () => root.render(<TaskCard record={record} previews={previews} onOpen={vi.fn()} onEditOutputs={vi.fn()} />)

    await act(async () => render())
    expect(thumbnail).toHaveBeenCalledOnce()
    viewport.priority = 0
    await act(async () => render())
    expect(requestSignal.aborted).toBe(false)
    await act(async () => finish(new Blob(['preview'], { type: 'image/jpeg' })))
    const image = host.querySelector('img')
    expect(image?.getAttribute('src')).toBe('blob:scrolled-cover')

    viewport.priority = 2
    await act(async () => render())
    expect(host.querySelector('img')).toBe(image)
    expect(thumbnail).toHaveBeenCalledOnce()
    expect(createURL).toHaveBeenCalledOnce()
    expect(revokeURL).not.toHaveBeenCalled()
  })

  it('retries a failed cover when refreshed metadata keeps the same task identity', async () => {
    const record = {
      id: 'job/one',
      job: { id: 'job', status: 'completed', model: 'fixture' },
      item: { custom_id: 'one', status: 'success', image_count: 1 },
    } as StudioTask
    const thumbnail = vi.fn().mockRejectedValueOnce(new Error('暂时无法加载缩略图')).mockResolvedValue(new Blob(['cover'], { type: 'image/jpeg' }))
    const scope = new AbortController()
    const previews = createThumbnailLoader(thumbnail, scope.signal)
    const createURL = vi.fn(() => 'blob:refreshed-cover'), revokeURL = vi.fn()
    vi.stubGlobal('URL', class extends URL { static createObjectURL = createURL; static revokeObjectURL = revokeURL })
    const host = document.createElement('div'); document.body.append(host)
    const root = createRoot(host)
    cleanup = () => { act(() => root.unmount()); host.remove() }
    const render = (task: StudioTask) => root.render(<TaskCard record={task} previews={previews} onOpen={vi.fn()} onEditOutputs={vi.fn()} />)

    await act(async () => render(record))
    expect(host.textContent).toContain('暂时无法加载缩略图')
    await act(async () => render(record))
    expect(thumbnail).toHaveBeenCalledTimes(1)

    await act(async () => render({ ...record, item: { ...record.item } }))
    expect(thumbnail).toHaveBeenCalledTimes(2)
    expect(host.textContent).not.toContain('暂时无法加载缩略图')
    expect(host.querySelector('img')?.getAttribute('src')).toBe('blob:refreshed-cover')
    await act(async () => scope.abort())
    expect(host.querySelector('img')).toBeNull()
    expect(revokeURL).toHaveBeenCalledWith('blob:refreshed-cover')
  })

  it('uses the metadata frame before visible decoding and does not resize on decode or failure', async () => {
    const record = {
      id: 'job/landscape',
      job: { id: 'job', status: 'completed', provider: 'vertex', model: 'gemini-3.1-pro-image', created_at: 10 },
      item: { custom_id: 'landscape', status: 'success', image_count: 1, prompt_preview: 'mountain sunrise', preview_size: { width: 1500, height: 1000 } },
    } as StudioTask
    const thumbnail = vi.fn().mockResolvedValue(new Blob(['cover'], { type: 'image/jpeg' }))
    const scope = new AbortController()
    const previews = createThumbnailLoader(thumbnail, scope.signal)
    vi.stubGlobal('URL', class extends URL { static createObjectURL = vi.fn(() => 'blob:landscape'); static revokeObjectURL = vi.fn() })
    const host = document.createElement('div'); document.body.append(host)
    const root = createRoot(host)
    cleanup = () => { act(() => root.unmount()); host.remove() }

    await act(async () => root.render(<TaskCard record={record} previews={previews} onOpen={vi.fn()} onEditOutputs={vi.fn()} />))
    expect(host.querySelector('.studio-card-model-mark')?.textContent).toBe('G')
    expect(host.querySelector('.studio-card-model-name')?.textContent).toBe('3.1 Pro')
    const preview = host.querySelector<HTMLElement>('.studio-card-preview')!
    expect(preview.style.aspectRatio).toBe('1.5')
    const image = host.querySelector<HTMLImageElement>('img')!
    Object.defineProperties(image, {
      naturalWidth: { configurable: true, value: 480 },
      naturalHeight: { configurable: true, value: 270 },
    })
    await act(async () => { image.dispatchEvent(new Event('load')) })
    expect(preview.style.aspectRatio).toBe('1.5')
    await act(async () => { image.dispatchEvent(new Event('error')) })
    expect(preview.querySelector('img')).toBeNull()
    expect(preview.style.aspectRatio).toBe('1.5')
    expect(host.textContent).toContain('无法显示这张预览图。')
  })

  it('retains a successful cover through metadata refresh and revokes it when the output is cleaned', async () => {
    const record = {
      id: 'job/one',
      job: { id: 'job', status: 'completed', model: 'fixture' },
      item: { custom_id: 'one', status: 'succeeded', image_count: 1 },
    } as StudioTask
    const thumbnail = vi.fn().mockResolvedValue(new Blob(['cover'], { type: 'image/jpeg' }))
    const scope = new AbortController()
    const previews = createThumbnailLoader(thumbnail, scope.signal)
    const revokeURL = vi.fn()
    vi.stubGlobal('URL', class extends URL { static createObjectURL = vi.fn(() => 'blob:kept'); static revokeObjectURL = revokeURL })
    const host = document.createElement('div'); document.body.append(host)
    const root = createRoot(host)
    cleanup = () => { act(() => root.unmount()); host.remove() }
    const render = (task: StudioTask) => root.render(<TaskCard record={task} previews={previews} onOpen={vi.fn()} onEditOutputs={vi.fn()} />)

    await act(async () => render(record))
    const image = host.querySelector('img')
    expect(image?.getAttribute('src')).toBe('blob:kept')
    await act(async () => render({ ...record, item: { ...record.item } }))
    expect(host.querySelector('img')).toBe(image)
    expect(thumbnail).toHaveBeenCalledOnce()
    expect(revokeURL).not.toHaveBeenCalled()

    await act(async () => render({ ...record, job: { ...record.job, status: 'output_deleted' } }))
    expect(host.querySelector('img')).toBeNull()
    expect(revokeURL).toHaveBeenCalledWith('blob:kept')
  })

  it('hides and releases an already displayed cover when its deadline passes without a metadata refresh', async () => {
    vi.useFakeTimers()
    vi.setSystemTime(1700000000000)
    const record = {
      id: 'job/one',
      job: { id: 'job', status: 'completed', model: 'fixture', output_expires_at: 1700000001 },
      item: { custom_id: 'one', status: 'succeeded', image_count: 1 },
    } as StudioTask
    const previews = createThumbnailLoader(vi.fn().mockResolvedValue(new Blob(['cover'], { type: 'image/jpeg' })), new AbortController().signal)
    const revokeURL = vi.fn()
    vi.stubGlobal('URL', class extends URL { static createObjectURL = vi.fn(() => 'blob:expiring'); static revokeObjectURL = revokeURL })
    const host = document.createElement('div'); document.body.append(host)
    const root = createRoot(host)
    cleanup = () => { act(() => root.unmount()); host.remove() }

    await act(async () => root.render(<TaskCard record={record} previews={previews} onOpen={vi.fn()} onEditOutputs={vi.fn()} />))
    expect(host.querySelector('img')?.getAttribute('src')).toBe('blob:expiring')
    viewport.priority = 0
    await act(async () => root.render(<TaskCard record={record} previews={previews} onOpen={vi.fn()} onEditOutputs={vi.fn()} />))
    expect(host.querySelector('img')?.getAttribute('src')).toBe('blob:expiring')
    await act(async () => vi.advanceTimersByTime(1000))
    expect(host.querySelector('img')).toBeNull()
    expect(revokeURL).toHaveBeenCalledWith('blob:expiring')
  })

  it('does not load a cover after retention ends, even before the cleanup runs', async () => {
    const record = {
      id: 'job/old-output',
      job: { id: 'job', status: 'completed', model: 'fixture', output_expires_at: 1 },
      item: { custom_id: 'old-output', status: 'succeeded', image_count: 1 },
    } as StudioTask
    const thumbnail = vi.fn().mockResolvedValue(new Blob(['cover'], { type: 'image/jpeg' }))
    const scope = new AbortController()
    const previews = createThumbnailLoader(thumbnail, scope.signal)
    const host = document.createElement('div'); document.body.append(host)
    const root = createRoot(host)
    cleanup = () => { act(() => root.unmount()); host.remove() }

    await act(async () => root.render(<TaskCard record={record} previews={previews} onOpen={vi.fn()} onEditOutputs={vi.fn()} />))
    expect(host.querySelector('.studio-source-card')?.getAttribute('data-status')).toBe('expired')
    expect(host.querySelector('img')).toBeNull()
    expect(thumbnail).not.toHaveBeenCalled()
  })

  it('keeps existing card nodes and waterfall columns when the next page is appended', async () => {
    const tasks = [1, 2, 3, 4].map(index => ({
      id: `job/${index}`,
      job: { id: 'job', status: 'completed', provider: 'openai_images', model: 'gpt-image-2.5', created_at: index },
      item: { custom_id: String(index), status: 'success', image_count: 0, prompt_preview: `item ${index}` },
    })) as StudioTask[]
    const host = document.createElement('div'); document.body.append(host)
    const root = createRoot(host)
    cleanup = () => { act(() => root.unmount()); host.remove() }
    const previews = createThumbnailLoader(vi.fn(), new AbortController().signal)
    const render = (rows: StudioTask[]) => root.render(<TaskGrid tasks={rows} previews={previews} open={vi.fn()} edit={vi.fn()} />)

    await act(async () => render(tasks.slice(0, 2)))
    const cards = [...host.querySelectorAll('[data-task-id]')]
    await act(async () => render(tasks))
    const appended = [...host.querySelectorAll('[data-task-id]')]
    const byID = new Map(appended.map(card => [card.getAttribute('data-task-id'), card]))
    expect(cards.every(card => byID.get(card.getAttribute('data-task-id')) === card)).toBe(true)
    const columns = [...host.querySelectorAll('.studio-gallery-column')]
    expect(columns.map(column => [...column.children].map(card => card.getAttribute('data-task-id')))).toEqual(
      columns.map((_, column) => tasks.filter((_, index) => index % columns.length === column).map(task => task.id)),
    )
  })
})
