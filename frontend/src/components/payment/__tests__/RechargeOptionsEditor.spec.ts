import { describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import RechargeOptionsEditor from '../RechargeOptionsEditor.vue'
import PurchaseRulesEditor from '../PurchaseRulesEditor.vue'
vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (key: string) => key }) }))
describe('RechargeOptionsEditor', () => {
  it('edits display content without dropping advanced fields or purchase rules', async () => {
    const source = [{ amount: 599, label: 'Pro', estimated_tokens: 123456, purchase_rules: { visible_user_ids: [42], min_total_recharge: 1000 } }]
    const wrapper = mount(RechargeOptionsEditor, { props: { modelValue: JSON.stringify(source) } })
    await wrapper.findAll('input[type="text"]')[0].setValue('New title')
    const changed = JSON.parse(wrapper.emitted('update:modelValue')!.at(-1)![0] as string)
    expect(changed[0]).toEqual({ ...source[0], label: 'New title' })
    wrapper.findComponent(PurchaseRulesEditor).vm.$emit('validity', false)
    expect(wrapper.emitted('validity')?.at(-1)).toEqual([false])
  })
  it('does not replace malformed raw JSON and recovers when the raw value is corrected', async () => {
    const wrapper = mount(RechargeOptionsEditor, { props: { modelValue: '{broken' } })
    expect(wrapper.find('[role="alert"]').exists()).toBe(true)
    expect(wrapper.emitted('update:modelValue')).toBeUndefined()
    expect(wrapper.find('button').exists()).toBe(false)
    await wrapper.setProps({ modelValue: '[]' })
    expect(wrapper.emitted('validity')?.at(-1)).toEqual([true])
    await wrapper.get('button').trigger('click')
    expect(JSON.parse(wrapper.emitted('update:modelValue')!.at(-1)![0] as string)[0].enabled).toBe(false)
  })
})
