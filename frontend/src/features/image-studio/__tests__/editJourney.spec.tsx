import React, { act } from 'react'
import { createRoot } from 'react-dom/client'
import { afterEach, expect, it, vi } from 'vitest'
import type { BatchImageSubmitRequest } from '@/api/batchImage'
import { Studio } from '../Studio'
import { listKeys, studioAPI, readImage, type StudioTask } from '../api'

vi.mock('../api', async importOriginal => {
 const actual = await importOriginal<typeof import('../api')>()
 return { ...actual, listKeys: vi.fn(), studioAPI: vi.fn(), readImage: vi.fn() }
})
vi.stubGlobal('IS_REACT_ACT_ENVIRONMENT', true)
vi.stubGlobal('ResizeObserver', class { observe() {} disconnect() {} })
vi.stubGlobal('IntersectionObserver', undefined)
HTMLDialogElement.prototype.close = vi.fn()
HTMLDialogElement.prototype.showModal = vi.fn()
URL.createObjectURL = vi.fn(() => 'blob:fixture')
URL.revokeObjectURL = vi.fn()
let unmount = () => {}
afterEach(() => { unmount(); vi.clearAllMocks(); vi.useRealTimers() })
const original = { id: 'root-0/one', job: { id: 'root-0', status: 'completed', model: 'fixture-image', provider: 'fixture', created_at: 1, fail_count: 0 }, item: { custom_id: 'one', status: 'succeeded', image_count: 1, prompt_preview: 'original' } } as StudioTask
const output = { id: 'edit-1/one', job: { ...original.job, id: 'edit-1', parent_batch_id: 'root-0', created_at: 2 }, item: { ...original.item, prompt_preview: 'first modification' } } as StudioTask

it('prefers an exact available 2.6 variant without overriding a deliberate selection', async () => {
  const models = ['gpt-image-2.5-sunburst', 'gpt-image-2.6-flare', 'gpt-image-2.6-sunburst'].map(id => ({ id, object: 'model', provider: 'openai_images' }))
  const client = { models: vi.fn(async () => models), tasks: vi.fn(async () => ({ tasks: [], nextCursor: null, itemContinuations: [] })), image: vi.fn(), submit: vi.fn(), refreshPending: vi.fn(), items: vi.fn(), cancel: vi.fn() }
  vi.mocked(studioAPI).mockReturnValue(client)
  vi.mocked(listKeys).mockResolvedValue([{ id: 76, name: 'fixture', group_name: 'fixture', status: 'active', expired: false, quota_exhausted: false }])
  const host = document.createElement('div'); document.body.append(host)
  const root = createRoot(host); unmount = () => { act(() => root.unmount()); host.remove() }
  await act(async () => root.render(<Studio userId={76} />))
  const trigger = host.querySelector<HTMLButtonElement>('button[aria-label="图片模型"]')!
  expect(trigger.title).toContain('gpt-image-2.6-sunburst')

  await act(async () => trigger.click())
  const previous = Array.from(host.querySelectorAll<HTMLButtonElement>('[role="option"]')).find(b => b.textContent?.includes('gpt-image-2.5-sunburst'))!
  await act(async () => previous.click())
  const clock = vi.spyOn(Date, 'now').mockReturnValue(Date.now() + 61_000)
  try {
    await act(async () => window.dispatchEvent(new Event('focus')))
    expect(client.models).toHaveBeenCalledTimes(2)
  } finally { clock.mockRestore() }
  expect(trigger.title).toContain('gpt-image-2.5-sunburst')
})

