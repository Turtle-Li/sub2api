import React, { act } from 'react'
import { createRoot } from 'react-dom/client'
import { afterEach, expect, it, vi } from 'vitest'
import type { BatchImageModel } from '@/api/batchImage'
import { Studio } from '../Studio'
import { listKeys, studioAPI, readImage } from '../api'

vi.mock('../api', async importOriginal => {
 const actual = await importOriginal<typeof import('../api')>()
 return { ...actual, listKeys: vi.fn(), studioAPI: vi.fn(), readImage: vi.fn() }
})
vi.stubGlobal('IS_REACT_ACT_ENVIRONMENT', true)
vi.stubGlobal('ResizeObserver', class { observe() {} disconnect() {} })
vi.stubGlobal('IntersectionObserver', undefined)
Object.defineProperties(HTMLDialogElement.prototype, {
 showModal: { configurable: true, value() { this.open = true } },
 close: { configurable: true, value() { this.open = false } },
})
const seedream: BatchImageModel = { id: 'seedream-fixture', object: 'model', provider: 'fixture', capabilities: { image_sizes: ['1K', '2K', '4K'], aspect_ratios: ['1:1', '16:9'], default_image_size: '2K', default_aspect_ratio: '1:1', max_reference_images: 4, response_mime_types: ['image/png'] } }
const muse: BatchImageModel = { id: 'muse-fixture', object: 'model', provider: 'fixture', capabilities: { image_sizes: ['large'], aspect_ratios: ['3:2'], default_image_size: 'large', default_aspect_ratio: '3:2', max_reference_images: 1, response_mime_types: [] } }
const legacy: BatchImageModel = { id: 'legacy-fixture', object: 'model', provider: 'fixture' }
let unmount = () => {}
afterEach(() => { unmount(); vi.clearAllMocks() })
function element<T extends Element>(host: HTMLElement, selector: string): T {
 const found = host.querySelector<T>(selector)
 if (!found) throw new Error(`Missing ${selector}`)
 return found
}
async function change(host: HTMLElement, name: string, value: string) {
 if (name === '图片模型') {
  await act(async () => { element<HTMLButtonElement>(host, 'button[aria-label="图片模型"]').click() })
  const option = Array.from(host.querySelectorAll<HTMLButtonElement>('[role="option"]')).find(button => button.querySelector('strong')?.textContent === value)
  if (!option) throw new Error(`Missing model ${value}`)
  await act(async () => { option.click() })
  return
 }
 await act(async () => { const select = element<HTMLSelectElement>(host, `select[aria-label="${name}"]`); select.value = value; select.dispatchEvent(new Event('change', { bubbles: true })) })
}
async function prompt(host: HTMLElement) {
 await act(async () => {
  const input = element<HTMLTextAreaElement>(host, 'textarea')
  const setter = Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, 'value')?.set
  if (!setter) throw new Error('Missing textarea setter')
  setter.call(input, 'a mountain')
  input.dispatchEvent(new Event('input', { bubbles: true }))
 })
}
async function upload(host: HTMLElement, count: number) {
 await act(async () => {
  const input = element<HTMLInputElement>(host, 'input[type=file]')
  Object.defineProperty(input, 'files', { configurable: true, value: Array.from({ length: count }, () => new File(['image'], 'ref.png', { type: 'image/png' })) })
  input.dispatchEvent(new Event('change', { bubbles: true }))
 })
}
async function mount(models = [seedream, muse, legacy]) {
 const submit = vi.fn().mockRejectedValue(new Error('Unconfirmed submission'))
 const client = { models: vi.fn(async () => models), tasks: vi.fn(async () => ({ tasks: [], nextCursor: null, itemContinuations: [] })), image: vi.fn(), thumbnail: vi.fn(), submit, refreshPending: vi.fn(), items: vi.fn(), cancel: vi.fn() }
 vi.mocked(studioAPI).mockReturnValue(client)
 vi.mocked(listKeys).mockResolvedValue([42, 43].map(id => ({ id, name: `key-${id}`, group_name: 'fixture', status: 'active', expired: false, quota_exhausted: false })))
 vi.mocked(readImage).mockResolvedValue('data:image/png;base64,aW1hZ2U=')
 const host = document.createElement('div'); document.body.append(host)
 const root = createRoot(host); unmount = () => { act(() => root.unmount()); host.remove() }
 await act(async () => root.render(<Studio userId={1} />))
 await prompt(host)
 return { host, root, client, submit }
}
const generate = (host: HTMLElement) => element<HTMLButtonElement>(host, 'button[aria-label="开始生成"]')
it('uses Seedream server defaults, applies changed size/aspect and retains retry identity only for the same body', async () => {
 const { host, submit } = await mount()
 expect(element<HTMLSelectElement>(host, 'select[aria-label="图片尺寸"]').value).toBe('2K')
 await act(async () => generate(host).click())
 expect(submit.mock.calls[0]?.[0]).toMatchObject({ image_size: '2K', aspect_ratio: '1:1', response_mime_type: 'image/png' })
 await act(async () => generate(host).click())
 expect(submit.mock.calls[1]?.[1]).toBe(submit.mock.calls[0]?.[1])
 await change(host, '图片尺寸', '4K'); await change(host, '图片比例', '16:9')
 await act(async () => generate(host).click())
 expect(submit.mock.calls[2]?.[0]).toMatchObject({ image_size: '4K', aspect_ratio: '16:9' })
 expect(submit.mock.calls[2]?.[1]).not.toBe(submit.mock.calls[0]?.[1])
 expect(host.textContent).toContain('实际尺寸以下载图片为准')
})
it('resets invalid choices on model change and blocks excess existing refs without deleting them', async () => {
 const { host, submit } = await mount()
 await change(host, '图片尺寸', '4K'); await upload(host, 2)
 await change(host, '图片模型', muse.id)
 expect(element<HTMLSelectElement>(host, 'select[aria-label="图片尺寸"]').value).toBe('large')
 expect(element<HTMLSelectElement>(host, 'select[aria-label="图片比例"]').value).toBe('3:2')
 expect(host.querySelectorAll('.studio-references img')).toHaveLength(2)
 expect(host.textContent).toContain('请移除多余图片后生成')
 expect(generate(host).disabled).toBe(true)
 await act(async () => generate(host).click())
 expect(submit).not.toHaveBeenCalled()
 await act(async () => element<HTMLButtonElement>(host, 'button[aria-label="移除参考图 2"]').click())
 expect(generate(host).disabled).toBe(false)
 await act(async () => generate(host).click())
 expect(submit.mock.calls[0]?.[0]).toMatchObject({ image_size: 'large', aspect_ratio: '3:2', items: [{ reference_images: [{ data: 'aW1hZ2U=' }] }] })
 expect(submit.mock.calls[0]?.[0]).not.toHaveProperty('response_mime_type')
})
it('rejects an upload exceeding the server cap before reading files', async () => {
 const { host } = await mount([muse])
 await upload(host, 2)
 expect(readImage).not.toHaveBeenCalled()
 expect(host.querySelectorAll('.studio-references img')).toHaveLength(0)
 expect(host.textContent).toContain('请减少选择或先移除已有图片')
})
it('omits all optional parameters for models without capabilities', async () => {
 const { host, submit } = await mount()
 await change(host, '图片尺寸', '4K'); await change(host, '图片模型', legacy.id)
 expect(host.querySelector('select[aria-label="图片尺寸"]')).toBeNull()
 await act(async () => generate(host).click())
 for (const key of ['image_size', 'aspect_ratio', 'response_mime_type']) expect(submit.mock.calls[0]?.[0]).not.toHaveProperty(key)
})
it('uses the first valid capability entry if a server default is not in its options', async () => {
 if (!muse.capabilities) throw new Error('Fixture must declare capabilities')
 const { host, submit } = await mount([{ ...muse, capabilities: { ...muse.capabilities, default_image_size: 'invalid', default_aspect_ratio: 'invalid' } }])
 await act(async () => generate(host).click())
 expect(submit.mock.calls[0]?.[0]).toMatchObject({ image_size: 'large', aspect_ratio: '3:2' })
})
it('isolates parameter state and pending file reads when the authenticated user changes', async () => {
 const { host, root } = await mount()
 await change(host, '图片尺寸', '4K')
 let finish: (data: string) => void = () => {}
 vi.mocked(readImage).mockReturnValue(new Promise(resolve => { finish = resolve }))
 await upload(host, 1)
 const oldSignal = vi.mocked(studioAPI).mock.calls[0]?.[1]
 await act(async () => root.render(<Studio userId={2} />))
 await act(async () => finish('data:image/png;base64,b2xk'))
 expect(oldSignal?.aborted).toBe(true)
 expect(element<HTMLSelectElement>(host, 'select[aria-label="图片尺寸"]').value).toBe('2K')
 expect(host.querySelectorAll('.studio-references img')).toHaveLength(0)
 expect(element<HTMLTextAreaElement>(host, 'textarea').value).toBe('')
 expect(host.querySelector('.studio-header')).toBeNull()
 expect(host.textContent).not.toContain('使用分组')
})
