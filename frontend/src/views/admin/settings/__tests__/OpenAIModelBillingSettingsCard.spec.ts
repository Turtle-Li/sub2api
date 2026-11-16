import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import OpenAIModelBillingSettingsCard from '../OpenAIModelBillingSettingsCard.vue'

const mocks = vi.hoisted(() => ({
  get: vi.fn(),
  put: vi.fn(),
  showSuccess: vi.fn(),
  showError: vi.fn(),
}))

vi.mock('@/api', () => ({
  adminAPI: {
    settings: {
      getOpenAIModelBillingSettings: mocks.get,
      updateOpenAIModelBillingSettings: mocks.put,
    },
  },
}))
vi.mock('@/stores', () => ({ useAppStore: () => mocks }))
vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (key: string) => key }) }))

describe('OpenAIModelBillingSettingsCard', () => {
  beforeEach(() => {
    vi.resetAllMocks()
  })

  it('blocks edits after a failed load, then saves only selected deletions after retry', async () => {
    const luna = { 'gpt-5.6-luna': 1.5, 'gpt-6-luna': 1.5 }
    mocks.get.mockRejectedValueOnce(new Error('offline')).mockResolvedValueOnce({
      multipliers: { ...luna, 'gpt-6-sol': 1.8 },
    })
    mocks.put.mockResolvedValue({ multipliers: luna })

    const wrapper = mount(OpenAIModelBillingSettingsCard)
    await flushPromises()
    expect(wrapper.get('[role="alert"]').text()).not.toBe('')
    expect(wrapper.findAll('input')).toHaveLength(0)
    expect(wrapper.findAll('button').map((button) => button.text())).toEqual(['common.retry'])
    expect(mocks.put).not.toHaveBeenCalled()

    await wrapper.get('button').trigger('click')
    await flushPromises()
    expect(wrapper.find('[role="alert"]').exists()).toBe(false)
    expect(wrapper.findAll('input').filter((field) => field.element.type === 'text').map((field) => field.element.value))
      .toEqual(['gpt-5.6-luna', 'gpt-6-luna', 'gpt-6-sol'])

    await wrapper.findAll('[data-testid="openai-model-billing-delete"]').at(-1)?.trigger('click')
    await wrapper.get('[data-testid="openai-model-billing-save"]').trigger('click')
    await flushPromises()
    expect(mocks.put).toHaveBeenCalledOnce()
    expect(mocks.put).toHaveBeenCalledWith({ multipliers: luna })
    expect(mocks.showSuccess).toHaveBeenCalledOnce()
  })

  it('allows an explicitly loaded empty list to be saved', async () => {
    const empty = { multipliers: {} }
    mocks.get.mockResolvedValue(empty)
    mocks.put.mockResolvedValue(empty)

    const wrapper = mount(OpenAIModelBillingSettingsCard)
    await flushPromises()
    expect(wrapper.find('[role="alert"]').exists()).toBe(false)
    await wrapper.get('[data-testid="openai-model-billing-save"]').trigger('click')
    expect(mocks.put).toHaveBeenCalledOnce()
    expect(mocks.put).toHaveBeenCalledWith(empty)
  })
})