it('submits the second edit with the completed first output and its source metadata', async () => {
 let submitted = false
 const bodies: BatchImageSubmitRequest[] = []
 const image = vi.fn(async (task: StudioTask) => new Blob([task.job.id]))
 const client = {
   models: vi.fn(async () => [{ id: 'fixture-image', provider: 'fixture', object: 'model' }]),
   tasks: vi.fn(async () => ({ tasks: submitted ? [original, output] : [original], nextCursor: null, itemContinuations: [] })),
   image,
   submit: vi.fn(async (body: BatchImageSubmitRequest) => { bodies.push(body); submitted = true; return output.job }),
   refreshPending: vi.fn(async (tasks: StudioTask[]) => tasks),
   items: vi.fn(), cancel: vi.fn(),
 }
 vi.mocked(studioAPI).mockReturnValue(client)
 vi.mocked(listKeys).mockResolvedValue([{ id: 42, name: 'fixture', group_name: 'fixture', status: 'active', expired: false, quota_exhausted: false }])
 let reads = 0
 vi.mocked(readImage).mockImplementation(async () => `data:image/png;base64,${++reads === 1 ? 'b3JpZ2luYWw=' : 'bmV3LW91dHB1dA=='}`)
 const host = document.createElement('div'); document.body.append(host)
 const root = createRoot(host); unmount = () => { act(() => root.unmount()); host.remove() }
 await act(async () => root.render(<Studio userId={1} />))
 const button = (name: string) => host.querySelector<HTMLButtonElement>(`button[aria-label="${name}"]`)!
 await act(async () => button('继续改图').click())
 const typePrompt = async (text: string) => {
   const input = host.querySelector('textarea')!
   await act(async () => {
     Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, 'value')!.set!.call(input, text)
     input.dispatchEvent(new Event('input', { bubbles: true }))
   })
 }
 await typePrompt('first modification')
 await act(async () => button('生成修改图').click())
 const alerts = [...host.querySelectorAll('[role="alert"]')].map(alert => alert.textContent?.trim()).filter(Boolean)
 expect(alerts).toEqual([])
 expect(client.submit).toHaveBeenCalledOnce()
 await vi.waitFor(() => expect(host.querySelector('.studio-references img')?.getAttribute('src')).toBe('data:image/png;base64,bmV3LW91dHB1dA=='))
 await typePrompt('second modification')
 await act(async () => button('生成修改图').click())
 expect(bodies).toHaveLength(2)
 expect(bodies[0].metadata?.source_batch_id).toBe('root-0')
 expect(bodies[1]).toMatchObject({ parent_batch_id: 'root-0', metadata: { source_batch_id: 'edit-1', source_item_id: 'one' }, items: [{ prompt: 'second modification', reference_images: [{ mime_type: 'image/png', data: 'bmV3LW91dHB1dA==' }] }] })
 expect(image).toHaveBeenCalledWith(expect.objectContaining({ id: 'edit-1/one' }), 0)
})

it('applies the cancellation response immediately, retaining item pending but stopping its spinner and polling', async () => {
 vi.useFakeTimers()
 const pending = { ...original, job: { ...original.job, status: 'submitted' as const }, item: { ...original.item, status: 'pending', image_count: 0 } }
 const refreshPending = vi.fn(async (tasks: StudioTask[]) => tasks)
 const cancel = vi.fn(async () => ({ ...pending.job, status: 'cancelled' as const }))
 const client = { models: vi.fn(async () => [{ id: 'fixture-image', provider: 'fixture', object: 'model' }]), tasks: vi.fn(async () => ({ tasks: [pending], nextCursor: null, itemContinuations: [] })), image: vi.fn(), submit: vi.fn(), refreshPending, items: vi.fn(), cancel }
 vi.mocked(studioAPI).mockReturnValue(client)
 vi.mocked(listKeys).mockResolvedValue([{ id: 42, name: 'fixture', group_name: 'fixture', status: 'active', expired: false, quota_exhausted: false }])
 HTMLDialogElement.prototype.showModal = vi.fn()
 const host = document.createElement('div'); document.body.append(host)
 const root = createRoot(host); unmount = () => { act(() => root.unmount()); host.remove(); vi.useRealTimers() }
 await act(async () => root.render(<Studio userId={1} />))
 expect(host.querySelector('.animate-spin')).not.toBeNull()
 await act(async () => (host.querySelector('.studio-source-card') as HTMLElement).click())
 const cancelButton = Array.from(host.querySelectorAll('button')).find(button => button.textContent === '取消此批次')!
 await act(async () => cancelButton.click())
 expect(cancel).toHaveBeenCalledOnce()
 expect(host.textContent).toContain('已取消')
 expect(host.querySelector('.animate-spin')).toBeNull()
 expect(pending.item.status).toBe('pending')
 await act(async () => { await vi.advanceTimersByTimeAsync(12000) })
 expect(refreshPending).not.toHaveBeenCalled()
})

