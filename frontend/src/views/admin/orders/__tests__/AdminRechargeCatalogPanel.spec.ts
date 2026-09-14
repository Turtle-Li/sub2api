import { flushPromises, mount } from '@vue/test-utils'
import { createPinia } from 'pinia'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import AdminRechargeCatalogPanel from '../AdminRechargeCatalogPanel.vue'

const { getConfig, updateConfig } = vi.hoisted(() => ({
  getConfig: vi.fn(),
  updateConfig: vi.fn(),
}))

vi.mock('@/api/admin/payment', () => ({
  adminPaymentAPI: { getConfig, updateConfig },
}))

vi.mock('vue-i18n', async (importOriginal) => {
  const actual = await importOriginal<typeof import('vue-i18n')>()
  return {
    ...actual,
    useI18n: () => ({ t: (key: string) => key }),
  }
})

const editorStub = {
  name: 'RechargeOptionsEditor',
  props: ['modelValue'],
  emits: ['update:modelValue', 'validity'],
  template: '<div data-testid="catalog-editor">{{ modelValue }}</div>',
}

function render() {
  return mount(AdminRechargeCatalogPanel, {
    global: {
      plugins: [createPinia()],
      stubs: { RechargeOptionsEditor: editorStub, Icon: true },
    },
  })
}

describe('AdminRechargeCatalogPanel', () => {
  beforeEach(() => {
    getConfig.mockReset()
    updateConfig.mockReset()
    getConfig.mockResolvedValue({
      data: {
        recharge_options: [
          { amount: 20, label: 'Starter', enabled: true, recommended: true },
          { amount: 50, label: 'Plus', enabled: true },
        ],
        recharge_options_invalid: false,
      },
    })
    updateConfig.mockResolvedValue({ data: { message: 'updated' } })
  })

  it('loads and saves the existing recharge catalogue through the payment config API', async () => {
    const wrapper = render()
    await flushPromises()

    expect(wrapper.get('[data-testid="catalog-editor"]').text()).toContain('Starter')
    expect(wrapper.get('select').element.value).toBe('20')

    await wrapper.get('select').setValue('50')
    const saveButton = wrapper.findAll('button').find(button => button.text().includes('common.save'))
    expect(saveButton).toBeDefined()
    await saveButton!.trigger('click')
    await flushPromises()

    expect(updateConfig).toHaveBeenCalledTimes(1)
    expect(updateConfig).toHaveBeenCalledWith({
      recharge_options: [
        { amount: 20, label: 'Starter', enabled: true },
        { amount: 50, label: 'Plus', enabled: true, recommended: true },
      ],
    })
  })

  it('warns when the persisted catalogue was only partially parsed', async () => {
    getConfig.mockResolvedValueOnce({
      data: { recharge_options: [], recharge_options_invalid: true },
    })
    const wrapper = render()
    await flushPromises()

    expect(wrapper.text()).toContain('payment.admin.rechargeCatalogInvalid')
  })

  it('blocks saving while the catalogue editor reports invalid purchase rules', async () => {
    const wrapper = render()
    await flushPromises()

    wrapper.getComponent({ name: 'RechargeOptionsEditor' }).vm.$emit('validity', false)
    await wrapper.vm.$nextTick()

    const saveButton = wrapper.findAll('button').find(button => button.text().includes('common.save'))
    expect(saveButton?.attributes('disabled')).toBeDefined()
    await saveButton!.trigger('click')
    expect(updateConfig).not.toHaveBeenCalled()
  })

  it('submits an explicitly edited recharge catalogue from the product screen', async () => {
    const wrapper = render()
    await flushPromises()

    const editor = wrapper.getComponent({ name: 'RechargeOptionsEditor' })
    editor.vm.$emit('update:modelValue', '[{"amount":49,"enabled":true}]')
    editor.vm.$emit('validity', true)
    await wrapper.vm.$nextTick()

    const saveButton = wrapper.findAll('button').find(button => button.text().includes('common.save'))
    await saveButton!.trigger('click')
    await flushPromises()

    expect(updateConfig).toHaveBeenCalledWith({
      recharge_options: [{ amount: 49, enabled: true }],
    })
  })
})
