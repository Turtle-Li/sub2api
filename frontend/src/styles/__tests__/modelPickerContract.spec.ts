import fs from 'node:fs'
import path from 'node:path'
import React, { act } from 'react'
import { createRoot } from 'react-dom/client'
import { mount } from '@vue/test-utils'
import { afterEach, describe, expect, it, vi } from 'vitest'
import type { BatchImageModel } from '@/api/batchImage'
import { ModelPicker } from '@/features/image-studio/ModelPicker'
import VideoModelPicker from '@/features/video-studio/VideoModelPicker.vue'
import type { VideoModel } from '@/features/video-studio/api'

vi.stubGlobal('IS_REACT_ACT_ENVIRONMENT', true)

const stylesDir = path.resolve(__dirname, '..')
const featuresDir = path.resolve(__dirname, '../../features')
const sharedPickerCss = fs.readFileSync(path.join(stylesDir, 'model-picker.css'), 'utf8')
const videoStudioCss = fs.readFileSync(path.join(featuresDir, 'video-studio/studio.css'), 'utf8')

const sampleImageModels: BatchImageModel[] = [
  { id: 'gpt-image-2.5', provider: 'openai_images', object: 'model', prices: { '1K': { min: 0.25, max: 0.25 } } },
  { id: 'gpt-image-2.6', provider: 'openai_images', object: 'model', prices: { '1K': { min: 0.5, max: 0.5 } } },
]

const sampleVideoModels: VideoModel[] = [
  {
    id: 'minimax/h3',
    label: 'H3',
    provider: 'goodput',
    min_seconds: 4,
    max_seconds: 15,
    default_seconds: 5,
    sizes: ['1344x768', '768x1344'],
    supports_frames: true,
    supports_seed: false,
    max_references: 2,
    quotes: { 5: 1.25 },
    currency: 'USD',
    quote_id: 'a'.repeat(64),
  },
  {
    id: 'minimax/h3-fast',
    label: 'H3 Fast',
    provider: 'goodput',
    min_seconds: 5,
    max_seconds: 14,
    default_seconds: 5,
    sizes: ['1344x768'],
    supports_frames: true,
    supports_seed: true,
    max_references: 2,
    quotes: { 5: 0.75 },
    currency: 'USD',
    quote_id: 'b'.repeat(64),
  },
]

let cleanup = () => {}
afterEach(() => {
  cleanup()
  cleanup = () => {}
  document.body.replaceChildren()
})