it('retains task details for expired work without fetching or reusing deleted bytes', async () => {
  const expired = { ...original, job: { ...original.job, status: 'output_deleted' as const, output_expires_at: 1, output_deleted_at: 2 } }
  const image = vi.fn(), thumbnail = vi.fn()
  const tasks = vi.fn<ReturnType<typeof studioAPI>['tasks']>(async (_, filters) => ({ tasks: filters?.status === 'expired' ? [expired] : [], nextCursor: null, itemContinuations: [] }))
  const client = { models: vi.fn(async () => []), tasks, image, thumbnail, submit: vi.fn(), refreshPending: vi.fn(), items: vi.fn(), cancel: vi.fn() }
  vi.mocked(studioAPI).mockReturnValue(client)
  vi.mocked(listKeys).mockResolvedValue([{ id: 42, name: 'fixture', group_name: 'fixture', status: 'active', expired: false, quota_exhausted: false }])
  const host = document.createElement('div'); document.body.append(host)
  const root = createRoot(host); unmount = () => { act(() => root.unmount()); host.remove() }
  await act(async () => root.render(<Studio userId={1} />))

  expect(tasks).toHaveBeenCalledOnce()
  expect(tasks).toHaveBeenCalledWith('', { status: 'current', search: '', root: '' })
  expect(host.querySelector('.studio-source-card')).toBeNull()
  await act(async () => [...host.querySelectorAll('button')].find(button => button.textContent === '已过期')!.click())
  expect(tasks).toHaveBeenLastCalledWith('', { status: 'expired', search: '', root: '' })
  expect(host.querySelector('.studio-source-card')).toBeNull()
  const row = host.querySelector<HTMLButtonElement>('.studio-expired-row')!
  expect(row.textContent).toContain('图片已清理')
  await act(async () => row.click())
  const detail = host.querySelector('dialog[aria-label="任务详情"]')!
  expect(detail.textContent).toContain('图片已清理')
  const actions = Array.from(detail.querySelectorAll('button')).filter(button => ['下载图片', '引用此图继续修改'].includes(button.textContent || ''))
  expect(actions).toHaveLength(0)
  expect(image).not.toHaveBeenCalled()
  expect(thumbnail).not.toHaveBeenCalled()
})

