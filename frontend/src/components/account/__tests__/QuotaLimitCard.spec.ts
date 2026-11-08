import { describe, expect, it } from 'vitest'
import { mount } from '@vue/test-utils'
import { createI18n } from 'vue-i18n'
import QuotaLimitCard from '../QuotaLimitCard.vue'
import zh from '@/i18n/locales/zh'

describe('QuotaLimitCard daily calls', () => {
  it('opens for a count-only limit, emits integers and clears the limit when disabled', async () => {
    const wrapper = mount(QuotaLimitCard, {
      props: {
        totalLimit: null, dailyLimit: null, dailyRequestLimit: 100, weeklyLimit: null,
        dailyResetMode: 'fixed', dailyResetHour: 0, weeklyResetMode: null,
        weeklyResetDay: null, weeklyResetHour: null, resetTimezone: 'Asia/Shanghai'
      },
      global: { plugins: [createI18n({ legacy: false, locale: 'zh', messages: { zh } })] }
    })

    expect((wrapper.get('select').element as HTMLSelectElement).value).toBe('fixed')
    expect(wrapper.findAll('select').map(select => (select.element as HTMLSelectElement).value)).toContain('Asia/Shanghai')
    await wrapper.get('select').setValue('rolling')
    expect(wrapper.emitted('update:dailyResetMode')?.[0]).toEqual(['rolling'])

    const input = wrapper.get('input[step="1"]')
    await input.setValue('25')

    expect(wrapper.emitted('update:dailyRequestLimit')?.[0]).toEqual([25])
    expect(wrapper.get('[role="switch"]').attributes('aria-checked')).toBe('true')
    await wrapper.get('[role="switch"]').trigger('click')
    expect(wrapper.emitted('update:dailyRequestLimit')?.[1]).toEqual([null])
    expect(wrapper.find('input[step="1"]').exists()).toBe(false)
  })
})
