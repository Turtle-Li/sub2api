import React, { act } from 'react'
import { createRoot } from 'react-dom/client'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { DetailModal } from '../DetailModal'
import type { StudioTask } from '../api'
import type { workspaceAPI } from '../workspaceAPI'

vi.stubGlobal('IS_REACT_ACT_ENVIRONMENT', true)
HTMLDialogElement.prototype.showModal = vi.fn(function (this: HTMLDialogElement) {
  this.setAttribute('open', '')
})
HTMLDialogElement.prototype.close = vi.fn(function (this: HTMLDialogElement) {
  this.removeAttribute('open')
})
let objectUrls = 0
URL.createObjectURL = vi.fn(() => `blob:detail-${++objectUrls}`)
URL.revokeObjectURL = vi.fn()

type API = ReturnType<typeof workspaceAPI>
let cleanup = () => {}

afterEach(() => {
  cleanup()
  cleanup = () => {}
  objectUrls = 0
  vi.clearAllMocks()
})

function makeTask(overrides?: {
  job?: Partial<StudioTask['job']>
  item?: Partial<StudioTask['item']>
  knownOutputGone?: true
}): StudioTask {
  return {
    id: 'job-1/item-1',
    job: {
      id: 'job-1',
      status: 'completed',
      model: 'gpt-image-2.6-flare',
      provider: 'openai_images',
      created_at: 1764000000,
      fail_count: 0,
      actual_cost: 0.2,
      hold_amount: 0.2,
      output_expires_at: Math.floor(Date.now() / 1000) + 86400,
      ...overrides?.job,
    },
    item: {
      custom_id: 'item-1',
      status: 'succeeded',
      image_count: 1,
      prompt_preview: '雾蓝海面上的红色纸船，竖构图',
      ...overrides?.item,
    },
    ...(overrides?.knownOutputGone ? { knownOutputGone: true } : {}),
  } as StudioTask
}

function makeAPI(overrides?: Partial<Pick<API, 'image' | 'archivePreview'>>): API {
  return {
    image: vi.fn(async () => new Blob(['img'], { type: 'image/png' })),
    archivePreview: vi.fn(),
    ...overrides,
  } as unknown as API
}

function mount() {
  const host = document.createElement('div')
  document.body.append(host)
  const root = createRoot(host)
  cleanup = () => {
    act(() => root.unmount())
    host.remove()
  }
  return { host, root }
}