it.each(['success', 'failed', 'expired'] as const)('opens a %s Vertex archive only on demand, without guessing the per-item state', async outcome => {
  const task: StudioTask = {
    ...original,
    job: { ...original.job, provider: 'vertex', output_expires_at: outcome === 'expired' ? 1 : Math.floor(Date.now() / 1000) + 3600 },
    item: { ...original.item, status: 'result_available', image_count: 0 },
  }
  const image = vi.fn(), thumbnail = vi.fn()
  const archivePreview = vi.fn().mockResolvedValue(outcome === 'failed' ? {
    custom_id: 'one', status: 'failed', image_count: 0, images: [], error: { code: 'SAFETY_BLOCKED', message: 'provider blocked input' },
  } : {
    custom_id: 'one', status: 'success', image_count: 1, images: [new Blob(['original'], { type: 'image/png' })],
  })
  const tasks = vi.fn<ReturnType<typeof studioAPI>['tasks']>(async (_, filters) => ({ tasks: filters?.status === (outcome === 'expired' ? 'expired' : 'current') ? [task] : [], nextCursor: null, itemContinuations: [] }))
  vi.mocked(studioAPI).mockReturnValue({ models: vi.fn(async () => []), tasks, image, thumbnail, archivePreview, submit: vi.fn(), refreshPending: vi.fn(), items: vi.fn(), cancel: vi.fn() })
  vi.mocked(listKeys).mockResolvedValue([{ id: 42, name: 'fixture', group_name: 'fixture', status: 'active', expired: false, quota_exhausted: false }])
  const host = document.createElement('div'); document.body.append(host)
  const root = createRoot(host); unmount = () => { act(() => root.unmount()); host.remove() }
  const onVideo = vi.fn()
  await act(async () => root.render(<Studio userId={6} onVideo={onVideo} />))
  if (outcome === 'expired') {
    await act(async () => [...host.querySelectorAll('button')].find(button => button.textContent === '已过期')!.click())
    await act(async () => host.querySelector<HTMLButtonElement>('.studio-expired-row')!.click())
  } else {
    expect(host.querySelector('.studio-source-card')?.getAttribute('data-status')).toBe('archived')
    await act(async () => host.querySelector<HTMLElement>('.studio-source-card')?.click())
  }
  const detail = host.querySelector('dialog[aria-label="任务详情"]')!
  const open = Array.from(detail.querySelectorAll('button')).find(button => button.textContent === '打开归档结果')
  expect(archivePreview).not.toHaveBeenCalled()
  expect(detail.textContent).not.toContain('用这张图生成视频')
  if (outcome === 'expired') {
    expect(open).toBeUndefined()
    expect(detail.textContent).toContain('图片已过期')
  } else {
    await act(async () => open!.click())
    expect(archivePreview).toHaveBeenCalledOnce()
    expect(detail.textContent).toContain('归档结果不支持直接带入')
    expect(detail.textContent).not.toContain('用这张图生成视频')
    const download = Array.from(detail.querySelectorAll('button')).find(button => button.textContent === '下载图片')
    if (outcome === 'failed') {
      expect(download).toBeUndefined()
      expect(detail.textContent).toContain('SAFETY_BLOCKED')
      expect(detail.querySelector('img')).toBeNull()
    } else {
      expect(download?.disabled).toBe(false)
      expect(detail.querySelector('.studio-detail-image img')?.getAttribute('src')).toBe('blob:fixture')
    }
  }
  expect(image).not.toHaveBeenCalled()
  expect(thumbnail).not.toHaveBeenCalled()
})


it('only offers video for the readable selected original; opening the draft does not upload or submit', async () => {
  const task: StudioTask = { ...original, item: { ...original.item, image_count: 2 } }
  const image = vi.fn().mockRejectedValueOnce({ status: 403 }).mockResolvedValueOnce(new Blob(['original'], { type: 'image/png' }))
  const tasks = vi.fn().mockResolvedValue({ tasks: [task], nextCursor: null, itemContinuations: [] })
  const submit = vi.fn(), onVideo = vi.fn(async () => {}), onCanvas = vi.fn(async () => {})
  vi.mocked(studioAPI).mockReturnValue({ models: vi.fn(async () => []), image, tasks, submit, refreshPending: vi.fn(),
    items: vi.fn(), thumbnail: vi.fn(), archivePreview: vi.fn(), cancel: vi.fn() })
  vi.mocked(listKeys).mockResolvedValue([{ id: 43, name: 'own key', group_name: 'fixture', status: 'active', expired: false, quota_exhausted: false }])
  const host = document.createElement('div'); document.body.append(host)
  const root = createRoot(host); unmount = () => { act(() => root.unmount()); host.remove() }
  await act(async () => root.render(<Studio userId={7} onVideo={onVideo} onCanvas={onCanvas} />))
  expect(image).not.toHaveBeenCalled()
  await act(async () => host.querySelector<HTMLElement>('.studio-source-card')?.click())
  const action = (label: string) => [...host.querySelectorAll<HTMLButtonElement>('dialog.studio-detail button')].find(b => (b.getAttribute('aria-label') || b.textContent) === label)
  expect(action('用这张图生成视频')).toBeUndefined()
  expect(action('引用此图继续修改')).toBeDefined()
  await act(async () => action('下一张')?.click())
  expect(action('用这张图生成视频')).toBeDefined()
  await act(async () => action('用这张图生成视频')?.click())
  const add = action('加入画布'); expect(add).toBeDefined()
  await act(async () => add!.click())
  expect(onCanvas).toHaveBeenCalledWith({ route: 43, batchId: task.job.id, itemId: task.item.custom_id, index: 1 })
  expect(onVideo).toHaveBeenCalledOnce()
  expect(onVideo).toHaveBeenCalledWith({ route: 43, batchId: task.job.id, itemId: task.item.custom_id, index: 1 })
  expect(image).toHaveBeenCalledTimes(2)
  expect(submit).not.toHaveBeenCalled()
})

