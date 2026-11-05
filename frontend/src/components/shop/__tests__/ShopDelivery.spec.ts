import { afterEach, describe, expect, it, vi } from 'vitest'
import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils'
import ShopDelivery from '../ShopDelivery.vue'

enableAutoUnmount(afterEach)
afterEach(() => vi.unstubAllGlobals())
describe('structured account delivery', () => {
  it('preserves API Key delivery as generic content even when it includes a delimiter', () => {
    const wrapper = mount(ShopDelivery, { props: { content: 'prefix----synthetic-api-key', apiKey: true } })
    expect(wrapper.text()).toContain('API Key 已交付')
    expect(wrapper.text()).not.toContain('复制账号')
    expect(wrapper.text()).not.toContain('复制密码')
    expect(wrapper.text()).toContain('复制完整原文')
    expect(wrapper.text()).not.toContain('synthetic-api-key')
  })
  it('offers only account and password copy without exposing masked secrets', async () => {
    const writeText = vi.fn().mockResolvedValue(undefined)
    vi.stubGlobal('navigator', { clipboard: { writeText } })
    const content = 'buyer@example.invalid---- pass----word \r\n'
    const wrapper = mount(ShopDelivery, { props: { content } })
    expect(wrapper.text()).not.toContain('buyer@example.invalid')
    expect(wrapper.text()).not.toContain('pass----word')
    expect(wrapper.text()).not.toContain('复制完整原文')
    const copy = async (text: string) => {
      await wrapper.findAll('button').find(button => button.text() === text)!.trigger('click')
      await flushPromises()
    }
    await copy('复制账号')
    expect(writeText).toHaveBeenLastCalledWith('buyer@example.invalid')
    expect(wrapper.find('[aria-label="账号已复制"]').text()).toBe('已复制')
    await copy('复制密码')
    expect(writeText).toHaveBeenLastCalledWith(' pass----word ')
    expect(wrapper.text()).not.toContain('pass----word')
    await copy('显示交付内容')
    expect(wrapper.text()).toContain('buyer@example.invalid')
    expect(wrapper.findAll('dd')[1]?.text()).toBe('pass----word')
    expect(wrapper.text()).not.toContain('复制完整原文')
    await wrapper.setProps({ content: 'next@example.invalid----next-password' })
    expect(wrapper.text()).not.toContain('next-password')
    expect(wrapper.text()).not.toContain('已复制')
  })
  it('retains generic raw copy for unsupported multiline delivery', async () => {
    const writeText = vi.fn().mockResolvedValue(undefined)
    vi.stubGlobal('navigator', { clipboard: { writeText } })
    const content = '账号：one\n密码：two'
    const wrapper = mount(ShopDelivery, { props: { content } })
    expect(wrapper.text()).not.toContain('复制账号')
    await wrapper.findAll('button').find(button => button.text() === '复制完整原文')!.trigger('click')
    await flushPromises()
    expect(writeText).toHaveBeenLastCalledWith(content)
    await wrapper.findAll('button')[0]!.trigger('click')
    await wrapper.findAll('button').find(button => button.text() === '复制第2行')!.trigger('click')
    await flushPromises()
    expect(writeText).toHaveBeenLastCalledWith('密码：two')
  })
  it('does not show late clipboard success on another order', async () => {
    let finish!: () => void
    vi.stubGlobal('navigator', { clipboard: { writeText: () => new Promise<void>(resolve => { finish = resolve }) } })
    const wrapper = mount(ShopDelivery, { props: { content: 'one----two' } })
    await wrapper.findAll('button').find(button => button.text() === '复制密码')!.trigger('click')
    await wrapper.setProps({ content: 'three----four' })
    finish()
    await flushPromises()
    expect(wrapper.text()).not.toContain('已复制')
  })
})
