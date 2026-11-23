import React, { act } from 'react'
import { createRoot } from 'react-dom/client'
import { afterEach, describe, expect, it, vi } from 'vitest'
import type { BatchImageModel } from '@/api/batchImage'
import { ModelPicker, modelBadge, modelPrice } from '../ModelPicker'

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
    expect(Array.from(host.querySelectorAll('nav button small')).map(count => Number(count.textContent)).sort()).toEqual([0, 0, 1, 2])
    const options = host.querySelectorAll<HTMLButtonElement>('[role="option"]')
    expect(options).toHaveLength(2)
    expect(options[0]?.textContent).toContain('当前')
    expect(options[0]?.textContent).toContain('1K · 额度 / 张')
    expect(options[1]?.textContent).toContain('价格待配置1K 分辨率')
    expect(options[1]?.textContent).not.toContain('额度 / 张')
    options[0]?.focus()
    await act(async () => options[0]?.dispatchEvent(new KeyboardEvent('keydown', { key: 'ArrowDown', bubbles: true })))
    expect(document.activeElement).toBe(options[1])
    expect(host.querySelector('.studio-model-detail')).toBeNull()
    const infoButtons = host.querySelectorAll<HTMLButtonElement>('button.mp-info-btn')
    expect(infoButtons).toHaveLength(2)
    await act(async () => { infoButtons[1]?.focus() })
    expect(host.querySelector('.studio-model-detail')?.textContent).toContain('候选预览')
    await act(async () => { infoButtons[1]?.blur() })
    expect(host.querySelector('.studio-model-detail')).toBeNull()
    expect(change).not.toHaveBeenCalled()
    await act(async () => host.querySelector<HTMLButtonElement>('.studio-picker-cancel')?.click())
    expect(change).not.toHaveBeenCalled()
    expect(document.activeElement).toBe(trigger)
    expect(host.querySelector('dialog')?.open).toBe(false)

    await act(async () => trigger.click())
    const reopenedOptions = host.querySelectorAll<HTMLButtonElement>('[role="option"]')
    await act(async () => reopenedOptions[1]?.click())
    expect(change).not.toHaveBeenCalled()
    await act(async () => host.querySelector<HTMLButtonElement>('.studio-picker-confirm')?.click())
    expect(change).toHaveBeenCalledWith('openai_images:image-b')
    expect(document.activeElement).toBe(trigger)
    expect(host.querySelector('dialog')?.open).toBe(false)
  })
  it('moves focus from the search box into candidate options on ArrowDown and applies on Enter', async () => {
    const host = document.createElement('div'); document.body.append(host)
    const root = createRoot(host), change = vi.fn()
    cleanup = () => { act(() => root.unmount()); host.remove() }
    await act(async () => root.render(<ModelPicker models={[first, second, third]} value="openai_images:image-a" size="1K" disabled={false} onChange={change} />))
    const trigger = host.querySelector<HTMLButtonElement>('button[aria-haspopup="dialog"]')!
    await act(async () => trigger.click())

    const search = host.querySelector<HTMLInputElement>('input[type="search"]')!
    expect(document.activeElement).toBe(search)
    await act(async () => search.dispatchEvent(new KeyboardEvent('keydown', { key: 'ArrowDown', bubbles: true })))
    const options = host.querySelectorAll<HTMLButtonElement>('[role="option"]')
    expect(document.activeElement).toBe(options[1])
    expect(options[1]?.getAttribute('aria-selected')).toBe('true')
    expect(change).not.toHaveBeenCalled()

    await act(async () => options[1]?.dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', bubbles: true })))
    expect(change).toHaveBeenCalledWith('openai_images:image-b')
    expect(document.activeElement).toBe(trigger)
    expect(host.querySelector('dialog')?.open).toBe(false)
  })
  it('formats platform marks and concise model short names for gallery cards', () => {
    expect(modelBadge('vertex', 'gemini-3.1-pro-image')).toMatchObject({ key: 'gemini', mark: 'G', label: '3.1 Pro' })
    expect(modelBadge('gemini_api', 'gemini-3.1-flash-lite-image')).toMatchObject({ key: 'gemini', mark: 'G', label: '3.1 Lite' })
    expect(modelBadge('openai_images', 'gpt-image-2.5')).toMatchObject({ key: 'openai', mark: 'O', label: 'GPT 2.5' })
    expect(modelBadge('seedream_images', 'doubao-seedream-5-0-pro-260628')).toMatchObject({ key: 'seedream', mark: 'S', label: '5.0 Pro' })
    expect(modelBadge('wan_images', 'wan2.7-image-pro')).toMatchObject({ key: 'wan', mark: 'W', label: '2.7 Pro' })
    expect(modelBadge('muse_images', 'muse-image')).toMatchObject({ key: 'muse', mark: 'M', label: 'Muse' })
    expect(modelBadge('workbuddy_images', 'cn:hunyuan-image-alpha-edit')).toMatchObject({ key: 'hunyuan', mark: 'H', label: 'Alpha Edit' })
  })
  it('uses one consistent model display name across trigger, option list, and detail pane', async () => {
    const long: BatchImageModel = {
      id: 'doubao-seedream-5-0-pro-260628',
      provider: 'seedream_images',
      object: 'model',
      capabilities: {
        image_sizes: ['1K', '2K'],
        aspect_ratios: ['1:1', '16:9'],
        default_image_size: '1K',
        default_aspect_ratio: '1:1',
        max_reference_images: 4,
        response_mime_types: ['image/png'],
      },
      prices: { '1K': { min: 0.32, max: 0.32 } },
    }
    const host = document.createElement('div'); document.body.append(host)
    const root = createRoot(host)
    cleanup = () => { act(() => root.unmount()); host.remove() }
    await act(async () => root.render(<ModelPicker models={[long]} value="seedream_images:doubao-seedream-5-0-pro-260628" size="1K" disabled={false} onChange={vi.fn()} />))
    const trigger = host.querySelector<HTMLButtonElement>('button[aria-label="图片模型"]')!
    expect(trigger.querySelector('.studio-model-mark')?.textContent).toBe('S')
    expect(trigger.querySelector('.studio-model-copy strong')?.textContent).toBe('doubao-seedream-5-0-pro-260628')
    expect(trigger.querySelector('.studio-model-copy small')?.textContent).toBe('豆包 Seedream · 1K/2K · 最多 4 张参考图')
    expect(trigger.title).toBe('doubao-seedream-5-0-pro-260628')

    await act(async () => trigger.click())
    const option = host.querySelector<HTMLButtonElement>('[role="option"]')!
    expect(option.querySelector('strong')?.textContent).toBe('doubao-seedream-5-0-pro-260628')
    expect(option.title).toBe('doubao-seedream-5-0-pro-260628')
    expect(option.getAttribute('aria-label')).toContain('doubao-seedream-5-0-pro-260628')
    expect(host.querySelector('.studio-model-detail')).toBeNull()
    const infoBtn = host.querySelector<HTMLButtonElement>('button.mp-info-btn')!
    await act(async () => infoBtn.click())
    expect(host.querySelector('.studio-model-detail h3')?.textContent).toBe('doubao-seedream-5-0-pro-260628')
    expect(host.textContent).not.toMatch(/线路|路由|API Key|分组|服务渠道/)
  })
  it('keeps distinct provider:id entries separate, differentiates same-name options by price/spec, and omits routing copy', async () => {
    const vertexModel: BatchImageModel = {
      id: 'gemini-3-pro-image',
      provider: 'vertex',
      object: 'model',
      capabilities: {
        image_sizes: ['1K', '2K', '4K'],
        aspect_ratios: ['1:1', '16:9'],
        default_image_size: '1K',
        default_aspect_ratio: '1:1',
        max_reference_images: 14,
        response_mime_types: ['image/png'],
      },
      prices: { '1K': { min: 0.12, max: 0.12 } },
    }
    const apiModel: BatchImageModel = {
      id: 'gemini-3-pro-image',
      provider: 'gemini_api',
      object: 'model',
      capabilities: {
        image_sizes: ['1K', '2K'],
        aspect_ratios: ['1:1'],
        default_image_size: '1K',
        default_aspect_ratio: '1:1',
        max_reference_images: 4,
        response_mime_types: ['image/png'],
      },
      prices: { '1K': { min: 0.28, max: 0.28 } },
    }
    const change = vi.fn()
    const host = document.createElement('div'); document.body.append(host)
    const root = createRoot(host)
    cleanup = () => { act(() => root.unmount()); host.remove() }
    await act(async () => root.render(<ModelPicker models={[vertexModel, apiModel]} value="vertex:gemini-3-pro-image" size="1K" disabled={false} onChange={change} />))
    const trigger = host.querySelector<HTMLButtonElement>('button[aria-label="图片模型"]')!
    expect(trigger.querySelector('.studio-model-copy strong')?.textContent).toBe('gemini-3-pro-image')
    expect(trigger.title).toBe('gemini-3-pro-image')
    await act(async () => trigger.click())
    const options = Array.from(host.querySelectorAll<HTMLButtonElement>('[role="option"]'))
    const infoButtons = Array.from(host.querySelectorAll<HTMLButtonElement>('button.mp-info-btn'))
    expect(options).toHaveLength(2)
    expect(infoButtons).toHaveLength(2)
    expect(options[0]?.textContent).toContain('gemini-3-pro-image')
    expect(options[0]?.textContent).toContain('0.12')
    expect(options[0]?.getAttribute('aria-label')).toContain('1K/2K/4K')
    expect(options[1]?.textContent).toContain('gemini-3-pro-image')
    expect(options[1]?.textContent).toContain('0.28')
    expect(options[1]?.getAttribute('aria-label')).toContain('1K/2K')
    expect(options[0]?.textContent).not.toBe(options[1]?.textContent)

    // Clicking '?' opens full specs in popover without changing draft selection or triggering onChange
    await act(async () => infoButtons[0]?.click())
    expect(host.querySelector('.studio-model-detail')?.textContent).toContain('1K/2K/4K')
    await act(async () => infoButtons[1]?.click())
    expect(host.querySelector('.studio-model-detail')?.textContent).toContain('1K/2K')
    expect(options[0]?.getAttribute('aria-selected')).toBe('true')
    expect(change).not.toHaveBeenCalled()
    expect(host.textContent).not.toMatch(/Google Vertex|vertex|gemini_api|线路 1|线路 2|路由|API Key|分组|服务渠道/)
    expect(options.every(option => !/线路|路由|Vertex|API Key|分组/.test(`${option.title} ${option.getAttribute('aria-label') || ''}`))).toBe(true)

    await act(async () => options[1]?.click())
    await act(async () => host.querySelector<HTMLButtonElement>('.studio-picker-confirm')?.click())
    expect(change).toHaveBeenCalledWith('gemini_api:gemini-3-pro-image')
  })

  it('uniformly enables stacked-price row layout when any visible option has a long price', async () => {
    const shortModel: BatchImageModel = {
      id: '中文基础生图模型',
      provider: 'openai_images',
      object: 'model',
      prices: { '1K': { min: 0.25, max: 0.25 } },
    }
    const longPriceModel: BatchImageModel = {
      id: '超长中文高清商业摄影级图像生成大模型旗舰增强版',
      provider: 'openai_images',
      object: 'model',
      prices: { '1K': { min: 1234.5678, max: 9876.5432 } },
    }
    const host = document.createElement('div'); document.body.append(host)
    const root = createRoot(host)
    cleanup = () => { act(() => root.unmount()); host.remove() }
    await act(async () => root.render(<ModelPicker models={[shortModel, longPriceModel]} value="openai_images:中文基础生图模型" size="1K" disabled={false} onChange={vi.fn()} />))
    await act(async () => host.querySelector<HTMLButtonElement>('button[aria-label="图片模型"]')?.click())

    const list = host.querySelector('.mp-list')
    expect(list?.classList.contains('is-stacked-price')).toBe(true)
    const options = Array.from(host.querySelectorAll<HTMLButtonElement>('[role="option"]'))
    expect(options).toHaveLength(2)
    expect(options[1]?.title).toBe(longPriceModel.id)
    expect(options[1]?.getAttribute('aria-label')).toContain(longPriceModel.id)
    expect(options[1]?.getAttribute('aria-label')).toContain('1,234.5678–9,876.5432')
  })

  it('shows missing native platforms without inventing selectable models', async () => {
    const change = vi.fn()
    const host = document.createElement('div'); document.body.append(host)
    const root = createRoot(host)
    cleanup = () => { act(() => root.unmount()); host.remove() }
    await act(async () => root.render(<ModelPicker models={[]} value="" size="1K" disabled={false} onChange={change} />))
    await act(async () => host.querySelector<HTMLButtonElement>('button[aria-label="图片模型"]')?.click())
    const platforms = Array.from(host.querySelectorAll<HTMLButtonElement>('nav[aria-label="模型品牌"] button'))
    expect(platforms.map(button => button.textContent)).toEqual(expect.arrayContaining(['通义万相0', '豆包 Seedream0']))
    await act(async () => platforms.find(button => button.textContent?.includes('Seedream'))?.click())
    expect(host.textContent).toContain('确认工作台的模型和生图权限')
    expect(host.querySelector('a[href="/keys"]')).toBeNull()
    expect(host.querySelector('[role="option"]')).toBeNull()
    expect(change).not.toHaveBeenCalled()

    const seedream: BatchImageModel = { id: 'doubao-seedream-5-0-lite-260128', provider: 'seedream_images', object: 'model', prices: { '2K': { min: .32, max: .32 } } }
    await act(async () => root.render(<ModelPicker models={[seedream]} value="" size="2K" disabled={false} onChange={change} />))
    const option = host.querySelector<HTMLButtonElement>('[role="option"]')!
    expect(option.textContent).toContain(seedream.id)
    expect(host.querySelector('a[href="/keys"]')).toBeNull()
    await act(async () => option.click())
    await act(async () => host.querySelector<HTMLButtonElement>('.studio-picker-confirm')?.click())
    expect(change).toHaveBeenCalledOnce()
    expect(change).toHaveBeenCalledWith(`seedream_images:${seedream.id}`)
  })
})
