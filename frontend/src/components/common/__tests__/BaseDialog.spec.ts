import { afterEach, describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import { nextTick } from 'vue'
import BaseDialog from '../BaseDialog.vue'

vi.mock('vue-i18n', () => ({
  useI18n: () => ({ t: (key: string) => key })
}))

describe('BaseDialog', () => {
  afterEach(() => {
    document.body.innerHTML = ''
    document.body.classList.remove('modal-open')
  })

  it('resets body scroll position when reopened', async () => {
    const wrapper = mount(BaseDialog, {
      attachTo: document.body,
      props: { show: false, title: 'Details' },
      slots: { default: '<div style="height: 2000px">content</div>' },
      global: { stubs: { Icon: true } }
    })

    await wrapper.setProps({ show: true })
    await nextTick()
    const body = document.body.querySelector<HTMLElement>('.modal-body')
    expect(body).not.toBeNull()
    body!.scrollTop = 480

    await wrapper.setProps({ show: false })
    await wrapper.setProps({ show: true })
    await nextTick()

    expect(document.body.querySelector<HTMLElement>('.modal-body')?.scrollTop).toBe(0)
    wrapper.unmount()
  })

  it('traps focus, restores the opener, and emits close on Escape', async () => {
    const opener = document.createElement('button')
    opener.textContent = 'Open details'
    document.body.appendChild(opener)
    opener.focus()

    const wrapper = mount(BaseDialog, {
      attachTo: document.body,
      props: { show: false, title: 'Details', showCloseButton: false, keepMounted: true },
      slots: {
        default: '<button data-test="first">First</button><button data-test="last">Last</button>',
      },
      global: { stubs: { Icon: true } },
    })

    await wrapper.setProps({ show: true })
    await nextTick()
    const first = document.body.querySelector<HTMLElement>('[data-test="first"]')!
    const last = document.body.querySelector<HTMLElement>('[data-test="last"]')!
    expect(document.activeElement).toBe(first)

    last.focus()
    document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Tab', bubbles: true }))
    expect(document.activeElement).toBe(first)

    first.focus()
    document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Tab', shiftKey: true, bubbles: true }))
    expect(document.activeElement).toBe(last)

    document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true }))
    expect(wrapper.emitted('close')).toHaveLength(1)

    await wrapper.setProps({ show: false })
    await nextTick()
    expect(document.activeElement).toBe(opener)
    expect(document.body.querySelector('.modal-overlay')).not.toBeNull()
    wrapper.unmount()
  })

  it('closes from a backdrop click by default but ignores a drag that started inside the panel', async () => {
    const wrapper = mount(BaseDialog, {
      attachTo: document.body,
      props: { show: true, title: 'Details' },
      slots: { default: '<button data-test="dialog-action">Continue</button>' },
      global: { stubs: { Icon: true } },
    })
    await nextTick()

    const overlay = document.body.querySelector<HTMLElement>('.modal-overlay')!
    const panel = document.body.querySelector<HTMLElement>('.modal-content')!

    panel.dispatchEvent(new Event('pointerdown', { bubbles: true }))
    overlay.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    expect(wrapper.emitted('close')).toBeUndefined()

    overlay.dispatchEvent(new Event('pointerdown', { bubbles: true }))
    overlay.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    expect(wrapper.emitted('close')).toHaveLength(1)

    wrapper.unmount()
  })

  it('only lets the topmost stacked dialog react to Escape', async () => {
    const outer = mount(BaseDialog, {
      attachTo: document.body,
      props: { show: true, title: 'Outer' },
      global: { stubs: { Icon: true } },
    })
    const inner = mount(BaseDialog, {
      attachTo: document.body,
      props: { show: true, title: 'Inner' },
      global: { stubs: { Icon: true } },
    })
    await nextTick()

    document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true }))

    expect(outer.emitted('close')).toBeUndefined()
    expect(inner.emitted('close')).toHaveLength(1)

    outer.unmount()
    inner.unmount()
  })

  it('restores a connected opener when an open dialog unmounts directly', async () => {
    const opener = document.createElement('button')
    opener.textContent = 'Open details'
    document.body.appendChild(opener)
    opener.focus()

    const wrapper = mount(BaseDialog, {
      attachTo: document.body,
      props: { show: true, title: 'Details', showCloseButton: false },
      slots: { default: '<button data-test="dialog-action">Continue</button>' },
      global: { stubs: { Icon: true } },
    })
    await nextTick()
    expect(document.activeElement).toBe(document.body.querySelector<HTMLElement>('[data-test="dialog-action"]'))

    wrapper.unmount()

    expect(document.activeElement).toBe(opener)
  })

  it('adds the mobile sheet classes used for safe-area layout', () => {
    const wrapper = mount(BaseDialog, {
      attachTo: document.body,
      props: { show: true, title: 'Details', mobileSheet: true },
      global: { stubs: { Icon: true } },
    })

    expect(document.body.querySelector('.modal-overlay--mobile-sheet')).not.toBeNull()
    expect(document.body.querySelector('.modal-content--mobile-sheet')).not.toBeNull()
    wrapper.unmount()
  })

  it('keeps page scrolling locked until stacked dialogs have both closed', async () => {
    const first = mount(BaseDialog, {
      attachTo: document.body,
      props: { show: true, title: 'First' },
      global: { stubs: { Icon: true } },
    })
    const second = mount(BaseDialog, {
      attachTo: document.body,
      props: { show: true, title: 'Second' },
      global: { stubs: { Icon: true } },
    })

    await nextTick()
    expect(document.body.classList.contains('modal-open')).toBe(true)
    await first.setProps({ show: false })
    expect(document.body.classList.contains('modal-open')).toBe(true)
    await second.setProps({ show: false })
    expect(document.body.classList.contains('modal-open')).toBe(false)

    first.unmount()
    second.unmount()
  })
})