describe('shared model picker dialog contract (React + Vue)', () => {
  it('disconnects VideoModelPicker footer actions from generic .vs-actions/.vs-button/.vs-primary classes and aligns with shared .mp-* structure', async () => {
    Object.defineProperties(HTMLDialogElement.prototype, {
      showModal: { configurable: true, value() { this.setAttribute('open', '') } },
      close: {
        configurable: true,
        value() {
          this.removeAttribute('open')
          this.dispatchEvent(new Event('close'))
        },
      },
    })

    const imageHost = document.createElement('div')
    document.body.append(imageHost)
    const reactRoot = createRoot(imageHost)
    const videoWrapper = mount(VideoModelPicker, {
      props: { models: sampleVideoModels, mobile: true },
      attachTo: document.body,
    })
    cleanup = () => {
      act(() => reactRoot.unmount())
      videoWrapper.unmount()
      imageHost.remove()
    }

    await act(async () => {
      reactRoot.render(
        React.createElement(ModelPicker, {
          models: sampleImageModels,
          value: 'openai_images:gpt-image-2.5',
          size: '1K',
          disabled: false,
          onChange: vi.fn(),
        }),
      )
    })

    const imageDialog = imageHost.querySelector('dialog.mp-dialog')!
    const videoDialog = videoWrapper.element as HTMLDialogElement
    expect(imageDialog).not.toBeNull()
    expect(videoDialog.classList.contains('mp-dialog')).toBe(true)

    const imageActions = imageDialog.querySelector('.mp-foot-actions')!
    const videoActions = videoDialog.querySelector('.mp-foot-actions')!
    expect(imageActions).not.toBeNull()
    expect(videoActions).not.toBeNull()

    // Generic .vs-actions must not race with shared mobile footer grid
    expect(videoActions.classList.contains('vs-actions')).toBe(false)
    expect(videoActions.classList.contains('vs-model-dialog-actions')).toBe(true)

    // Both pickers expose the identical shared button hooks in cancel-then-confirm order
    for (const actions of [imageActions, videoActions]) {
      const buttons = Array.from(actions.querySelectorAll('button'))
      expect(buttons).toHaveLength(2)
      expect(buttons[0]?.classList.contains('mp-btn-cancel')).toBe(true)
      expect(buttons[1]?.classList.contains('mp-btn-confirm')).toBe(true)
    }
  })

  it('scopes video-studio dialog backdrop blur away from .mp-dialog and uses shared light/dark scrim tokens without blur', () => {
    // Video studio backdrop rule must exclude .mp-dialog so delivery/composer sheets keep blur(3px) while pickers stay clean
    expect(videoStudioCss).toMatch(/\.video-studio\s+dialog:not\(\.mp-dialog\)::backdrop\s*\{[^}]*backdrop-filter:\s*blur\(3px\)/)
    expect(videoStudioCss).not.toMatch(/\.video-studio\s+dialog::backdrop\s*\{/)

    // Shared model-picker stylesheet defines light/dark scrim tokens + no blur and wires studio themes through tokens
    expect(sharedPickerCss).toMatch(/--mp-scrim-light:\s*#0f172a80/)
    expect(sharedPickerCss).toMatch(/--mp-scrim-dark:\s*#04091499/)
    expect(sharedPickerCss).toMatch(/--mp-scrim-filter:\s*none/)
    expect(sharedPickerCss).toMatch(/dialog\.mp-dialog::backdrop\s*\{[^}]*background:\s*var\(--mp-backdrop\);[^}]*backdrop-filter:\s*var\(--mp-backdrop-filter\);/s)

    // Both desktop and mobile footer actions are scoped under dialog.mp-dialog to avoid stylesheet load-order races
    expect(sharedPickerCss).toMatch(/dialog\.mp-dialog\s+\.mp-foot-actions\s*\{[^}]*display:\s*inline-flex;/s)
    expect(sharedPickerCss).toMatch(/@media\s*\(max-width:\s*820px\)[\s\S]*dialog\.mp-dialog\s+\.mp-foot-actions\s*\{[^}]*display:\s*grid;[^}]*grid-template-columns:\s*minmax\(0,\s*1fr\)\s+minmax\(0,\s*1\.6fr\);/s)
  })

  it('establishes a shared scroll boundary for open .mp-dialog modals including nested mobile composer sheets', () => {
    expect(sharedPickerCss).toMatch(/html:has\(dialog\.mp-dialog\[open\]\)\s*\{[^}]*overflow:\s*hidden;[^}]*overscroll-behavior:\s*none;/s)
    expect(sharedPickerCss).toMatch(/\.studio-composer:has\(dialog\.mp-dialog\[open\]\)\s*\{[^}]*overflow:\s*hidden;/s)
    expect(sharedPickerCss).toMatch(/dialog\.mp-dialog\s*\{[^}]*overscroll-behavior:\s*contain;/s)
    expect(sharedPickerCss).toMatch(/\.mp-list\s*\{[^}]*overscroll-behavior:\s*contain;/s)
    expect(sharedPickerCss).toMatch(/\.mp-detail\s*\{[^}]*overscroll-behavior:\s*contain;/s)
    expect(sharedPickerCss).toMatch(/@media\s*\(max-width:\s*820px\)[\s\S]*\.mp-layout\s*\{[^}]*overflow-y:\s*auto;[^}]*overscroll-behavior:\s*contain;/s)
  })

  it('applies shared per-model-family data-model-theme styling and vector marks across both React and Vue pickers without changing prices or display names', async () => {
    Object.defineProperties(HTMLDialogElement.prototype, {
      showModal: { configurable: true, value() { this.setAttribute('open', '') } },
      close: {
        configurable: true,
        value() {
          this.removeAttribute('open')
          this.dispatchEvent(new Event('close'))
        },
      },
    })

    const themedImageModels: BatchImageModel[] = [
      { id: 'gpt-image-2.5', provider: 'openai_images', object: 'model', prices: { '1K': { min: 0.25, max: 0.25 } } },
      { id: 'gemini-3-pro-image', provider: 'vertex', object: 'model', prices: { '1K': { min: 0.12, max: 0.12 } } },
    ]
    const themedVideoModels: VideoModel[] = [
      {
        id: 'muse-video',
        label: 'Muse',
        provider: 'muse',
        duration_mode: 'automatic',
        min_seconds: 6,
        max_seconds: 6,
        default_seconds: 6,
        sizes: ['16:9'],
        supports_frames: false,
        supports_seed: false,
        max_references: 10,
        quotes: { 6: 1.5 },
        currency: 'CNY',
        quote_id: 'c'.repeat(64),
      },
      ...sampleVideoModels,
    ]

    const imageHost = document.createElement('div')
    document.body.append(imageHost)
    const reactRoot = createRoot(imageHost)
    const videoWrapper = mount(VideoModelPicker, {
      props: { models: themedVideoModels, mobile: false },
      attachTo: document.body,
    })
    cleanup = () => {
      act(() => reactRoot.unmount())
      videoWrapper.unmount()
      imageHost.remove()
    }

    await act(async () => {
      reactRoot.render(
        React.createElement(ModelPicker, {
          models: themedImageModels,
          value: 'vertex:gemini-3-pro-image',
          size: '1K',
          disabled: false,
          onChange: vi.fn(),
        }),
      )
    })

    const imageTrigger = imageHost.querySelector<HTMLButtonElement>('button.mp-trigger')!
    expect(imageTrigger.getAttribute('data-model-theme')).toBe('gemini')
    expect(imageTrigger.querySelector('.mp-trigger-mark svg')).not.toBeNull()

    await act(async () => imageTrigger.click())
    const imageOption = imageHost.querySelector<HTMLButtonElement>('[role="option"]')!
    expect(imageOption.getAttribute('data-model-theme')).toBe('gemini')
    expect(imageOption.querySelector('.mp-option-mark svg')).not.toBeNull()
    expect(imageOption.querySelector('strong')?.textContent).toBe('gemini-3-pro-image')
    // Lightweight default state: detail popover is collapsed until '?' is activated, and '?' is never nested inside option
    expect(imageHost.querySelector('.mp-detail')).toBeNull()
    expect(imageOption.querySelector('.mp-info-btn')).toBeNull()
    const imageInfoBtn = imageHost.querySelector<HTMLButtonElement>('button.mp-info-btn')!
    expect(imageInfoBtn).not.toBeNull()
    await act(async () => {
      imageInfoBtn.dispatchEvent(Object.assign(new MouseEvent('pointerover', { bubbles: true }), { pointerType: 'touch' }))
      imageInfoBtn.dispatchEvent(new MouseEvent('mouseover', { bubbles: true }))
    })
    expect(imageHost.querySelector('.mp-detail')).toBeNull()
    await act(async () => {
      imageInfoBtn.dispatchEvent(Object.assign(new MouseEvent('pointerover', { bubbles: true }), { pointerType: 'mouse' }))
    })
    expect(imageHost.querySelector('.mp-detail')?.getAttribute('data-model-theme')).toBe('gemini')
    await act(async () => {
      imageInfoBtn.dispatchEvent(Object.assign(new MouseEvent('pointerout', { bubbles: true }), { pointerType: 'mouse' }))
    })
    expect(imageHost.querySelector('.mp-detail')).toBeNull()
    await act(async () => { imageInfoBtn.focus() })
    expect(imageHost.querySelector('.mp-detail')?.getAttribute('data-model-theme')).toBe('gemini')
    await act(async () => { imageInfoBtn.blur() })
    expect(imageHost.querySelector('.mp-detail')).toBeNull()
    // Click/tap pins detail open without changing selection, Escape dismisses only the popover
    await act(async () => imageInfoBtn.click())
    expect(imageHost.querySelector('.mp-detail')?.getAttribute('data-model-theme')).toBe('gemini')
    await act(async () => {
      imageHost.querySelector('dialog.mp-dialog')?.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true, cancelable: true }))
    })
    expect(imageHost.querySelector('.mp-detail')).toBeNull()
    expect(imageHost.querySelector('dialog.mp-dialog')?.hasAttribute('open')).toBe(true)

    const videoOpener = document.createElement('button')
    document.body.append(videoOpener)
    await (videoWrapper.vm as InstanceType<typeof VideoModelPicker>).open(themedVideoModels[0]!, videoOpener)
    const videoOptions = videoWrapper.findAll('[role="option"]')
    expect(videoOptions.map(node => node.attributes('data-model-theme'))).toEqual(['muse', 'minimax', 'h3-fast'])
    expect(videoOptions[0]?.find('.mp-option-mark svg').exists()).toBe(true)
    expect(videoOptions[0]?.find('strong').text()).toBe('Muse')
    expect(videoWrapper.find('.mp-detail').exists()).toBe(false)
    expect(videoOptions[0]?.find('.mp-info-btn').exists()).toBe(false)
    const videoInfoBtns = videoWrapper.findAll('button.mp-info-btn')
    expect(videoInfoBtns).toHaveLength(3)
    const videoHover = videoWrapper.findAll('.mp-info-wrap')[1]
    expect(videoHover).toBeDefined()
    await videoHover?.trigger('pointerenter', { pointerType: 'touch' })
    await videoHover?.trigger('mouseenter')
    expect(videoWrapper.find('.mp-detail').exists()).toBe(false)
    await videoHover?.trigger('pointerenter', { pointerType: 'mouse' })
    expect(videoWrapper.find('.mp-detail').attributes('data-model-theme')).toBe('minimax')
    expect(videoOptions[0]?.attributes('aria-selected')).toBe('true')
    await videoHover?.trigger('pointerleave', { pointerType: 'mouse' })
    expect(videoWrapper.find('.mp-detail').exists()).toBe(false)
    // Click/tap pins popover for current model without changing selection
    await videoInfoBtns[0]!.trigger('click')
    expect(videoWrapper.find('.mp-detail').attributes('data-model-theme')).toBe('muse')
    await videoInfoBtns[0]!.trigger('click')
    expect(videoWrapper.find('.mp-detail').exists()).toBe(false)

    for (const theme of ['gemini', 'openai', 'seedream', 'wan', 'muse', 'minimax', 'h3-fast', 'hunyuan']) {
      expect(sharedPickerCss).toContain(`[data-model-theme="${theme}"]`)
      expect(sharedPickerCss).toContain(`.dark :is(.mp-trigger, .mp-option, .mp-detail, .mp-brand-btn)[data-model-theme="${theme}"]`)
    }
  })
})