describe('DetailModal', () => {
  it('closes only Lightbox on first Escape, then closes detail and restores opener focus on second Escape', async () => {
    const opener = document.createElement('button')
    opener.textContent = 'open card'
    document.body.append(opener)
    opener.focus()

    const close = vi.fn()
    const api = makeAPI()
    const task = makeTask({ item: { preview_size: { width: 533, height: 800 } } })
    const { host, root } = mount()
    const prevCleanup = cleanup
    cleanup = () => { prevCleanup(); opener.remove() }

    await act(async () => {
      root.render(<DetailModal task={task} api={api} close={close} edit={vi.fn()} cancel={vi.fn()} />)
    })

    const dialog = host.querySelector<HTMLDialogElement>('dialog[aria-label="任务详情"]')!
    expect(dialog.dataset.layout).toBe('portrait')
    expect(Number(dialog.style.getPropertyValue('--detail-aspect'))).toBeCloseTo(533 / 800)

    const zoomButton = dialog.querySelector<HTMLButtonElement>('button[aria-label="放大图片"]')!
    await act(async () => zoomButton.click())
    expect(host.querySelector('[data-lightbox-root]')).not.toBeNull()

    // First Escape closes only Lightbox
    await act(async () => {
      window.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', cancelable: true }))
    })
    expect(host.querySelector('[data-lightbox-root]')).toBeNull()
    expect(close).not.toHaveBeenCalled()
    expect(host.querySelector('dialog[aria-label="任务详情"]')).not.toBeNull()

    // Second Escape closes DetailModal and restores focus to opener
    await act(async () => {
      window.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', cancelable: true }))
    })
    expect(close).toHaveBeenCalledOnce()
    expect(document.activeElement).toBe(opener)
  })

  it('guards native dialog cancel while Lightbox is open so only Lightbox dismisses', async () => {
    const close = vi.fn()
    const api = makeAPI()
    const { host, root } = mount()
    await act(async () => {
      root.render(<DetailModal task={makeTask()} api={api} close={close} edit={vi.fn()} cancel={vi.fn()} />)
    })

    const dialog = host.querySelector<HTMLDialogElement>('dialog[aria-label="任务详情"]')!
    await act(async () => dialog.querySelector<HTMLButtonElement>('button[aria-label="放大图片"]')!.click())
    expect(host.querySelector('[data-lightbox-root]')).not.toBeNull()

    await act(async () => {
      dialog.dispatchEvent(new Event('cancel', { cancelable: true }))
    })
    expect(host.querySelector('[data-lightbox-root]')).toBeNull()
    expect(close).not.toHaveBeenCalled()
  })

  it('remembers decoded aspect ratios when switching across multiple images', async () => {
    const api = makeAPI()
    const task = makeTask({ item: { image_count: 2, preview_size: { width: 600, height: 900 } } })
    const { host, root } = mount()
    await act(async () => {
      root.render(<DetailModal task={task} api={api} close={vi.fn()} edit={vi.fn()} cancel={vi.fn()} />)
    })

    const dialog = host.querySelector<HTMLDialogElement>('dialog[aria-label="任务详情"]')!
    const decode = async (width: number, height: number) => {
      const img = dialog.querySelector<HTMLImageElement>('.studio-detail-image img')!
      Object.defineProperties(img, { naturalWidth: { value: width }, naturalHeight: { value: height } })
      await act(async () => img.dispatchEvent(new Event('load')))
    }

    await decode(800, 1200)
    expect(dialog.dataset.layout).toBe('portrait')
    expect(Number(dialog.style.getPropertyValue('--detail-aspect'))).toBeCloseTo(2 / 3)
    // A historical job without a recorded tier must not invent one from its pixels, while decoded pixels remain explicit.
    expect(dialog.querySelector('.studio-detail-facts')?.textContent).not.toContain('分辨率档位')
    expect(dialog.querySelector('.studio-detail-facts')?.textContent).toContain('实际像素800 × 1200')

    await act(async () => dialog.querySelector<HTMLButtonElement>('button[aria-label="下一张"]')!.click())
    await decode(1600, 900)
    expect(dialog.dataset.layout).toBe('landscape')
    expect(Number(dialog.style.getPropertyValue('--detail-aspect'))).toBeCloseTo(16 / 9)

    await act(async () => dialog.querySelector<HTMLButtonElement>('button[aria-label="上一张"]')!.click())
    expect(dialog.dataset.layout).toBe('portrait')
    expect(Number(dialog.style.getPropertyValue('--detail-aspect'))).toBeCloseTo(2 / 3)
  })

  it('renders a structured danger callout for failed items without broken images or image actions', async () => {
    const api = makeAPI()
    const failed = makeTask({
      item: {
        status: 'failed',
        image_count: 0,
        error: { code: 'PROVIDER_ITEM_FAILED', message: 'upstream failed' },
      },
    })
    const { host, root } = mount()
    await act(async () => {
      root.render(<DetailModal task={failed} api={api} close={vi.fn()} edit={vi.fn()} cancel={vi.fn()} />)
    })

    const dialog = host.querySelector<HTMLDialogElement>('dialog[aria-label="任务详情"]')!
    expect(dialog.dataset.layout).toBe('record')
    expect(dialog.querySelector('img')).toBeNull()
    expect(dialog.querySelector('.studio-detail-actions')).toBeNull()
    expect(dialog.querySelector('.studio-detail-badge')?.getAttribute('data-tone')).toBe('danger')
    expect(dialog.querySelector('.studio-detail-callout')?.getAttribute('data-tone')).toBe('danger')
    expect(dialog.textContent).toContain('生成服务未能交付可用图片。')
    expect(dialog.textContent).toContain('PROVIDER_ITEM_FAILED')
    expect(api.image).not.toHaveBeenCalled()
  })

  it('renders expired records without broken images, actions, or original reads', async () => {
    const api = makeAPI()
    const expired = makeTask({
      job: { output_expires_at: 1 },
      knownOutputGone: true,
    })
    const { host, root } = mount()
    await act(async () => {
      root.render(<DetailModal task={expired} api={api} close={vi.fn()} edit={vi.fn()} cancel={vi.fn()} />)
    })

    const dialog = host.querySelector<HTMLDialogElement>('dialog[aria-label="任务详情"]')!
    expect(dialog.dataset.layout).toBe('record')
    expect(dialog.querySelector('img')).toBeNull()
    expect(dialog.querySelector('.studio-detail-actions')).toBeNull()
    expect(dialog.querySelector('.studio-detail-badge')?.getAttribute('data-tone')).toBe('muted')
    expect(dialog.querySelector('.studio-detail-callout')?.getAttribute('data-tone')).toBe('neutral')
    expect(dialog.textContent).toContain('图片已过期')
    expect(api.image).not.toHaveBeenCalled()
  })

  it('reads archives only on demand and adapts layout once decoded', async () => {
    const archivePreview = vi.fn(async () => ({
      custom_id: 'item-1',
      status: 'success',
      image_count: 1,
      images: [new Blob(['archive'], { type: 'image/png' })],
    }))
    const api = makeAPI({ archivePreview })
    const archived = makeTask({
      job: { provider: 'vertex' },
      item: { status: 'result_available', image_count: 0 },
    })
    const { host, root } = mount()
    await act(async () => {
      root.render(<DetailModal task={archived} api={api} close={vi.fn()} edit={vi.fn()} cancel={vi.fn()} />)
    })

    const dialog = host.querySelector<HTMLDialogElement>('dialog[aria-label="任务详情"]')!
    expect(dialog.dataset.layout).toBe('record')
    expect(dialog.querySelector('img')).toBeNull()
    expect(archivePreview).not.toHaveBeenCalled()

    const openBtn = Array.from(dialog.querySelectorAll('button')).find(btn => btn.textContent === '打开归档结果')!
    await act(async () => openBtn.click())
    expect(archivePreview).toHaveBeenCalledOnce()

    const img = dialog.querySelector<HTMLImageElement>('.studio-detail-image img')!
    Object.defineProperties(img, { naturalWidth: { value: 720 }, naturalHeight: { value: 1280 } })
    await act(async () => img.dispatchEvent(new Event('load')))
    expect(dialog.dataset.layout).toBe('portrait')
    expect(Number(dialog.style.getPropertyValue('--detail-aspect'))).toBeCloseTo(720 / 1280)
    expect(api.image).not.toHaveBeenCalled()
  })

  it('steps multi-image index with ArrowLeft/ArrowRight and does not dismiss DetailModal on Escape while a topmost external dialog (AddToCanvasDialog) is open', async () => {
    const close = vi.fn()
    const api = makeAPI()
    const task = makeTask({ item: { image_count: 2 } })
    const { host, root } = mount()

    const externalDialog = document.createElement('dialog')
    externalDialog.className = 'cc-add-dialog'
    document.body.append(externalDialog)
    const prevCleanup = cleanup
    cleanup = () => {
      prevCleanup()
      externalDialog.remove()
    }

    await act(async () => {
      root.render(<DetailModal task={task} api={api} close={close} edit={vi.fn()} cancel={vi.fn()} />)
    })

    const dialog = host.querySelector<HTMLDialogElement>('dialog[aria-label="任务详情"]')!
    expect(dialog.textContent).toContain('1 / 2')

    await act(async () => {
      dialog.dispatchEvent(new KeyboardEvent('keydown', { key: 'ArrowRight', bubbles: true, cancelable: true }))
    })
    expect(dialog.textContent).toContain('2 / 2')

    // Simulate AddToCanvasDialog opened above DetailModal
    externalDialog.showModal()
    await act(async () => {
      window.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', cancelable: true }))
    })
    expect(close).not.toHaveBeenCalled()

    // Once topmost external dialog closes, Escape dismisses DetailModal
    externalDialog.close()
    await act(async () => {
      window.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', cancelable: true }))
    })
    expect(close).toHaveBeenCalledOnce()
  })
})
