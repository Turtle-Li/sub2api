import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import ResetCardTierPolicyPanel from '../ResetCardTierPolicyPanel.vue'
import type { SubscriptionPlan } from '@/types/payment'

const { getResetCardTierPolicies, updateResetCardTierPolicy, showError, showSuccess } = vi.hoisted(() => ({
  getResetCardTierPolicies: vi.fn(),
  updateResetCardTierPolicy: vi.fn(),
  showError: vi.fn(),
  showSuccess: vi.fn(),
}))

vi.mock('@/api/admin/payment', () => ({
  adminPaymentAPI: { getResetCardTierPolicies, updateResetCardTierPolicy },
}))

vi.mock('@/stores/app', () => ({
  useAppStore: () => ({ showError, showSuccess }),
}))

vi.mock('vue-i18n', () => ({
  useI18n: () => ({
    t: (key: string) => key === 'payment.errors.RESET_CARD_TIER_POLICY_FROZEN'
      ? 'localized frozen tier policy'
      : key,
  }),
}))

const plans = [
  { id: 10, name: 'Plus monthly', group_id: 4, group_name: 'GPT Plus' },
  { id: 11, name: 'Plus annual', group_id: 4, group_name: 'GPT Plus' },
  { id: 20, name: 'Pro monthly', group_id: 6, group_name: 'GPT Pro' },
] as SubscriptionPlan[]

function render() {
  return mount(ResetCardTierPolicyPanel, {
    props: { plans },
    global: { stubs: { Icon: true } },
  })
}

describe('ResetCardTierPolicyPanel', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    getResetCardTierPolicies.mockResolvedValue({
      data: [{ group_id: 4, family_key: 'gpt_standard', tier_rank: 2 }],
    })
    updateResetCardTierPolicy.mockResolvedValue({
      data: { group_id: 6, family_key: 'gpt_standard', tier_rank: 3 },
    })
  })

  it('loads one policy per current subscription group and marks missing policies as pending', async () => {
    const wrapper = render()
    await flushPromises()

    expect(getResetCardTierPolicies).toHaveBeenCalledWith()
    expect(wrapper.text()).toContain('GPT Plus')
    expect(wrapper.text()).toContain('GPT Pro')
    expect(wrapper.text()).toContain('payment.admin.resetCardTierConfigured')
    expect(wrapper.text()).toContain('payment.admin.resetCardTierUnconfigured')
    expect((wrapper.get('[data-testid="tier-family-4"]').element as HTMLInputElement).value).toBe('gpt_standard')
    expect((wrapper.get('[data-testid="tier-rank-4"]').element as HTMLInputElement).value).toBe('2')
  })

  it('requires a valid compatibility key and positive integer, then saves the group rule', async () => {
    const wrapper = render()
    await flushPromises()

    const save = wrapper.get('[data-testid="save-tier-6"]')
    expect((save.element as HTMLButtonElement).disabled).toBe(true)

    await wrapper.get('[data-testid="tier-family-6"]').setValue('gpt_standard')
    await wrapper.get('[data-testid="tier-rank-6"]').setValue('3')
    expect((save.element as HTMLButtonElement).disabled).toBe(false)

    await save.trigger('click')
    await flushPromises()

    expect(updateResetCardTierPolicy).toHaveBeenCalledWith(6, {
      family_key: 'gpt_standard',
      tier_rank: 3,
    })
    expect(showSuccess).toHaveBeenCalledWith('payment.admin.resetCardTierSaved')
    expect(wrapper.emitted('saved')).toEqual([[]])
  })

  it('keeps an API conflict visible and reports it through the shared error feedback', async () => {
    updateResetCardTierPolicy.mockRejectedValueOnce({
      reason: 'RESET_CARD_TIER_POLICY_CONFLICT',
      message: 'duplicate tier',
    })
    const wrapper = render()
    await flushPromises()

    await wrapper.get('[data-testid="tier-family-6"]').setValue('gpt_standard')
    await wrapper.get('[data-testid="tier-rank-6"]').setValue('3')
    await wrapper.get('[data-testid="save-tier-6"]').trigger('click')
    await flushPromises()

    expect(wrapper.get('[role="alert"]').text()).toContain('duplicate tier')
    expect(showError).toHaveBeenCalledWith('duplicate tier')
  })

  it('localizes a frozen tier policy instead of exposing the provider message', async () => {
    updateResetCardTierPolicy.mockRejectedValueOnce({
      reason: 'RESET_CARD_TIER_POLICY_FROZEN',
      message: 'tier policy is frozen after issued cards exist',
    })
    const wrapper = render()
    await flushPromises()

    await wrapper.get('[data-testid="tier-family-6"]').setValue('gpt_standard')
    await wrapper.get('[data-testid="tier-rank-6"]').setValue('3')
    await wrapper.get('[data-testid="save-tier-6"]').trigger('click')
    await flushPromises()

    expect(wrapper.get('[role="alert"]').text()).toContain('localized frozen tier policy')
    expect(wrapper.text()).not.toContain('tier policy is frozen after issued cards exist')
  })
})
