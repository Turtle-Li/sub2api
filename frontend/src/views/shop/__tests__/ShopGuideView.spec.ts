import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils'
import ShopGuideView from '../ShopGuideView.vue'

const route = vi.hoisted(() => ({ path: '/shop/help/chatgpt-plus-account', query: {} as Record<string, unknown> }))
vi.mock('vue-router', () => ({ useRoute: () => route }))
const options = { attachTo: document.body, global: { stubs: {
  AppLayout: { template: '<main><slot /></main>' },
  RouterLink: { props: ['to'], template: '<a :href="to"><slot /></a>' },
} } }
enableAutoUnmount(afterEach)
beforeEach(() => { route.path = '/shop/help/chatgpt-plus-account' })
describe('account guide navigation', () => {
  it('preserves the independent daypass guide and its chapter destinations', () => {
    route.path = '/shop/help/deepseek-daypass'
    route.query = { order: '42' }
    const wrapper = mount(ShopGuideView, options)
    expect(wrapper.get('h1').text()).toBe('DeepSeek API 日卡使用指南')
    expect(wrapper.find('#receive').exists()).toBe(true)
    expect(wrapper.find('#email-login').exists()).toBe(false)
    expect(wrapper.get('.guide-desktop-nav a[href="#configure"]').text()).toContain('配置客户端')
  })
  it.each([
    [{ order: '42' }, '/shop/orders/42', '返回订单'],
    [{ product: '2' }, '/shop/products/2', '返回商品'],
    [{ order: '//example.invalid' }, '/shop', '返回商城'],
    [{ order: ['42'] }, '/shop', '返回商城'],
    [{ order: '-42' }, '/shop', '返回商城'],
    [{ order: '9007199254740992' }, '/shop', '返回商城'],
    [{}, '/shop', '返回商城'],
  ])('offers a bounded return destination: %j', (query, href, label) => {
    route.query = query
    const wrapper = mount(ShopGuideView, options)
    expect(wrapper.get('.guide-back').attributes('href')).toBe(href)
    expect(wrapper.get('.guide-back').text()).toBe(label)
  })
  it('closes the mobile chapter chooser and permits repeated section changes', async () => {
    const wrapper = mount(ShopGuideView, options)
    const details = wrapper.get('details').element as HTMLDetailsElement
    const faq = wrapper.get('#faq').element
    const prepare = wrapper.get('#prepare').element
    faq.scrollIntoView = vi.fn()
    prepare.scrollIntoView = vi.fn()
    details.open = true
    await wrapper.get('.guide-mobile-nav a[href="#faq"]').trigger('click')
    await flushPromises()
    expect(details.open).toBe(false)
    expect(faq.scrollIntoView).toHaveBeenCalledWith({ block: 'start', behavior: 'instant' })
    details.open = true
    await wrapper.get('.guide-mobile-nav a[href="#prepare"]').trigger('click')
    await flushPromises()
    expect(details.open).toBe(false)
    expect(prepare.scrollIntoView).toHaveBeenCalledOnce()
  })
})
