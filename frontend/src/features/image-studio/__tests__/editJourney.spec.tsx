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
 expect(host.querySelector('[role="alert"]')?.textContent).toBeUndefined()
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
  const tasks = vi.fn(async () => ({ tasks: [expired], nextCursor: null, itemContinuations: [] }))
  const client = { models: vi.fn(async () => []), tasks, image, thumbnail, submit: vi.fn(), refreshPending: vi.fn(), items: vi.fn(), cancel: vi.fn() }
  vi.mocked(studioAPI).mockReturnValue(client)
  vi.mocked(listKeys).mockResolvedValue([{ id: 42, name: 'fixture', group_name: 'fixture', status: 'active', expired: false, quota_exhausted: false }])
  const host = document.createElement('div'); document.body.append(host)
  const root = createRoot(host); unmount = () => { act(() => root.unmount()); host.remove() }
  await act(async () => root.render(<Studio userId={1} />))

  await act(async () => host.querySelector<HTMLElement>('.studio-source-card')?.click())
  const detail = host.querySelector('dialog[aria-label="任务详情"]')!
  expect(detail.textContent).toContain('图片已过期')
  const actions = Array.from(detail.querySelectorAll('button')).filter(button => ['下载图片', '引用此图继续修改'].includes(button.textContent || ''))
  expect(actions.map(button => button.disabled)).toEqual([true, true])
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
  const tasks = vi.fn(async () => ({ tasks: [task], nextCursor: null, itemContinuations: [] }))
  vi.mocked(studioAPI).mockReturnValue({ models: vi.fn(async () => []), tasks, image, thumbnail, archivePreview, submit: vi.fn(), refreshPending: vi.fn(), items: vi.fn(), cancel: vi.fn() })
  vi.mocked(listKeys).mockResolvedValue([{ id: 42, name: 'fixture', group_name: 'fixture', status: 'active', expired: false, quota_exhausted: false }])
  const host = document.createElement('div'); document.body.append(host)
  const root = createRoot(host); unmount = () => { act(() => root.unmount()); host.remove() }
  await act(async () => root.render(<Studio userId={6} />))
  expect(host.querySelector('.studio-source-card')?.getAttribute('data-status')).toBe(outcome === 'expired' ? 'expired' : 'archived')
  await act(async () => host.querySelector<HTMLElement>('.studio-source-card')?.click())
  const detail = host.querySelector('dialog[aria-label="任务详情"]')!
  const open = Array.from(detail.querySelectorAll('button')).find(button => button.textContent === '打开归档结果')
  expect(archivePreview).not.toHaveBeenCalled()
  if (outcome === 'expired') {
    expect(open).toBeUndefined()
    expect(detail.textContent).toContain('图片已过期')
  } else {
    await act(async () => open!.click())
    expect(archivePreview).toHaveBeenCalledOnce()
    const download = Array.from(detail.querySelectorAll('button')).find(button => button.textContent === '下载图片')!
    expect(download.disabled).toBe(outcome === 'failed')
    if (outcome === 'failed') {
      expect(detail.textContent).toContain('SAFETY_BLOCKED')
      expect(detail.querySelector('img')).toBeNull()
    } else expect(detail.querySelector('.studio-detail-image img')?.getAttribute('src')).toBe('blob:fixture')
  }
  expect(image).not.toHaveBeenCalled()
  expect(thumbnail).not.toHaveBeenCalled()
})
