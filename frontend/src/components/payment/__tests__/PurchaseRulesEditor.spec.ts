import { describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import PurchaseRulesEditor from '../PurchaseRulesEditor.vue'
vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (key: string) => key }) }))
describe('PurchaseRulesEditor', () => {
  it('preserves threshold while editing and deduplicating private user IDs', async () => {
    const wrapper = mount(PurchaseRulesEditor, { props: { modelValue: { min_total_recharge: 100, visible_user_ids: [12] } } })
    await wrapper.get('input[type="text"]').setValue('12, 35，12')
    expect(wrapper.emitted('update:modelValue')?.at(-1)?.[0]).toEqual({ min_total_recharge: 100, visible_user_ids: [12, 35] })
    await wrapper.get('input[type="text"]').setValue('')
    expect(wrapper.emitted('update:modelValue')?.at(-1)?.[0]).toEqual({ min_total_recharge: 100, visible_user_ids: [] })
  })
  it('blocks save for invalid IDs or fractional-cent amounts without silently applying an old rule', async () => {
    const wrapper = mount(PurchaseRulesEditor, { props: { modelValue: {} } })
    await wrapper.get('input[type="text"]').setValue('12, nope')
    expect(wrapper.emitted('validity')?.at(-1)).toEqual([false])
    expect(wrapper.emitted('update:modelValue')).toBeUndefined()
    await wrapper.get('input[type="text"]').setValue('12')
    await wrapper.get('input[type="number"]').setValue('10.001')
    expect(wrapper.emitted('validity')?.at(-1)).toEqual([false])
    await wrapper.get('input[type="number"]').setValue('100')
    expect(wrapper.emitted('validity')?.at(-1)).toEqual([true])
  })
})
