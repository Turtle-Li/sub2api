import { nextTick } from 'vue'
import { describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import { createPinia } from 'pinia'
import SupportedModelChip from '../SupportedModelChip.vue'
import { useAppStore } from '@/stores/app'
import type { PublicSettings } from '@/types'

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({ t: (key: string) => key })
  }
})

describe('SupportedModelChip', () => {
  it.each(['availableChannels.pricing', 'admin.availableChannels.pricing'])(
    'shows video prices per second using %s translations',
    async (pricingKeyPrefix) => {
      const wrapper = mount(SupportedModelChip, {
        attachTo: document.body,
        global: { plugins: [createPinia()] },
        props: {
          pricingKeyPrefix,
          model: {
            name: 'video-test',
            platform: '',
            pricing: {
              billing_mode: 'video',
              input_price: null,
              output_price: null,
              cache_write_price: null,
              cache_read_price: null,
              image_input_price: null,
              image_output_price: null,
              per_request_price: 0.05,
              intervals: [
                { tier_label: '480p', min_tokens: 0, max_tokens: null, input_price: null, output_price: null, cache_write_price: null, cache_read_price: null, per_request_price: 0 },
                { tier_label: '720p', min_tokens: 0, max_tokens: null, input_price: null, output_price: null, cache_write_price: null, cache_read_price: null, per_request_price: 0.12 }
              ]
            }
          }
        }
      })
      try {
        await wrapper.find('[tabindex="0"]').trigger('mouseenter')
        await nextTick()
        const tooltip = document.body.querySelector('[role="tooltip"]')
        expect(tooltip?.textContent).toContain(`${pricingKeyPrefix}.billingModeVideo`)
        expect(tooltip?.textContent).toContain(`${pricingKeyPrefix}.videoPrice`)
        expect(tooltip?.textContent).toContain(`$0.05 ${pricingKeyPrefix}.unitPerSecond`)
        expect(tooltip?.textContent).toContain('480p')
        expect(tooltip?.textContent).toContain(`$0 ${pricingKeyPrefix}.unitPerSecond`)
        expect(tooltip?.textContent).toContain('720p')
        expect(tooltip?.textContent).toContain(`$0.12 ${pricingKeyPrefix}.unitPerSecond`)
        const model = wrapper.props('model')
        await wrapper.setProps({
          model: { ...model, pricing: { ...model.pricing!, per_request_price: null } }
        })
        expect(tooltip?.textContent).not.toContain(`${pricingKeyPrefix}.videoPrice`)
        expect(tooltip?.textContent).toContain(`$0.12 ${pricingKeyPrefix}.unitPerSecond`)
        await wrapper.setProps({
          model: { ...model, pricing: { ...model.pricing!, per_request_price: 0 } }
        })
        expect(tooltip?.textContent).toContain(`${pricingKeyPrefix}.videoPrice`)
        expect(wrapper.findComponent({ name: 'PricingRow' }).text()).toContain(`$0 ${pricingKeyPrefix}.unitPerSecond`)
      } finally {
        wrapper.unmount()
      }
    }
  )

  function mountChip(
    pricingCurrency?: PublicSettings['pricing_currency'],
    sourceCurrency?: 'USD' | 'CNY'
  ) {
    const pinia = createPinia()
    const appStore = useAppStore(pinia)
    appStore.cachedPublicSettings = pricingCurrency
      ? ({ pricing_currency: pricingCurrency } as PublicSettings)
      : null

    return mount(SupportedModelChip, {
      attachTo: document.body,
      props: {
        model: {
          name: 'gpt-test',
          platform: '',
          pricing: {
            billing_mode: 'token',
            currency: sourceCurrency,
            input_price: 10e-6,
            output_price: 50e-6,
            cache_write_price: null,
            cache_read_price: null,
            image_input_price: null,
            image_output_price: null,
            per_request_price: null,
            intervals: [{
              min_tokens: 272000,
              max_tokens: null,
              input_price: null,
              output_price: null,
              cache_write_price: null,
              cache_read_price: null,
              input_multiplier: 2,
              output_multiplier: 1.5,
              per_request_price: null
            }]
          }
        },
        showPlatform: false
      },
      global: { plugins: [pinia] }
    })
  }

  it('仅配置区间倍率时按基础价展示 token 档位', async () => {
    const wrapper = mountChip()

    await wrapper.find('[tabindex="0"]').trigger('mouseenter')
    await nextTick()

    expect(document.body.textContent).toContain('$20 / $75')
    wrapper.unmount()
  })

  it('把基础价格和阶梯 resolver 价格按公开 CNY 配置换算后显示', async () => {
    const wrapper = mountChip({ settlement_currency: 'CNY', usd_to_cny_rate: 6.75 })

    await wrapper.find('[tabindex="0"]').trigger('mouseenter')
    await nextTick()

    // PricingRow 覆盖基础字段；popover 中的阶梯行走同一结算配置。
    expect(document.body.textContent).toContain('¥67.5')
    expect(document.body.textContent).toContain('¥135 / ¥506.25')
    expect(document.body.textContent).not.toContain('$20 / $75')
    wrapper.unmount()
  })

  it('keeps a CNY-authored card in CNY without applying the USD rate again', async () => {
    const wrapper = mountChip(
      { settlement_currency: 'CNY', usd_to_cny_rate: 6.75 },
      'CNY'
    )

    await wrapper.find('[tabindex="0"]').trigger('mouseenter')
    await nextTick()

    expect(document.body.textContent).toContain('¥10')
    expect(document.body.textContent).toContain('¥20 / ¥75')
    expect(document.body.textContent).not.toContain('¥67.5')
    wrapper.unmount()
  })
})
