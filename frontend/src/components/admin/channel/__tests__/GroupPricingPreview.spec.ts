import { mount } from '@vue/test-utils'
import { describe, expect, it, vi } from 'vitest'
import GroupPricingPreview from '../GroupPricingPreview.vue'
import type { PricingFormEntry } from '../types'
import type { ChannelModelPricing } from '@/api/admin/channels'

vi.mock('@/stores/app', () => ({ useAppStore: () => ({
  cachedPublicSettings: { pricing_currency: { settlement_currency: 'CNY', usd_to_cny_rate: 6.75 } },
}) }))
vi.mock('vue-i18n', async importOriginal => ({
  ...await importOriginal<typeof import('vue-i18n')>(),
  useI18n: () => ({ t: (key: string) => key }),
}))

const entry: PricingFormEntry = {
  models: ['kimi-k3.1-pro'], billing_mode: 'token', currency: 'CNY',
  input_price: 13, output_price: 68, cache_write_price: null, cache_read_price: null,
  image_input_price: null, image_output_price: null, per_request_price: null,
  intervals: [], time_pricing: { timezone: 'Asia/Shanghai', weekdays_only: false, periods: [] },
}
const kimi = { id: 18, name: 'KIMI', platform: 'kimi', subscription_type: 'standard', rate_multiplier: .3 }
const groupCard: ChannelModelPricing = {
  platform: 'kimi', models: [], billing_mode: 'token', currency: 'CNY',
  input_price: 1e-6, output_price: 2e-6, cache_write_price: null, cache_read_price: null,
  image_input_price: null, image_output_price: null, per_request_price: null,
  intervals: [], time_pricing: null,
}

describe('GroupPricingPreview', () => {
  it('previews native CNY prices using the linked group multiplier and reacts to its changes', async () => {
    const wrapper = mount(GroupPricingPreview, { props: { entry, groups: [kimi] } })
    expect(wrapper.text()).toContain('$3.90')
    expect(wrapper.text()).toContain('$20.40')
    await wrapper.setProps({ groups: [{ ...kimi, rate_multiplier: .5 }] })
    expect(wrapper.text()).toContain('$6.50')
    expect(wrapper.text()).toContain('$34.00')
    expect(entry.input_price).toBe(13)
  })

  it('uses direct reference units for standard Claude groups and USD units for subscription groups', () => {
    const wrapper = mount(GroupPricingPreview, { props: {
      entry: { ...entry, currency: 'USD', input_price: 3, output_price: 15 },
      groups: [
        { ...kimi, id: 1, name: 'Claude', platform: 'anthropic' },
        { ...kimi, id: 2, name: 'Subscription', subscription_type: 'subscription' },
      ],
    } })
    const rows = wrapper.findAll('li')
    for (const row of rows) {
      expect(row.text()).toContain('$0.90')
      expect(row.text()).toContain('$4.50')
    }
  })

  it('keeps blank prices distinct from explicit free prices', () => {
    const wrapper = mount(GroupPricingPreview, { props: {
      entry: { ...entry, input_price: null, output_price: 0 }, groups: [kimi],
    } })
    expect(wrapper.text()).toContain('admin.channels.form.inputPrice -')
    expect(wrapper.text()).toContain('$0.00')
  })

  it.each(['kimi-k3.1-pro', 'kimi-*', '*'])('defers to matching group price cards for %s', model => {
    const card = { ...groupCard, models: [model] }
    const wrapper = mount(GroupPricingPreview, { props: {
      entry, groups: [{ ...kimi, model_pricing: [card] }],
    } })
    expect(wrapper.text()).toContain('admin.channels.form.groupPricingOverride')
    expect(wrapper.text()).not.toContain('$3.90')
  })

  it('keeps the channel preview for unmatched group cards', () => {
    const card = { ...groupCard, models: ['kimi-k3'] }
    const wrapper = mount(GroupPricingPreview, { props: {
      entry, groups: [{ ...kimi, model_pricing: [card] }],
    } })
    expect(wrapper.text()).toContain('$3.90')
  })

  it('normalizes Claude dots, case and whitespace when checking group overrides', () => {
    const wrapper = mount(GroupPricingPreview, { props: {
      entry: { ...entry, models: ['claude-sonnet-4.5'] },
      groups: [{ ...kimi, model_pricing: [{ ...groupCard, models: [' CLAUDE-SONNET-4-5 '] }] }],
    } })
    expect(wrapper.text()).toContain('admin.channels.form.groupPricingOverride')
    expect(wrapper.text()).not.toContain('$3.90')
  })
})
