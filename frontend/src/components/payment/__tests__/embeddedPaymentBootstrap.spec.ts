import { runInNewContext } from 'node:vm'
import { describe, expect, it, vi } from 'vitest'
import html from '../../../../index.html?raw'

const bootstrap = html.match(/<script id="embedded-payment-return"[^>]*>([\s\S]*?)<\/script>/)?.[1]

describe('embedded payment first paint', () => {
  it.each(['/payment/result', '/payment/result/'])('hides the result and sends a hint before app initialization: %s', pathname => {
    expect(bootstrap).toBeTruthy()
    const postMessage = vi.fn()
    const document = { documentElement: { hidden: false } }
    runInNewContext(bootstrap!, { document, window: { parent: { postMessage }, location: { pathname, origin: 'https://shop.example.invalid' } } })
    expect(document.documentElement.hidden).toBe(true)
    expect(postMessage).toHaveBeenCalledWith({ type: 'sub2api:payment-return-ready' }, 'https://shop.example.invalid')
  })
  it.each(['/payment', '/shop', '/payment/result-other'])('does not hide an unrelated frame: %s', pathname => {
    const postMessage = vi.fn()
    const document = { documentElement: { hidden: false } }
    runInNewContext(bootstrap!, { document, window: { parent: { postMessage }, location: { pathname, origin: 'https://shop.example.invalid' } } })
    expect(document.documentElement.hidden).toBe(false)
    expect(postMessage).not.toHaveBeenCalled()
  })
  it('keeps the standalone return page visible', () => {
    const document = { documentElement: { hidden: false } }
    const window = { location: { pathname: '/payment/result' } } as { location: { pathname: string }; parent?: unknown }
    window.parent = window
    runInNewContext(bootstrap!, { document, window })
    expect(document.documentElement.hidden).toBe(false)
  })
})
