import React, { act } from 'react'
import { createRoot } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import Lightbox from '../upstream/components/Lightbox'

vi.stubGlobal('IS_REACT_ACT_ENVIRONMENT', true)
let host: HTMLDivElement
let root: ReturnType<typeof createRoot>
let close: ReturnType<typeof vi.fn>
let next: ReturnType<typeof vi.fn>
let previous: ReturnType<typeof vi.fn>
beforeEach(() => {
 host = document.createElement('div'); document.body.append(host); root = createRoot(host)
 close = vi.fn(); next = vi.fn(); previous = vi.fn()
})
afterEach(() => { act(() => root.unmount()); host.remove(); vi.useRealTimers() })
function render() {
 act(() => root.render(<Lightbox src="data:image/png;base64,AA==" imageId="one" currentIndex={0} total={2} showNav onClose={close} onPrev={previous} onNext={next} onDownload={vi.fn()} onEdit={vi.fn()} />))
}
describe('adopted lightbox lifecycle and gestures', () => {
 it('locks the background, navigates by keyboard, and restores the scroll lock on unmount', () => {
   render()
   expect(document.body.style.overflow).toBe('hidden')
   act(() => window.dispatchEvent(new KeyboardEvent('keydown', { key: 'ArrowRight' })))
   expect(next).toHaveBeenCalledOnce()
   act(() => window.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape' })))
   expect(close).toHaveBeenCalledOnce()
   act(() => root.render(null))
   expect(document.body.style.overflow).not.toBe('hidden')
   act(() => window.dispatchEvent(new KeyboardEvent('keydown', { key: 'ArrowRight' })))
   expect(next).toHaveBeenCalledOnce()
 })
 it('zooms on wheel without closing and resets when the selected image changes', () => {
   render()
   const overlay = host.querySelector('[data-lightbox-root]')!
   act(() => overlay.dispatchEvent(new WheelEvent('wheel', { deltaY: -100, bubbles: true, cancelable: true })))
   expect(host.textContent).toContain('115%')
   expect(host.querySelector('img')?.parentElement?.style.transform).toContain('scale(1.15)')
   expect(close).not.toHaveBeenCalled()
   act(() => root.render(<Lightbox src="data:image/png;base64,BB==" imageId="two" currentIndex={1} total={2} showNav onClose={close} onPrev={previous} onNext={next} onDownload={vi.fn()} onEdit={vi.fn()} />))
   expect(host.querySelector('img')?.parentElement?.style.transform).toContain('scale(1)')
 })
 it('distinguishes horizontal swipes from clicks and invokes the real navigation callback', () => {
   render()
   const image = host.querySelector('img')!
   const touch = (type: string, x: number, ended = false) => {
     const event = new Event(type, { bubbles: true, cancelable: true })
     Object.defineProperties(event, { touches: { value: ended ? [] : [{ clientX: x, clientY: 100 }] }, changedTouches: { value: [{ clientX: x, clientY: 100 }] } })
     act(() => image.dispatchEvent(event))
   }
   touch('touchstart', 200); touch('touchmove', 100); touch('touchend', 100, true)
   expect(next).toHaveBeenCalledOnce(); expect(close).not.toHaveBeenCalled()
 })
 it('uses the original two-finger distance ratio for pinch zoom', () => {
   render()
   const image = host.querySelector('img')!
   const gesture = (type: string, distance: number) => {
     const event = new Event(type, { bubbles: true, cancelable: true })
     Object.defineProperty(event, 'touches', { value: [{ clientX: 0, clientY: 100 }, { clientX: distance, clientY: 100 }] })
     act(() => image.dispatchEvent(event))
   }
   gesture('touchstart', 100); gesture('touchmove', 200)
   expect(image.parentElement?.style.transform).toContain('scale(2)')
   expect(host.textContent).toContain('200%')
   expect(close).not.toHaveBeenCalled()
 })

})
