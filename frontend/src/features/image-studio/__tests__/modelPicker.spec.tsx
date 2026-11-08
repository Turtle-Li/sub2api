import React, { act } from 'react'
import { createRoot } from 'react-dom/client'
import { afterEach, describe, expect, it, vi } from 'vitest'
import type { BatchImageModel } from '@/api/batchImage'
import { ModelPicker, modelPrice } from '../ModelPicker'

vi.stubGlobal('IS_REACT_ACT_ENVIRONMENT', true)
Object.defineProperties(HTMLDialogElement.prototype, {
  showModal: { configurable: true, value() { this.open = true } },
  close: { configurable: true, value() { this.open = false } },
})
const first: BatchImageModel = { id: 'image-a', provider: 'openai_images', object: 'model', prices: { '1K': { min: 0.25, max: 0.25 }, '2K': { min: 0.5, max: 0.75 } } }
const second: BatchImageModel = { id: 'image-b', provider: 'openai_images', object: 'model' }
const third: BatchImageModel = { id: 'gemini-image', provider: 'vertex', object: 'model' }
let cleanup = () => {}
afterEach(() => cleanup())

describe('image model selection', () => {
  it.each(['gemini-3-pro-image', 'gemini-3.1-flash-image'])('collapses legacy %s aliases with omitted capabilities and preserves a real capability difference', async id => {
    const stable: BatchImageModel = { id, provider: 'vertex', object: 'model', prices: { '1K': { min: .5692, max: .5692 } } }
    const preview = { ...stable, id: `${id}-preview` }
    const host = document.createElement('div'); document.body.append(host)
    const root = createRoot(host), change = vi.fn()
    cleanup = () => { act(() => root.unmount()); host.remove() }
    await act(async () => root.render(<ModelPicker models={[preview, stable]} value={`vertex:${preview.id}`} size="1K" disabled={false} onChange={change} />))
    await act(async () => host.querySelector<HTMLButtonElement>('button[aria-haspopup="dialog"]')?.click())
    expect(host.querySelectorAll('[role="option"]')).toHaveLength(1)
    expect(host.querySelector('[role="option"] strong')?.textContent).toBe(id)
    expect(change).not.toHaveBeenCalled()
    const capabilities = { image_sizes: ['1K'], aspect_ratios: ['1:1'], default_image_size: '1K', default_aspect_ratio: '1:1', max_reference_images: 4, response_mime_types: ['image/png'] }
    await act(async () => root.render(<ModelPicker models={[preview, { ...stable, capabilities }]} value={`vertex:${preview.id}`} size="1K" disabled={false} onChange={change} />))
    expect(host.querySelectorAll('[role="option"]')).toHaveLength(2)
  })
  it('shows one option for equivalent Gemini preview aliases but preserves price differences', async () => {
    const capabilities = { image_sizes: ['1K'], aspect_ratios: ['1:1'], default_image_size: '1K', default_aspect_ratio: '1:1', max_reference_images: 14, response_mime_types: ['image/png'] }
    const stable: BatchImageModel = { id: 'gemini-3-pro-image', provider: 'vertex', object: 'model', capabilities, prices: { '1K': { min: .1, max: .1 } } }
    const preview = { ...stable, id: `${stable.id}-preview` }
    const host = document.createElement('div'); document.body.append(host)
    const root = createRoot(host)
    cleanup = () => { act(() => root.unmount()); host.remove() }
    await act(async () => root.render(<ModelPicker models={[preview, stable]} value="vertex:gemini-3-pro-image" size="1K" disabled={false} onChange={vi.fn()} />))
    await act(async () => host.querySelector<HTMLButtonElement>('button[aria-haspopup="dialog"]')?.click())
    expect(host.querySelectorAll('[role="option"]')).toHaveLength(1)
    expect(host.querySelector('[role="option"] strong')?.textContent).toBe(stable.id)
    await act(async () => root.render(<ModelPicker models={[{ ...preview, prices: { '1K': { min: .2, max: .2 } } }, stable]} value="vertex:gemini-3-pro-image" size="1K" disabled={false} onChange={vi.fn()} />))
    expect(host.querySelectorAll('[role="option"]')).toHaveLength(2)
  })
  it('uses the selected resolution, shows account ranges and multiplies a batch estimate', () => {
    expect(modelPrice(first, '2K', 3)).toBe('1.5–2.25')
    expect(modelPrice(first, '1K')).toBe('0.25')
    expect(modelPrice(first, '4K')).toBe('价格待配置')
    expect(modelPrice({ ...first, prices: { '1K': { min: 0, max: 0 } } }, '1K')).toBe('0')
  })
  it('groups models by platform and selects an exact identity with keyboard navigation', async () => {
    const host = document.createElement('div'); document.body.append(host)
    const root = createRoot(host), change = vi.fn()
    cleanup = () => { act(() => root.unmount()); host.remove() }
    await act(async () => root.render(<ModelPicker models={[first, second, third]} value="openai_images:image-a" size="1K" disabled={false} onChange={change} />))
    const trigger = host.querySelector<HTMLButtonElement>('button[aria-haspopup="dialog"]')
    if (!trigger) throw new Error('Missing picker trigger')
    await act(async () => trigger.click())

    expect(document.activeElement).toBe(host.querySelector('input[type="search"]'))
    expect(host.querySelectorAll('nav button')).toHaveLength(2)
    const options = host.querySelectorAll<HTMLButtonElement>('[role="option"]')
    expect(options).toHaveLength(2)
    expect(options[0]?.textContent).toContain('1K · 额度 / 张')
    expect(options[1]?.textContent).toContain('价格待配置1K 分辨率')
    expect(options[1]?.textContent).not.toContain('额度 / 张')
    options[0]?.focus()
    await act(async () => options[0]?.dispatchEvent(new KeyboardEvent('keydown', { key: 'ArrowDown', bubbles: true })))
    expect(document.activeElement).toBe(options[1])
    await act(async () => options[1]?.click())
    expect(change).toHaveBeenCalledWith('openai_images:image-b')
    expect(document.activeElement).toBe(trigger)
    expect(host.querySelector('dialog')?.open).toBe(false)
  })
})