it('shows the requested tier immediately while sizing the detail from each output', async () => {
  const task = { ...original, job: { ...original.job, image_size: '2K' }, item: { ...original.item, image_count: 2, preview_size: { width: 600, height: 900 } } }
  const image = vi.fn(async (_task: StudioTask, index: number) => new Blob([String(index)], { type: 'image/png' }))
  const client = {
    models: vi.fn(async () => []), tasks: vi.fn(async () => ({ tasks: [task], nextCursor: null, itemContinuations: [] })),
    image, submit: vi.fn(), refreshPending: vi.fn(), items: vi.fn(), cancel: vi.fn(),
  }
  vi.mocked(studioAPI).mockReturnValue(client)
  vi.mocked(listKeys).mockResolvedValue([{ id: 42, name: 'fixture', group_name: 'fixture', status: 'active', expired: false, quota_exhausted: false }])
  const host = document.createElement('div'); document.body.append(host)
  const root = createRoot(host); unmount = () => { act(() => root.unmount()); host.remove() }
  await act(async () => root.render(<Studio userId={805} />))
  const card = host.querySelector<HTMLElement>('.studio-source-card')
  if (!card) throw new Error('No gallery card')
  expect(image).not.toHaveBeenCalled()

  await act(async () => card.click())
  const detail = host.querySelector<HTMLDialogElement>('dialog[aria-label="任务详情"]')
  if (!detail) throw new Error('No task dialog')
  const ratio = () => Number(detail.style.getPropertyValue('--detail-aspect'))
  const tier = () => [...detail.querySelectorAll('dt')].find(label => label.textContent === '分辨率档位')?.nextElementSibling?.textContent
  expect(tier()).toBe('2K')
  expect(detail.dataset.layout).toBe('portrait')
  expect(ratio()).toBeCloseTo(2 / 3)
  expect(image.mock.calls.map(([, index]) => index)).toEqual([0])

  const decode = async (width: number, height: number) => {
    const img = detail.querySelector('.studio-detail-image img')
    if (!img) throw new Error('No detail image to decode')
    Object.defineProperties(img, { naturalWidth: { value: width }, naturalHeight: { value: height } })
    await act(async () => img.dispatchEvent(new Event('load')))
  }
  await decode(1024, 1536)
  expect(tier()).toBe('2K')
  expect(detail.textContent).not.toContain('1024 × 1536')
  const next = detail.querySelector<HTMLButtonElement>('.studio-detail-actions button[aria-label="下一张"]')
  if (!next) throw new Error('No next-image control in detail actions')
  await act(async () => next.click())
  expect(ratio()).toBe(1)
  expect(tier()).toBe('2K')
  expect(image.mock.calls.map(([, index]) => index)).toEqual([0, 1])
  await decode(1536, 1024)
  expect(detail.dataset.layout).toBe('landscape')
  expect(ratio()).toBe(1.5)
  expect(tier()).toBe('2K')
  expect(detail.textContent).not.toContain('1536 × 1024')
})
